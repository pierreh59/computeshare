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

#[derive(Serialize, Clone)]
pub struct NodeStatus {
    pub running: bool,
    pub gateway: String,
    pub node_id: String,
    pub chunks_done: u32,
    pub chunks_failed: u32,
    pub last_error: String,
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
        })
    })
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
    let mut st = status_cell().lock().unwrap();
    if st.running {
        return Ok(()); // deja demarre, une seule boucle par noeud
    }
    st.running = true;
    st.gateway = gw.clone();
    st.node_id = id.clone();
    drop(st);
    std::thread::spawn(move || node_loop(gw, id));
    Ok(())
}

#[tauri::command]
pub fn node_status() -> NodeStatus {
    status_cell().lock().unwrap().clone()
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
    let register_body = serde_json::json!({
        "node_id": node_id,
        "gpu_model": hw.as_ref().map(|h| h.gpu_model.clone()).unwrap_or_default(),
        "gpu_category": hw.as_ref().map(|h| h.gpu_category.clone()).unwrap_or_default(),
        "machine_fingerprint": machine_fingerprint,
        "wsl2_ready": wsl2_ready(),
    })
    .to_string();

    loop {
        // 1. Register : idempotent, met a jour LastSeen = heartbeat.
        match crate::http::post_json(&gateway, "/register", &register_body) {
            Some(r) if r.status == 200 => {
                // Un register reussi efface l'erreur precedente : sans
                // cela, "gateway injoignable" restait affiche meme apres
                // une reconnexion reussie.
                status_cell().lock().unwrap().last_error.clear();
            }
            Some(r) if r.status == 401 || r.status == 403 => set_error(
                "jeton de noeud refuse : reconnectez-vous avec votre compte (bouton Se deconnecter puis connexion)",
            ),
            Some(r) => set_error(&format!("register: HTTP {}", r.status)),
            None => set_error("gateway injoignable"),
        }

        // 2. Claim un chunk seulement si le noeud est disponible :
        // bouton On/Off + charge CPU/GPU sous les seuils (sprint 21).
        crate::availability::ensure_sampler();
        if crate::availability::available() {
            if let Some(r) = crate::http::get(&gateway, &format!("/chunks?node_id={}", node_id)) {
                if let Ok(v) = serde_json::from_str::<serde_json::Value>(&r.body) {
                    if v.is_object() {
                        run_and_report(&gateway, &v);
                    }
                }
            }
        }

        std::thread::sleep(Duration::from_secs(5));
    }
}

fn run_and_report(gateway: &str, chunk: &serde_json::Value) {
    crate::availability::set_working(true);
    let res = run_chunk_gateway(gateway, chunk);
    crate::availability::set_working(false);
    let payload = serde_json::json!({
        "chunk_id": res.chunk_id,
        "outcome": res.outcome,
        "sha256": res.output_sha256.unwrap_or_default(),
        "duration_secs": (res.duration_ms / 1000) as u64,
    })
    .to_string();
    let _ = crate::http::post_json(gateway, "/chunk_result", &payload);

    let mut st = status_cell().lock().unwrap();
    if res.outcome == "ok" {
        st.chunks_done += 1;
        st.last_error.clear();
    } else {
        st.chunks_failed += 1;
        st.last_error = format!("{}: {}", res.outcome, res.detail);
    }
}

/// Sprint 21 : le chunk est extrait dans une fonction separee pour que
/// le marquage "working" (graphe d'activite) encadre toute l'execution,
/// sandbox ou conteneur.
fn run_chunk_gateway(gateway: &str, chunk: &serde_json::Value) -> crate::sandbox::ChunkResult {
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
            output_sha256: None,
        };
    }
    // Sprint 19 (v2) : un chunk conteneur est route vers docker,
    // les autres restent dans le sandbox classique.
    let runtime = chunk["runtime"].as_str().unwrap_or_default().to_string();
    let image = chunk["image"].as_str().unwrap_or_default().to_string();
    if runtime == "container" {
        crate::container::run_container_chunk(&spec, &image)
    } else {
        crate::sandbox::run_chunk(spec)
    }
}

fn set_error(msg: &str) {
    status_cell().lock().unwrap().last_error = msg.to_string();
}
