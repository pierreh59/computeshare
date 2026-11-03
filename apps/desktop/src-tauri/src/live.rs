//! LAI-23 - Stats temps reel du noeud fournisseur.
//! Gains estimes, charge GPU, temperature, statut minage.

use serde::Serialize;
use std::sync::{Mutex, OnceLock};
use std::time::Instant;
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

// Taux indicatif en EUR/s a pleine charge GPU.
// Sera remplace par le moteur de tarification (LAI-51) avant la beta.
const RATE_EUR_PER_SEC: f64 = 0.00003;

fn earnings_cell() -> &'static Mutex<f64> {
    static CELL: OnceLock<Mutex<f64>> = OnceLock::new();
    CELL.get_or_init(|| Mutex::new(0.0))
}

fn last_call() -> &'static Mutex<Option<Instant>> {
    static CELL: OnceLock<Mutex<Option<Instant>>> = OnceLock::new();
    CELL.get_or_init(|| Mutex::new(None))
}

fn boot() -> &'static Instant {
    static CELL: OnceLock<Instant> = OnceLock::new();
    CELL.get_or_init(Instant::now)
}

#[tauri::command]
pub fn get_live_stats() -> LiveStats {
    let idle = crate::idle::get_idle_status();

    let (gpu_load, gpu_temp) =
        match nvml_wrapper::Nvml::init().ok().and_then(|n| n.device_by_index(0).ok()) {
            Some(dev) => (
                dev.utilization_rates().map(|u| u.gpu).unwrap_or(0),
                dev.temperature(TemperatureSensor::Gpu).unwrap_or(0),
            ),
            None => (0, 0),
        };

    // Temps ecoule depuis l'appel precedent (donc depuis le dernier refresh UI).
    let now = Instant::now();
    let dt = {
        let mut last = last_call().lock().unwrap();
        let dt = last.map(|t| t.elapsed().as_secs_f64()).unwrap_or(0.0);
        *last = Some(now);
        dt.min(10.0) // garde-fou si l'UI a ete gele longtemps
    };

    // Accumulation des gains : seulement pendant le minage.
    let mut earn = earnings_cell().lock().unwrap();
    if idle.should_mine {
        *earn += (gpu_load as f64 / 100.0) * dt * RATE_EUR_PER_SEC;
    }

    LiveStats {
        gpu_load,
        gpu_temp_c: gpu_temp,
        cpu_load: idle.cpu_load,
        should_mine: idle.should_mine,
        earnings_eur: *earn,
        uptime_secs: boot().elapsed().as_secs(),
    }
}
