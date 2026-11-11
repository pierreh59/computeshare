//! LAI-53 - Client noeud : boucle register / poll chunks / report.
//! Register periodique (idempotent, sert de heartbeat), claim d'un chunk
//! seulement si la machine est idle, execution dans le sandbox, remontee
//! du resultat + checksum SHA-256.

use crate::sandbox::ChunkSpec;
use serde::Serialize;
use std::sync::{Mutex, OnceLock};

/// Sprint 18 (v2) : detection WSL2 pour les jobs conteneur.
/// Le noeud annonce sa capacite au register (wsl2_ready). Detection
/// par `wsl --status` : si la commande reussit, WSL2 est installe.
#[cfg(windows)]
fn wsl2_ready() -> bool {
    use std::os::windows::process::CommandExt;
    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    std::process::Command::new("wsl")
        .arg("--status")
        .creation_flags(CREATE_NO_WINDOW)
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

#[cfg(not(windows))]
fn wsl2_ready() -> bool {
    false
}

/// Sprint 25 : detection GPU pour les jobs conteneur avec "gpu": true.
/// Probe `docker run --rm --gpus all alpine true` : reussie = le daemon
/// peut passer le GPU au conteneur. Sprint 33 : le resultat est
/// RE-SONDE au moins toutes les 2 minutes (avant, il etait memorise
/// pour toute la session : un Docker Desktop pret APRES le lancement
/// de l'app etait ignore pour toujours).
#[cfg(windows)]
fn gpu_ready() -> bool {
    use std::os::windows::process::CommandExt;
    // Sprint 33 : le resultat est re-sonde au moins toutes les
    // 2 minutes. Avant, il etait memorise pour toute la session :
    // si Docker Desktop devenait pret APRES le lancement de l'app
    // (cas reel vu le 8 nov. : noeud gpu_ready false malgre un RTX
    // 5070), le noeud ne declarait JAMAIS le GPU. Un resultat frais
    // est redeclare au register suivant (boucle de 5 s).
    static STATE: std::sync::OnceLock<Mutex<Option<(std::time::Instant, bool)>>> =
        std::sync::OnceLock::new();
    let cell = STATE.get_or_init(|| Mutex::new(None));
    if let Some((sonde_a, pret)) = *cell.lock().unwrap() {
        if sonde_a.elapsed().as_secs() < 120 {
            return pret;
        }
    }
    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    let ok = std::process::Command::new("docker")
        .args(["run", "--rm", "--gpus", "all", "alpine", "true"])
        .creation_flags(CREATE_NO_WINDOW)
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false);
    *cell.lock().unwrap() = Some((std::time::Instant::now(), ok));
    ok
}

#[cfg(not(windows))]
fn gpu_ready() -> bool {
    false
}

#[derive(Serialize, Clone)]
pub struct NodeStatus {
    pub running: bool,
    pub gateway: String,
    pub node_id: String,
    pub chunks_done: u32,
    pub chunks_failed: u32,
    pub last_error: String,
    /// Sprint 33 : action en cours, affichee en direct dans le
    /// tableau de bord (telechargement, calcul, envoi du resultat).
    pub current_action: String,
}

fn status_cell() -> &'static Mutex<NodeStatus> {
    static CELL: OnceLock<Mutex<NodeStatus>> = OnceLock::new();
    CELL.get_or_init(|| {
        Mutex::new(NodeStatus {
            running: false,
            gateway: String::new(),
            node_id: String::new(),
            chunks_done: 0,
            chunks_failed: 0,
            last_error: String::new(),
            current_action: String::new(),
        })
    })
}

/// Sprint 36 : verrou tolerant au poison. Si un fil panique en tenant
/// le verrou, std::sync l'"empoisonne" et les autres fils (register,
/// UI, poll) ne doivent pas mourir en cascade a leur tour a cause d'un
/// lock().unwrap(). On recupere le contenu garde meme empoisonne : le
/// NodeStatus est de simples champs, jamais structurellement incoherent.
fn lock_status() -> std::sync::MutexGuard<'static, NodeStatus> {
    status_cell().lock().unwrap_or_else(|p| p.into_inner())
}

