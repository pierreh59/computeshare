//! Sprint 19 (v2) - Execution des chunks de type conteneur.
//! Un chunk runtime=="container" tourne via `docker run` (Docker Desktop,
//! cote Windows) dans un dossier sandbox monte sur /work. Meme modele de
//! resultat que le sandbox classique : outcome + detail + SHA-256.
//!
//! Sprint 19b : on n'utilise PLUS `wsl docker`. Vu en vrai le 6 nov. chez
//! Pierre : la distribution WSL par defaut de son PC est celle de Salad
//! (compte verrouille, "This account is currently not available"), donc
//! `wsl <cmd>` ne peut pas marcher. docker.exe (installe par Docker
//! Desktop dans le PATH Windows) fonctionne lui sans WSL par defaut.

use crate::sandbox::{hash_file, sanitize_id, tail_utf8, ChunkResult, ChunkSpec};
use std::time::{Duration, Instant};

const DEFAULT_TIMEOUT_SECS: u64 = 300;
const MAX_TIMEOUT_SECS: u64 = 3600;

/// Valide une reference d'image conteneur envoyee par le serveur.
/// Pas d'espaces ni de caracteres interpretables par le shell.
pub(crate) fn image_valide(image: &str) -> bool {
    !image.is_empty()
        && image.len() <= 500
        && !image.starts_with('-')
        && !image.chars().any(|c| {
            c.is_whitespace()
                || matches!(c, '"' | '\'' | '\\' | '`' | ';' | '$' | '|' | '&' | '<' | '>')
        })
}

/// Sprint 27 : nom de fichier d'asset assaini - alphanumerique,
/// point, tiret, underscore, 64 caracteres max. Pas de separateur,
/// pas de traversal ("../"), pas de caracteres speciaux Windows : le
/// nom vient du client, on ne le fait JAMAIS confiance.
pub(crate) fn sanitize_asset_filename(name: &str) -> String {
    let s: String = name
        .chars()
        .filter(|c| c.is_ascii_alphanumeric() || *c == '.' || *c == '-' || *c == '_')
        .take(64)
        .collect();
    s.trim_start_matches('.').to_string()
}

/// Sprint 28 : chemin de Docker Desktop (installation standard).
#[cfg(windows)]
fn docker_desktop_exe() -> std::path::PathBuf {
    let base = std::env::var("PROGRAMFILES").unwrap_or_else(|_| r"C:\Program Files".to_string());
    std::path::PathBuf::from(base)
        .join("Docker")
        .join("Docker")
        .join("Docker Desktop.exe")
}

/// Sprint 28 : Docker Desktop est-il installe sur ce PC ?
#[cfg(windows)]
fn docker_desktop_present() -> bool {
    docker_desktop_exe().exists()
        || std::path::PathBuf::from(r"C:\Program Files\Docker\Docker\Docker Desktop.exe")
            .exists()
}

