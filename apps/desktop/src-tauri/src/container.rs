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

/// Sprint 19 : execution conteneur. Windows uniquement, via docker.exe
/// (Docker Desktop). Le dossier sandbox Windows est monte directement
/// sur /work (Docker Desktop accepte les chemins Windows en /).
#[cfg(windows)]
pub(crate) fn run_container_chunk(spec: &ChunkSpec, image: &str, gpu: bool) -> ChunkResult {
    use std::os::windows::process::CommandExt;
    use std::process::{Command, Stdio};

    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    let started = Instant::now();

    let mk = |outcome: &str, detail: &str, sha: Option<String>, path: Option<String>| ChunkResult {
        chunk_id: spec.chunk_id.clone(),
        outcome: outcome.to_string(),
        detail: detail.to_string(),
        duration_ms: started.elapsed().as_millis(),
        output_sha256: sha,
        output_path: path,
    };

    if !image_valide(image) {
        return mk("rejected", "reference d'image conteneur invalide", None, None);
    }

    // Docker Desktop doit etre lance : `docker version` ne reussit que
    // si le daemon repond (docker --version reussirait meme daemon arrete).
    let docker_ok = Command::new("docker")
        .arg("version")
        .creation_flags(CREATE_NO_WINDOW)
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false);
    if !docker_ok {
        return mk(
            "failed",
            "docker introuvable ou arrete (installez/lancez Docker Desktop)",
            None,
            None,
        );
    }

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

    let mut child = match builder
        .current_dir(&dir)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
    {
        Ok(c) => c,
        Err(e) => return mk("failed", &format!("spawn docker: {e}"), None),
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
        Err(e) => return mk("failed", &format!("wait: {e}"), None),
    };

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
pub(crate) fn run_container_chunk(_spec: &ChunkSpec, _image: &str, _gpu: bool) -> ChunkResult {
    ChunkResult {
        chunk_id: _spec.chunk_id.clone(),
        outcome: "rejected".into(),
        detail: "conteneurs supportes uniquement sous Windows avec WSL2".into(),
        duration_ms: 0,
        output_sha256: None,
        output_path: None,
    }
}

#[cfg(test)]
mod tests {
    use super::image_valide;

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
}