/// Sprint 36 : la tache en cours, vue par le chien de garde externe.
/// deadline = instant au-dela duquel la tache est consideree bloquee
/// (recalculee phase par phase : assets, calcul, envoi).
struct WorkInfo {
    chunk_id: String,
    deadline: std::time::Instant,
}

fn working_cell() -> &'static Mutex<Option<WorkInfo>> {
    static CELL: OnceLock<Mutex<Option<WorkInfo>>> = OnceLock::new();
    CELL.get_or_init(|| Mutex::new(None))
}

/// Sprint 36 : debut de tache, echeance initiale couvrant la phase
/// d'assets puis celle de calcul.
fn working_begin(chunk_id: &str, echeance_secs: u64) {
    *working_cell().lock() = Some(WorkInfo {
        chunk_id: chunk_id.to_string(),
        deadline: std::time::Instant::now() + Duration::from_secs(echeance_secs),
    });
}

/// Sprint 36 : nouvelle echeance (changement de phase).
fn working_echeance(secs: u64) {
    if let Some(w) = working_cell().lock().as_mut() {
        w.deadline = std::time::Instant::now() + Duration::from_secs(secs);
    }
}

/// Sprint 36 : fin de tache (normale OU apres panic interceptee).
fn working_end() {
    *working_cell().lock() = None;
}

/// Sprint 36 : message lisible d'une charge de panic recuperee par
/// catch_unwind (String ou &str en pratique).
fn panic_msg(p: &(dyn std::any::Any + Send)) -> String {
    if let Some(s) = p.downcast_ref::<&str>() {
        (*s).to_string()
    } else if let Some(s) = p.downcast_ref::<String>() {
        s.clone()
    } else {
        "cause inconnue".to_string()
    }
}

/// Sprint 36 : chien de garde EXTERNE du fil de poll. Le fil ne peut
/// pas etre tue de l'exterieur en Rust : si une tache depasse son
/// echeance (le chien de garde INTERNE de container.rs aurait deja du
/// la couper au timeout du chunk), on detruit le conteneur docker au
/// mieux pour debloquer le fil bloque dans docker run, on journalise
/// et on affiche l'erreur - plus jamais de noeud "en ligne mais qui
/// ne traite rien" sans explication (symptome du 11 nov. 2026).
fn watchdog_loop() {
    loop {
        std::thread::sleep(Duration::from_secs(30));
        let bloquee: Option<String> = {
            let w = working_cell().lock();
            match w.as_ref() {
                Some(info) if std::time::Instant::now() > info.deadline => {
                    Some(info.chunk_id.clone())
                }
                _ => None,
            }
        };
        let Some(chunk_id) = bloquee else { continue };
        trace(&format!(
            "{} WATCHDOG : tache bloquee au-dela de son delai, destruction du conteneur au mieux",
            chunk_id
        ));
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            use std::process::{Command, Stdio};
            const CREATE_NO_WINDOW: u32 = 0x08000000;
            let cname = format!("cs-{}", crate::sandbox::sanitize_id(&chunk_id));
            let _ = Command::new("docker")
                .args(["rm", "-f", &cname])
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .creation_flags(CREATE_NO_WINDOW)
                .status();
        }
        set_error(&format!(
            "watchdog : la tache {} semblait bloquee, conteneur interrompu (le noeud continue)",
            chunk_id
        ));
        // Re verifier dans 10 min si le fil reste bloque, sans toucher
        // a l'etat working (le fil de poll en reste maitre).
        let w = working_cell().lock();
        if let Some(info) = w.as_mut() {
            info.deadline = std::time::Instant::now() + Duration::from_secs(600);
        }
    }
}

/// Sprint 16 : plus AUCUN parametre - tout vient de la configuration
/// persistante (gateway + node_id + jeton recupere a la connexion).
#[tauri::command]
pub fn start_node() -> Result<(), String> {
    let cfg = crate::config::load();
    if cfg.node_token.is_empty() {
        return Err("Connectez-vous d'abord avec votre compte fournisseur.".into());
    }
    let (gw, id) = (cfg.gateway.clone(), cfg.node_id.clone());
    if gw.is_empty() || id.is_empty() {
        return Err("configuration incomplete".into());
    }
    crate::http::set_node_token(&cfg.node_token);
    let mut st = lock_status();
    if st.running {
        return Ok(()); // deja demarre, une seule boucle par noeud
    }
    st.running = true;
    st.gateway = gw.clone();
    st.node_id = id.clone();
    drop(st);
    std::thread::spawn(move || node_loop(gw, id));
    // Sprint 36 : un seul chien de garde par demarrage du noeud (le
    // garde st.running empeche un double lancement).
    std::thread::spawn(watchdog_loop);
    Ok(())
}