/// Sprint 28 : le daemon docker repond-il ? `docker version` ne
/// reussit que si le moteur tourne (docker --version reussirait meme
/// daemon arrete).
#[cfg(windows)]
fn docker_daemon_pret() -> bool {
    use std::os::windows::process::CommandExt;
    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    std::process::Command::new("docker")
        .arg("version")
        .creation_flags(CREATE_NO_WINDOW)
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

/// Sprint 28c : desactive le reglage Docker "Open Docker Dashboard
/// at startup" (cle openUIOnStartupDisabled dans settings-store.json,
/// settings.json sur les versions anciennes) pour que notre relance
/// silencieuse n'ouvre AUCUNE fenetre. A appeler uniquement quand le
/// daemon est arrete : Docker relit ses reglages au demarrage et
/// reecrirait le fichier a son arret. Non fatal : en cas d'echec on
/// relance quand meme Docker Desktop. Retourne true si un reglage a
/// ete modifie (pour la trace).
#[cfg(windows)]
fn docker_desktop_desactive_fenetre() -> bool {
    let appdata = match std::env::var("APPDATA") {
        Ok(v) => v,
        Err(_) => return false,
    };
    let mut modifie = false;
    for nom in ["settings-store.json", "settings.json"] {
        let chemin = std::path::PathBuf::from(&appdata)
            .join("Docker")
            .join(nom);
        let contenu = match std::fs::read_to_string(&chemin) {
            Ok(c) => c,
            Err(_) => continue,
        };
        let mut racine: serde_json::Value = match serde_json::from_str(&contenu) {
            Ok(v) => v,
            Err(_) => continue,
        };
        let deja = racine
            .get("openUIOnStartupDisabled")
            .and_then(|v| v.as_bool())
            .unwrap_or(false);
        if deja {
            continue;
        }
        // Sauvegarde unique avant la premiere modification.
        let bak = chemin.with_extension("json.computeshare-bak");
        if !bak.exists() {
            let _ = std::fs::copy(&chemin, &bak);
        }
        if let Some(obj) = racine.as_object_mut() {
            obj.insert(
                "openUIOnStartupDisabled".to_string(),
                serde_json::Value::Bool(true),
            );
            if let Ok(nouveau) = serde_json::to_string_pretty(&racine) {
                let _ = std::fs::write(&chemin, nouveau);
                modifie = true;
            }
        }
    }
    modifie
}

/// Sprint 33 : lit le flux stderr de `docker pull`, met a jour
/// l'action affichee en direct (parsing des lignes de progression
/// de docker) et retourne les dernieres lignes brutes pour le
/// diagnostic en cas d'echec.
#[cfg(windows)]
fn maj_progression_pull<R: std::io::Read>(flux: R, image: &str) -> Vec<String> {
    use std::io::{BufRead, BufReader};
    let mut dernieres: Vec<String> = Vec::new();
    let mut couches: std::collections::HashMap<String, (f64, f64)> =
        std::collections::HashMap::new();
    let mut derniere_maj: Option<Instant> = None;
    for ligne in BufReader::new(flux).lines() {
        let Ok(l) = ligne else { break };
        let l = l.trim_end_matches('\r').to_string();
        dernieres.push(l.clone());
        if dernieres.len() > 5 {
            dernieres.remove(0);
        }
        if !maj_couches_pull(&l, &mut couches) {
            continue;
        }
        // Rafraichissement limite (400 ms) : un gros pull emit des
        // centaines de lignes, inutile de spammer le statut.
        let doit = match derniere_maj {
            None => true,
            Some(t) => t.elapsed().as_millis() >= 400,
        };
        if !doit {
            continue;
        }
        derniere_maj = Some(Instant::now());
        let (recu, total) = couches
            .values()
            .fold((0.0, 0.0), |(a, b), (x, y)| (a + x, b + y));
        crate::node::set_action(&crate::node::fmt_progress(
            &format!("Telechargement de l'image {}", image),
            recu as u64,
            total as u64,
        ));
    }
    dernieres
}

/// Sprint 33 : analyse une ligne de progression docker pull, format
/// "<id>: Downloading [=====>  ] 12.3MB/45.6MB" (idem "Extracting",
/// "Pull complete"). Retourne true si la ligne a ete comprise.
#[cfg(windows)]
fn maj_couches_pull(
    ligne: &str,
    couches: &mut std::collections::HashMap<String, (f64, f64)>,
) -> bool {
    let Some((id, reste)) = ligne.split_once(": ") else { return false };
    if reste.starts_with("Pull complete") || reste.starts_with("Verifying Checksum") {
        if let Some((recu, total)) = couches.get_mut(id) {
            *recu = *total;
        }
        return true;
    }
    if !reste.starts_with("Downloading") && !reste.starts_with("Extracting") {
        return false;
    }
    let Some(fin) = reste.rfind("] ") else { return false };
    let morceaux: Vec<&str> = reste[fin + 2..].split('/').collect();
    if morceaux.len() != 2 {
        return false;
    }
    let (Some(recu), Some(total)) = (parse_taille(morceaux[0]), parse_taille(morceaux[1])) else {
        return false;
    };
    let total = if total <= 0.0 { recu } else { total };
    couches.insert(id.to_string(), (recu.min(total), total));
    true
}

/// Sprint 33 : "12.3MB" / "1.2GB" / "345kB" -> octets (base 1024).
#[cfg(windows)]
fn parse_taille(s: &str) -> Option<f64> {
    let t = s.trim();
    for (suffixe, mult) in [("GB", 1073741824.0), ("MB", 1048576.0), ("kB", 1024.0), ("B", 1.0)] {
        if let Some(v) = t.strip_suffix(suffixe) {
            return v.trim().parse::<f64>().ok().map(|n| n * mult);
        }
    }
    t.parse::<f64>().ok()
}

/// Sprint 19 : execution conteneur. Windows uniquement, via docker.exe
/// (Docker Desktop). Le dossier sandbox Windows est monte directement
/// sur /work (Docker Desktop accepte les chemins Windows en /).
#[cfg(windows)]
pub(crate) fn run_container_chunk(
    spec: &ChunkSpec,
    image: &str,
    gpu: bool,
    assets: &[crate::node::FetchedAsset],
) -> ChunkResult {
    use std::os::windows::process::CommandExt;
    use std::process::{Command, Stdio};

    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    let started = Instant::now();
    // Sprint 30 : decomposition du temps rapporte au serveur -
    // pull (telechargement de l'image) vs calcul reel (docker run).
    let pull_ms = std::cell::Cell::new(0u64);
    let compute_ms = std::cell::Cell::new(0u64);

    let mk = |outcome: &str, detail: &str, sha: Option<String>, path: Option<String>| ChunkResult {
        chunk_id: spec.chunk_id.clone(),
        outcome: outcome.to_string(),
        detail: detail.to_string(),
        duration_ms: started.elapsed().as_millis(),
        pull_ms: pull_ms.get(),
        compute_ms: compute_ms.get(),
        output_sha256: sha,
        output_path: path,
    };

    if !image_valide(image) {
        return mk("rejected", "reference d'image conteneur invalide", None, None);
    }

    // Docker Desktop doit etre lance. Sprint 28 : si le daemon ne
    // repond pas, on relance Docker Desktop EN SILENCE (CREATE_NO_WINDOW
    // sur l'executable ; le reglage Docker "Open Docker Dashboard at
    // startup" decoche evite de surcroit toute fenetre) et on attend
    // sa reponse jusqu'a 90 secondes avant d'abandonner proprement.
    if !docker_daemon_pret() {
        if docker_desktop_present() {
            crate::node::trace(&format!(
                "{} daemon docker arrete : relance silencieuse de Docker Desktop",
                spec.chunk_id
            ));
            // Sprint 28c : d'abord eviter que Docker ouvre son
            // dashboard (fenetre) au demarrage.
            if docker_desktop_desactive_fenetre() {
                crate::node::trace(&format!(
                    "{} reglage Docker applique : pas de fenetre au demarrage",
                    spec.chunk_id
                ));
            }
            let _ = Command::new(&docker_desktop_exe())
                .creation_flags(CREATE_NO_WINDOW)
                .stdin(Stdio::null())
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .spawn();
            let mut pret = false;
            for i in 0..18 {
                std::thread::sleep(Duration::from_secs(5));
                if docker_daemon_pret() {
                    crate::node::trace(&format!(
                        "{} daemon docker pret apres relance ({} s)",
                        spec.chunk_id,
                        (i + 1) * 5
                    ));
                    pret = true;
                    break;
                }
            }
            if !pret {
                return mk(
                    "failed",
                    "Docker Desktop n'a pas repondu 90 s apres relance automatique : ouvrez-le une fois manuellement (au premier lancement, validez ses conditions), puis laissez-le tourner",
                    None,
                    None,
                );
            }
        } else {
            return mk(
                "failed",
                "docker introuvable ou arrete (installez/lancez Docker Desktop)",
                None,
                None,
            );
        }
    }

    // Sprint 30 : pull SEPARAE du run pour mesurer le temps de
    // telechargement de l'image. Ce temps est facture au client a
    // tarif reduit (20 %) et n'est jamais paye au vendeur. Si
    // l'image est deja en cache local, le pull est quasi instantane.
    // Sprint 33 : la progression du pull est affichee en direct dans
    // l'app (parsing des lignes de progression de docker).
    let pull_started = Instant::now();
    let mut pull_detail = String::new();
    let pull_ok = {
        let mut child = match Command::new("docker")
            .args(["pull", image])
            .creation_flags(CREATE_NO_WINDOW)
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::piped())
            .spawn()
        {
            Ok(c) => c,
            Err(e) => {
                return mk("failed", &format!("lancement de docker pull : {e}"), None, None);
            }
        };
        // Sprint 33 : lecture du flux de progression ligne par ligne,
        // mise a jour de l'action affichee (throttle 400 ms).
        let dernieres = match child.stderr.take() {
            Some(se) => maj_progression_pull(se, image),
            None => Vec::new(),
        };
        pull_detail = dernieres.join(" | ");
        match child.wait() {
            Ok(s) => s.success(),
            Err(e) => {
                return mk("failed", &format!("attente docker pull : {e}"), None, None);
            }
        }
    };
    pull_ms.set(pull_started.elapsed().as_millis() as u64);
    if !pull_ok {
        return mk(
            "failed",
            &format!(
                "pull de l'image impossible | {}",
                tail_utf8(pull_detail.as_bytes())
            ),
            None,
            None,
        );
    }
    crate::node::trace(&format!(
        "{} image telechargee en {} ms",
        spec.chunk_id,
        pull_ms.get()
    ));

    // Dossier sandbox Windows, monte tel quel dans le conteneur.
    let dir = std::env::temp_dir()
        .join("computeshare-sandbox")
        .join(sanitize_id(&spec.chunk_id));
    let _ = std::fs::remove_dir_all(&dir);
    if std::fs::create_dir_all(&dir).is_err() {
        return mk("failed", "impossible de creer le repertoire sandbox", None, None);
    }
    // Docker accepte les chemins Windows avec des / : C:/Users/...
    let win_mount = dir.to_string_lossy().replace('\\', "/");

    // Sprint 27 : pose des assets d'entree dans le dossier monte sur
    // /work AVANT le lancement du conteneur. Ils sont deja telecharges
    // (sha256 verifie) cote node.
    for a in assets {
        let dest = dir.join(&a.filename);
        if let Err(e) = std::fs::copy(&a.local_path, &dest) {
            return mk(
                "failed",
                &format!("copie de l'asset {} : {e}", a.filename),
                None,
                None,
            );
        }
    }

    let cname = format!("cs-{}", sanitize_id(&spec.chunk_id));

    let timeout = if spec.timeout_secs == 0 {
        Duration::from_secs(DEFAULT_TIMEOUT_SECS)
    } else {
        Duration::from_secs(spec.timeout_secs.min(MAX_TIMEOUT_SECS))
    };

    // Nom du fichier resultat : celui du job, ou nom par defaut.
    let out_name: String = if spec.expected_output.is_empty() {
        "output.txt".to_string()
    } else {
        spec.expected_output
            .chars()
            .filter(|c| c.is_ascii_alphanumeric() || *c == '.' || *c == '-' || *c == '_')
            .take(64)
            .collect()
    };

    // Sprint 20 (v2) : durcissement securite.
    // - reseau coupe : le conteneur n'a acces ni a Internet ni au LAN
    //   du fournisseur (anti-exfiltration).
    // - cap-drop ALL + no-new-privileges : aucun privilege Linux,
    //   interdiction de s'elever.
    // - pids-limit : pas de bombe a processus.
    // - read-only : systeme de fichiers fige, seul /work (le dossier
    //   resultat, monte en lecture-ecriture) et /tmp (ephemere) sont
    //   inscriptibles.
    let mut builder = Command::new("docker");
    builder
        .arg("run")
        .args([
            "--rm",
            "--name",
            &cname,
            "--memory",
            "2g",
            "--cpus",
            "2",
            "--network",
            "none",
            "--cap-drop",
            "ALL",
            "--security-opt",
            "no-new-privileges",
            "--pids-limit",
            "256",
            "--read-only",
            "--tmpfs",
            "/tmp:rw,size=64m",
            "-v",
            &format!("{win_mount}:/work"),
            "-w",
            "/work",
        ]);
    // Sprint 25 : le GPU n'est expose QUE si le job le demande
    // ("gpu": true). Les jobs CPU restent sans GPU (et factures au
    // tarif CPU). Tous les garde-fous du sprint 20 sont conserves.
    // NB : le drapeau doit preceder le nom de l'image.
    if gpu {
        builder.args(["--gpus", "all"]);
    }
    builder
        .arg(image)
        .arg(&spec.command)
        .args(&spec.args);

    builder.creation_flags(CREATE_NO_WINDOW);

    // Sprint 33 : action affichee pendant le calcul (la charge GPU
    // reste visible en direct dans le tableau de bord).
    crate::node::set_action(if gpu {
        "Calcul en cours (GPU demande pour ce job)..."
    } else {
        "Calcul en cours (CPU)..."
    });

    // Sprint 30 : le temps de calcul mesure l'execution du conteneur
    // (docker run), hors telechargement de l'image.
    let compute_started = Instant::now();

    let mut child = match builder
        .current_dir(&dir)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
    {
        Ok(c) => c,
        Err(e) => return mk("failed", &format!("spawn docker: {e}"), None, None),
    };

    // Chien de garde : taskkill sur l'arbre docker.exe + docker rm -f.
    let pid = child.id();
    let cname_w = cname.clone();
    std::thread::spawn(move || {
        std::thread::sleep(timeout);
        let mut killer = Command::new("taskkill");
        killer.args(["/PID", &pid.to_string(), "/T", "/F"]);
        killer.stdout(Stdio::null()).stderr(Stdio::null());
        killer.creation_flags(CREATE_NO_WINDOW);
        let _ = killer.status();
        let _ = Command::new("docker")
            .args(["rm", "-f", &cname_w])
            .creation_flags(CREATE_NO_WINDOW)
            .status();
    });

    let output = match child.wait_with_output() {
        Ok(o) => o,
        Err(e) => return mk("failed", &format!("wait: {e}"), None, None),
    };
    compute_ms.set(compute_started.elapsed().as_millis() as u64);

    if started.elapsed() >= timeout {
        return mk("timeout", "delai depasse, conteneur interrompu", None, None);
    }
    if !output.status.success() {
        let detail = format!(
            "docker exit {:?} | {}",
            output.status.code(),
            tail_utf8(&output.stderr)
        );
        return mk("failed", &detail, None, None);
    }

    // Fichier resultat attendu dans le dossier monte (cote Windows).
    let out_path = dir.join(&out_name);
    match hash_file(&out_path) {
        // Sprint 26 : fichier de resultat rapatrie apres rapport.
        Some(sha) => mk("ok", "chunk conteneur termine", Some(sha),
            Some(out_path.to_string_lossy().into_owned())),
        None => mk("bad_output", "fichier resultat manquant", None, None),
    }
}

