//! LAI-23 - Stats temps reel du noeud fournisseur.
//! Gains REELS depuis le wallet de la gateway (LAI-34), charge GPU,
//! temperature, statut minage.

use serde::Serialize;
use std::sync::{Mutex, OnceLock};
use std::time::{Duration, Instant};
use nvml_wrapper::enum_wrappers::device::TemperatureSensor;

#[derive(Serialize)]
pub struct LiveStats {
    pub gpu_load: u32,
    pub gpu_temp_c: u32,
    pub cpu_load: f32,
    pub should_mine: bool,
    pub earnings_eur: f64,
    pub uptime_secs: u64,
}

// Cache du solde wallet : requete a la gateway au plus toutes les 10 s
// (l'UI raffraichit toutes les 2 s), dernier solde connu sinon.
// (f64, Option<Instant>) : solde connu, instant du dernier essai.
fn wallet_cache() -> &'static Mutex<(f64, Option<Instant>)> {
    static CELL: OnceLock<Mutex<(f64, Option<Instant>)>> = OnceLock::new();
    CELL.get_or_init(|| Mutex::new((0.0, None)))
}

fn boot() -> &'static Instant {
    static CELL: OnceLock<Instant> = OnceLock::new();
    CELL.get_or_init(Instant::now)
}

/// Solde reel du vendeur, depuis GET /wallet?node_id=X de la gateway.
/// Retourne le dernier solde connu si la gateway ne repond pas.
fn wallet_earnings() -> f64 {
    let st = crate::node::node_status();
    let now = Instant::now();
    let mut cell = wallet_cache().lock().unwrap();
    let due = cell.1.map(|t| t.elapsed() >= Duration::from_secs(10)).unwrap_or(true);
    if st.running && due {
        cell.1 = Some(now); // marque l'essai, reussi ou non
        if let Some(r) = crate::http::get(&st.gateway, &format!("/wallet?node_id={}", st.node_id)) {
            if r.status == 200 {
                if let Ok(v) = serde_json::from_str::<serde_json::Value>(&r.body) {
                    if let Some(b) = v["balance"].as_f64() {
                        cell.0 = b;
                    }
                }
            }
        }
    }
    cell.0
}

#[tauri::command]
pub fn get_live_stats() -> LiveStats {
    let idle = crate::idle::get_idle_status();

    let (gpu_load, gpu_temp) = match nvml_wrapper::Nvml::init() {
        Ok(nvml) => match nvml.device_by_index(0) {
            Ok(dev) => (
                dev.utilization_rates().map(|u| u.gpu).unwrap_or(0),
                dev.temperature(TemperatureSensor::Gpu).unwrap_or(0),
            ),
            Err(_) => (0, 0),
        },
        Err(_) => (0, 0),
    };

    LiveStats {
        gpu_load,
        gpu_temp_c: gpu_temp,
        cpu_load: idle.cpu_load,
        should_mine: idle.should_mine,
        earnings_eur: wallet_earnings(),
        uptime_secs: boot().elapsed().as_secs(),
    }
}