#[tauri::command]
pub fn node_status() -> NodeStatus {
    lock_status().clone()
}

use std::time::Duration;

fn node_loop(gateway: String, node_id: String) {
    let hw = crate::hw::detect_hardware().ok();
    // LAI-44 : empreinte machine pour la detection anti-Sybil
    // (nom de machine + modele CPU, sans donnee personnelle).
    let machine_fingerprint = format!(
        "{}|{}",
        std::env::var("COMPUTERNAME").unwrap_or_default(),
        hw.as_ref().map(|h| h.cpu_model.clone()).unwrap_or_default()
    );
    let gpu_model = hw.as_ref().map(|h| h.gpu_model.clone()).unwrap_or_default();
    let gpu_category = hw.as_ref().map(|h| h.gpu_category.clone()).unwrap_or_default();

    // Sprint 22 : le register (heartbeat) tourne dans SON PROPRE thread.
    // Avant, la boucle etait bloquee pendant l'execution d'un chunk : un
    // job conteneur de 5 min sans heartbeat faisait marquer le noeud
    // "hors ligne" par le serveur, avec risque de re-attribution du job
    // a un autre noeud (vu en vrai le 7 nov., job-gros-1).
    {
        let gw = gateway.clone();
        let nid = node_id.clone();
        std::thread::spawn(move || {
            let wsl2 = wsl2_ready(); // detection lente : une seule fois
            loop {
            // Register : idempotent, met a jour LastSeen = heartbeat.
            // Sprint 25 : "gpu_ready" re-evalue a chaque register (la
            // probe peut reussir apres le lancement de Docker Desktop).
            let body = serde_json::json!({
                "node_id": nid,
                "gpu_model": gpu_model,
                "gpu_category": gpu_category,
                "machine_fingerprint": machine_fingerprint,
                "wsl2_ready": wsl2,
                "gpu_ready": gpu_ready(),
            })
            .to_string();
            match crate::http::post_json(&gw, "/register", &body) {
                Some(r) if r.status == 200 => {
                    // Un register reussi efface l'erreur precedente : sans
                    // cela, "gateway injoignable" restait affiche meme apres
                    // une reconnexion reussie.
                    lock_status().last_error.clear();
                }
                Some(r) if r.status == 401 || r.status == 403 => set_error(
                    "jeton de noeud refuse : reconnectez-vous avec votre compte (bouton Se deconnecter puis connexion)",
                ),
                Some(r) => set_error(&format!("register: HTTP {}", r.status)),
                None => set_error("gateway injoignable"),
            }
            std::thread::sleep(Duration::from_secs(5));
            }
        });
    }

    loop {
        // Claim un chunk seulement si le noeud est disponible :
        // bouton On/Off + charge CPU/GPU sous les seuils (sprint 21).
        crate::availability::ensure_sampler();
        if crate::availability::available() {
            if let Some(r) = crate::http::get(&gateway, &format!("/chunks?node_id={}", node_id)) {
                if let Ok(v) = serde_json::from_str::<serde_json::Value>(&r.body) {
                    if v.is_object() {
                        // Sprint 36 : une panic pendant le traitement d'un
                        // chunk tuait AVANT le fil de poll en silence (app
                        // Windows sans console = rien n'est affiche) : le
                        // noeud restait "en ligne" (register independant)
                        // mais ne traitait plus jamais rien (vu en vrai le
                        // 11 nov. 2026). Le fil survit desormais a toute
                        // panic, avec nettoyage complet de l'etat.
                        let res = std::panic::catch_unwind(
                            std::panic::AssertUnwindSafe(|| {
                                run_and_report(&gateway, &node_id, &v)
                            }),
                        );
                        if let Err(pan) = res {
                            crate::availability::set_working(false);
                            working_end();
                            clear_action();
                            let cause = panic_msg(&pan);
                            set_error(&format!(
                                "bug intercepte pendant une tache (le noeud continue) : {}",
                                cause
                            ));
                            trace(&format!(
                                "PANIQUE INTERCEPTEE, fil de poll survit : {}",
                                cause
                            ));
                        }
                    }
                }
            }
        }

        std::thread::sleep(Duration::from_secs(5));
    }
}

