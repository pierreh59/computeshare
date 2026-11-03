//! LAI-21 - Detection d'idle : le minage ne demarre que si la machine
//! est inutilisee, et se coupe automatiquement si un jeu/app lourde demarre.

use serde::Serialize;
use std::time::Instant;
use std::sync::Mutex;
use std::sync::OnceLock;
use device_query::{DeviceQuery, DeviceState, MousePosition};
use sysinfo::System;

#[derive(Serialize)]
pub struct IdleStatus {
    pub should_mine: bool,
    pub cpu_load: f32,
    pub gpu_idle: bool,
    pub user_idle_secs: u64,
    pub game_detected: bool,
}

const USER_IDLE_THRESHOLD_SECS: u64 = 300;   // 5 min sans souris/clavier
const CPU_BUSY_THRESHOLD: f32 = 60.0;       // charge CPU au-dela = machine occupee
const GPU_BUSY_THRESHOLD: u32 = 50;        // charge GPU au-dela = jeu ou rendu

fn last_input() -> &'static Mutex<Option<(MousePosition, Instant)>> {
    static CELL: OnceLock<Mutex<Option<(MousePosition, Instant)>>> = OnceLock::new();
    CELL.get_or_init(|| Mutex::new(None))
}

#[tauri::command]
pub fn get_idle_status() -> IdleStatus {
    let state = DeviceState::new();
    let mouse = state.get_mouse().coords;
    let keys = state.get_keys();

    // Idle utilisateur : souris immobile + aucune touche depuis N minutes
    let idle_secs = {
        let mut cell = last_input().lock().unwrap();
        match cell.as_ref() {
            Some((prev, t)) if *prev == mouse && keys.is_empty() => t.elapsed().as_secs(),
            _ => {
                *cell = Some((mouse, Instant::now()));
                0
            }
        }
    };
    let user_idle = idle_secs >= USER_IDLE_THRESHOLD_SECS && keys.is_empty();

    // Charge systeme
    let sys = System::new_all();
    let cpu_load = sys.global_cpu_usage() as f32;

    // GPU : NVML si dispo
    let gpu_idle = match nvml_wrapper::Nvml::init().ok().and_then(|n| n.device_by_index(0).ok()) {
        Some(dev) => dev.utilization_rates().map(|u| u.gpu < GPU_BUSY_THRESHOLD).unwrap_or(true),
        None => true,
    };

    let game_detected = !gpu_idle && cpu_load > CPU_BUSY_THRESHOLD;

    IdleStatus {
        should_mine: user_idle && gpu_idle && !game_detected,
        cpu_load,
        gpu_idle,
        user_idle_secs: idle_secs,
        game_detected,
    }
}