#[cfg(not(windows))]
pub(crate) fn run_container_chunk(
    _spec: &ChunkSpec,
    _image: &str,
    _gpu: bool,
    _assets: &[crate::node::FetchedAsset],
) -> ChunkResult {
    ChunkResult {
        chunk_id: _spec.chunk_id.clone(),
        outcome: "rejected".into(),
        detail: "conteneurs supportes uniquement sous Windows avec WSL2".into(),
        duration_ms: 0,
        pull_ms: 0,
        compute_ms: 0,
        output_sha256: None,
        output_path: None,
    }
}

#[cfg(test)]
mod tests {
    use super::{image_valide, sanitize_asset_filename};

    #[test]
    fn accepte_images_normales() {
        assert!(image_valide("alpine"));
        assert!(image_valide("alpine:3.20"));
        assert!(image_valide("library/alpine:latest"));
        assert!(image_valide("registry.example.com/team/worker:1.2.3"));
        assert!(image_valide(&"a".repeat(500)));
    }

    #[test]
    fn refuse_vide_et_trop_long() {
        assert!(!image_valide(""));
        assert!(!image_valide(&"a".repeat(501)));
    }

    #[test]
    fn refuse_tiret_initial() {
        // Une image ne doit jamais pouvoir etre confondue avec un
        // drapeau docker (ex. --privileged).
        assert!(!image_valide("-privileged"));
        assert!(!image_valide("--network=host"));
    }