fn run_and_report(gateway: &str, node_id: &str, chunk: &serde_json::Value) {
    crate::availability::set_working(true);
    set_action("Traitement d'une tache...");
    // Sprint 36 : echeance annoncee au chien de garde externe. Phase
    // assets : 600 s de timeout http par fichier + marge ; phase
    // calcul : le timeout du chunk (300 s si absent, cf container.rs).
    // Le chien de garde ne declenche QUE si ce delai global est
    // depasse, jamais pendant un travail legitime long.
    let ts = chunk["timeout_secs"].as_u64().unwrap_or(0);
    let echeance_calcul = if ts == 0 { 300 } else { ts.min(3600) };
    working_begin(
        chunk["id"].as_str().unwrap_or_default(),
        600 + 120 + echeance_calcul,
    );
    let res = run_chunk_gateway(gateway, node_id, chunk);
    crate::availability::set_working(false);
    let payload = serde_json::json!({
        "chunk_id": res.chunk_id,
        // Sprint 22 : le rapport precise QUI a execute : le paiement
        // doit aller au noeud qui a reellement travaille, meme si le
        // serveur a re-attribue le chunk entre-temps.
        "node_id": node_id,
        "outcome": res.outcome,
        "sha256": res.output_sha256.unwrap_or_default(),
        "duration_secs": (res.duration_ms / 1000) as u64,
        // Sprint 30 : decomposition du temps (ms) pour la facturation :
        // pull facture au client a tarif reduit, calcul paye au
        // vendeur et facture a plein tarif.
        "pull_ms": res.pull_ms,
        "compute_ms": res.compute_ms,
        // Sprint 26b : detail d erreur transmis au serveur (diagnostic).
        "detail": res.detail,
    })
    .to_string();
    let _ = crate::http::post_json(gateway, "/chunk_result", &payload);

    // Sprint 26 : rapatriement du fichier de resultat. Le client doit
    // recevoir le fichier lui-meme, pas seulement son empreinte.
    // Sprint 26c : chaque etape est tracee (fichier local %TEMP% +
    // compte-rendu au serveur via /chunk_upload) car ce parcours a
    // echoue silencieusement en vrai et l'UI seule ne suffit pas.
    let mut note = String::new();
    if res.outcome == "ok" {
        match &res.output_path {
            None => {
                note = "ok SANS chemin de fichier (bug noeud)".to_string();
                trace(&format!("{} {}", res.chunk_id, note));
            }
            Some(p) => {
                let taille = std::fs::metadata(p).map(|m| m.len()).unwrap_or(0);
                trace(&format!("{} fichier {} octets a pousser", res.chunk_id, taille));
                set_action(&format!(
                    "Envoi du resultat ({:.1} Mo)...",
                    taille as f64 / 1048576.0
                ));
                // Sprint 36 : l'envoi a son propre timeout (3600 s dans
                // http.rs) : nouvelle echeance pour le chien de garde,
                // sinon il interromprait un upload legitime d'un gros fichier.
                working_echeance(3600 + 60);
                if taille > 5 * 1024 * 1024 * 1024 {
                    note = "fichier trop volumineux (5 Go)".to_string();
                    set_error("fichier resultat trop volumineux (5 Go max), non rapatrie");
                } else {
                    match std::fs::File::open(p) {
                        Ok(f) => match crate::http::post_file(gateway, "/results", &res.chunk_id, f) {
                            Some(r) if r.status == 200 => {
                                note = format!("fichier pousse, HTTP 200");
                                lock_status().last_error.clear();
                            }
                            Some(r) => {
                                note = format!("refus HTTP {} corps: {}", r.status, r.body);
                                set_error(&format!("upload resultat: HTTP {}", r.status));
                            }
                            None => {
                                note = "gateway injoignable".to_string();
                                set_error("upload resultat: gateway injoignable");
                            }
                        },
                        Err(e) => {
                            note = format!("ouverture fichier impossible: {e}");
                            set_error("fichier resultat introuvable apres execution");
                        }
                    }
                }
                trace(&format!("{} upload: {}", res.chunk_id, note));
            }
        }
    }
    // Compte-rendu du rapatriement au serveur (journal admin).
    let _ = crate::http::post_json(
        gateway,
        "/chunk_upload",
        &serde_json::json!({"chunk_id": res.chunk_id, "node_id": node_id, "note": note}).to_string(),
    );

    // Sprint 27c : effacement des fichiers locaux du chunk (sandbox
    // de travail + assets d'entree). Regle : on efface DES QUE le
    // resultat est remonte vers le serveur (upload HTTP 200) ou que
    // le chunk a echoue (aucun fichier a proteger). On ne CONSERVE
    // les fichiers QUE si l'upload du resultat a ete refuse ou la
    // gateway injoignable : diagnostic et recuperation manuelle
    // restent possibles, avec une trace explicite.
    let upload_refuse = note.starts_with("refus HTTP") || note == "gateway injoignable";
    if !upload_refuse {
        crate::sandbox::cleanup_chunk_dirs(&res.chunk_id);
        trace(&format!(
            "{} fichiers locaux supprimes (sandbox + assets)",
            res.chunk_id
        ));
    } else {
        trace(&format!(
            "{} fichiers locaux CONSERVES : upload du resultat en echec ({})",
            res.chunk_id, note
        ));
    }

    clear_action();
    let mut st = lock_status();
    if res.outcome == "ok" {
        st.chunks_done += 1;
        // Sprint 26b : on n'efface PAS last_error ici : le message
        // d'erreur d'upload (set_error ci-dessus) serait masque.
        // Il est efface par un upload reussi (ligne ci-dessus).
    } else {
        st.chunks_failed += 1;
        st.last_error = format!("{}: {}", res.outcome, res.detail);
    }
    // Sprint 36 : tache finie, le chien de garde externe n'a plus rien
    // a surveiller (appele aussi par le nettoyage de panic du poll).
    working_end();
}

/// Sprint 27 : un asset d'entree telecharge et verifie, pret a etre
/// copie dans le dossier monte sur /work du conteneur.
pub struct FetchedAsset {
    pub filename: String,
    pub local_path: String,
}

/// Sprint 27 : telecharge les assets d'entree d'un chunk (manifeste
/// embarque dans le chunk livre par /chunks), verifie le sha256 de
/// chaque fichier, les pose dans %TEMP%/computeshare-assets/<chunk>/.
/// Chaque etape est tracee (lecon du sprint 26 : un parcours qui
/// echoue silencieusement coute des heures de debug).
fn fetch_assets(
    gateway: &str,
    node_id: &str,
    chunk: &serde_json::Value,
) -> Result<Vec<FetchedAsset>, String> {
    let chunk_id = chunk["id"].as_str().unwrap_or_default().to_string();
    let entries = match chunk["assets"].as_array() {
        Some(a) if !a.is_empty() => a.clone(),
        _ => return Ok(Vec::new()),
    };
    let dir = std::env::temp_dir()
        .join("computeshare-assets")
        .join(crate::sandbox::sanitize_id(&chunk_id));
    let _ = std::fs::remove_dir_all(&dir);
    std::fs::create_dir_all(&dir).map_err(|e| format!("creation du dossier assets : {e}"))?;
    let mut out = Vec::new();
    for e in entries {
        let asset_id = e["asset_id"].as_str().unwrap_or_default().to_string();
        let filename = crate::container::sanitize_asset_filename(
            e["filename"].as_str().unwrap_or_default(),
        );
        let sha_attendu = e["sha256"].as_str().unwrap_or_default().to_string();
        if asset_id.is_empty() || filename.is_empty() {
            return Err("manifeste d'asset invalide".into());
        }
        let path = dir.join(&filename);
        trace(&format!(
            "{} telechargement de l'asset {} ({})",
            chunk_id, asset_id, filename
        ));
        let url_path = format!(
            "/v1/assets/{}/node-download?node_id={}&chunk_id={}",
            asset_id, node_id, chunk_id
        );
        // Sprint 33 : progression du telechargement affichee en direct
        // dans le tableau de bord (pourcentage + tailles recues/totales).
        let progress = |recu: u64, total: u64| {
            set_action(&fmt_progress(
                &format!("Telechargement de {}", filename),
                recu,
                total,
            ));
        };
        match crate::http::get_file(
            gateway,
            &url_path,
            &path,
            Some(&progress as &dyn Fn(u64, u64)),
        ) {
            Some(r) if r.status == 200 => {}
            Some(r) => return Err(format!("asset {} refuse : HTTP {}", asset_id, r.status)),
            None => return Err(format!("asset {} : gateway injoignable", asset_id)),
        }
        // Integrite : un fichier corrompu ne doit JAMAIS entrer dans
        // un conteneur.
        match crate::sandbox::hash_file(&path) {
            Some(sha) if sha == sha_attendu => {}
            Some(_) => {
                return Err(format!(
                    "asset {} corrompu (sha256 different de l'annonce)",
                    asset_id
                ))
            }
            None => {
                return Err(format!(
                    "asset {} introuvable apres telechargement",
                    asset_id
                ))
            }
        }
        trace(&format!("{} asset {} sha OK", chunk_id, asset_id));
        out.push(FetchedAsset {
            filename,
            local_path: path.to_string_lossy().into_owned(),
        });
    }
    Ok(out)
}