    #[test]
    fn refuse_espaces_et_caracteres_dangereux() {
        for bad in [
            "alpine latest",
            "alpine;rm -rf /",
            "alpine$(whoami)",
            "alpine`id`",
            "alpine|cat",
            "alpine&cat",
            "alpine>out",
            "alpine<in",
            "a\"b",
            "a'b",
            "a\\b",
        ] {
            assert!(!image_valide(bad), "devrait refuser : {bad}");
        }
    }

    // Sprint 27 : noms de fichiers d'assets assainis.
    #[test]
    fn assainit_noms_d_assets() {
        assert_eq!(sanitize_asset_filename("donnees.csv"), "donnees.csv");
        // Tentative de traversal : separateurs filtres, points de
        // tete supprimes.
        assert_eq!(sanitize_asset_filename("..\\..\\win.ini"), "win.ini");
        assert_eq!(sanitize_asset_filename("../../etc/passwd"), "etcpasswd");
        assert_eq!(sanitize_asset_filename("rapport final.pdf"), "rapportfinal.pdf");
        assert_eq!(sanitize_asset_filename(""), "");
        assert_eq!(sanitize_asset_filename(".."), "");
        assert_eq!(
            sanitize_asset_filename(&"a".repeat(100)),
            "a".repeat(64)
        );
    }
}