/// Sprint 21 : le chunk est extrait dans une fonction separee pour que
/// le marquage "working" (graphe d'activite) encadre toute l'execution,
/// sandbox ou conteneur.
fn run_chunk_gateway(
    gateway: &str,
    node_id: &str,
    chunk: &serde_json::Value,
) -> crate::sandbox::ChunkResult {
    let spec = ChunkSpec {
        chunk_id: chunk["id"].as_str().unwrap_or_default().to_string(),
        command: chunk["command"].as_str().unwrap_or_default().to_string(),
        args: chunk["args"]
            .as_array()
            .map(|a| a.iter().filter_map(|x| x.as_str().map(String::from)).collect())
            .unwrap_or_default(),
        expected_output: chunk["expected_output"].as_str().unwrap_or_default().to_string(),
        timeout_secs: chunk["timeout_secs"].as_u64().unwrap_or(0),
    };
    if spec.chunk_id.is_empty() {
        return crate::sandbox::ChunkResult {
            chunk_id: String::new(),
            outcome: "rejected".into(),
            detail: "chunk sans identifiant".into(),
            duration_ms: 0,
            pull_ms: 0,
            compute_ms: 0,
            output_sha256: None,
            output_path: None,
        };
    }
    // Sprint 19 (v2) : un chunk conteneur est route vers docker,
    // les autres restent dans le sandbox classique.
    let runtime = chunk["runtime"].as_str().unwrap_or_default().to_string();
    let image = chunk["image"].as_str().unwrap_or_default().to_string();
    if runtime == "container" {
        // Sprint 25 : le job peut demander le GPU ("gpu": true) - le
        // conteneur est alors lance avec --gpus all (toujours durci).
        let gpu = chunk["gpu"].as_bool().unwrap_or(false);
        // Sprint 27 : les assets d'entree sont telecharges et
        // verifies AVANT le conteneur ; en cas d'echec le chunk
        // echoue proprement (detail visible dans le journal admin).
        let assets = match fetch_assets(gateway, node_id, chunk) {
            Ok(a) => a,
            Err(e) => {
                trace(&format!("{} assets d'entree en echec : {}", spec.chunk_id, e));
                return crate::sandbox::ChunkResult {
                    chunk_id: spec.chunk_id,
                    outcome: "failed".into(),
                    detail: format!("assets d'entree : {e}"),
                    duration_ms: 0,
                    pull_ms: 0,
                    compute_ms: 0,
                    output_sha256: None,
                    output_path: None,
                };
            }
        };
        // Sprint 36 : assets poses, place au calcul : l'echeance du
        // chien de garde se resserre sur le seul timeout du chunk
        // (300 s par defaut, cf DEFAULT_TIMEOUT_SECS dans container.rs).
        let echeance_calcul = if spec.timeout_secs == 0 { 300 } else { spec.timeout_secs.min(3600) };
        working_echeance(120 + echeance_calcul);
        crate::container::run_container_chunk(&spec, &image, gpu, &assets)
    } else {
        crate::sandbox::run_chunk(spec)
    }
}

fn set_error(msg: &str) {
    lock_status().last_error = msg.to_string();
}

/// Sprint 33 : action en cours, affichee en direct dans le tableau
/// de bord (telechargement d'image ou de fichier, calcul, envoi).
pub(crate) fn set_action(msg: &str) {
    lock_status().current_action = msg.to_string();
}

/// Sprint 33 : plus d'action en cours (tache terminee).
pub(crate) fn clear_action() {
    lock_status().current_action.clear();
}

/// Sprint 33 : progression lisible : "<label> : 42 % (12,3 Mo / 29,9 Mo)".
/// Taille totale inconnue : "<label> : 12,3 Mo".
pub(crate) fn fmt_progress(label: &str, done: u64, total: u64) -> String {
    let mo = |n: u64| (n as f64 / 1048576.0 * 10.0).round() / 10.0;
    if total > 0 {
        let pct = ((done as f64 / total as f64) * 100.0).round() as u64;
        format!("{} : {} % ({:.1} Mo / {:.1} Mo)", label, pct, mo(done), mo(total))
    } else {
        format!("{} : {:.1} Mo", label, mo(done))
    }
}

/// Sprint 26c : trace locale du noeud, append dans %TEMP%.
/// Diagnostic du rapatriement (chaque etape, avec code HTTP).
/// Sprint 28 : pub(crate) - container.rs trace aussi le parcours
/// de relance silencieuse de Docker Desktop.
pub(crate) fn trace(msg: &str) {
    use std::io::Write;
    let p = std::env::temp_dir().join("computeshare-node.log");
    if let Ok(mut f) = std::fs::OpenOptions::new().create(true).append(true).open(&p) {
        let _ = writeln!(f, "{} {}", chrono_like_now(), msg);
    }
}

/// Horodatage simple sans dependance (secondes epoch).
fn chrono_like_now() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs().to_string())
        .unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Sprint 36 : le verrou tolere un mutex empoisonne (panic d'un fil
    /// pendant qu'il tenait le verrou) sans paniquer a son tour.
    #[test]
    fn lock_status_survit_au_poison() {
        let r = std::panic::catch_unwind(|| {
            let _g = status_cell().lock().unwrap();
            panic!("empoisonnement volontaire");
        });
        assert!(r.is_err());
        assert!(status_cell().is_poisoned());
        // Ne doit PAS paniquer, meme empoisonne :
        lock_status().last_error.clear();
    }

    /// Sprint 36 : la cellule du chien de garde suit le cycle complet
    /// begin -> echeance (changement de phase) -> end.
    #[test]
    fn working_cell_cycle_complet() {
        working_begin("chunk-test", 60);
        {
            let w = working_cell().lock();
            let info = w.as_ref().expect("workinfo attendu");
            assert_eq!(info.chunk_id, "chunk-test");
            assert!(info.deadline > std::time::Instant::now());
        }
        working_echeance(600);
        {
            let w = working_cell().lock();
            let info = w.as_ref().expect("workinfo attendu");
            assert!(
                info.deadline > std::time::Instant::now() + Duration::from_secs(500),
                "l'echeance doit avoir ete repoussee a +600 s"
            );
        }
        working_end();
        assert!(working_cell().lock().is_none());
    }

    /// Sprint 36 : message lisible d'une charge de panic recuperee par
    /// catch_unwind (String, &str, ou autre chose).
    #[test]
    fn panic_msg_lisible() {
        assert_eq!(panic_msg(&"boum &str"), "boum &str");
        assert_eq!(panic_msg(&String::from("boum String")), "boum String");
        assert_eq!(panic_msg(&7u32), "cause inconnue");
    }
}
