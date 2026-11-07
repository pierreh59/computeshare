//! Sprint 21 - Disponibilite du noeud basee sur la charge machine.
//! Remplace l'ancienne regle "60 s sans souris/clavier" par :
//! - un interrupteur manuel On/Off (bouton dans l'app),
//! - la charge CPU (seuil CPU_MAX),
//! - la charge GPU (seuil GPU_MAX, si GPU NVIDIA detectee).
//! Le noeud accepte du travail seulement si : bouton ON + CPU < seuil
//! + GPU < seuil. Un echantillon est pris toutes les 2 s pour le graphe
//! d'activite (1 h d'historique).

use nvml_wrapper::Nvml;
use serde::Serialize;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Mutex, OnceLock};
use std::time::Duration;
use sysinfo::System;

const CPU_MAX: f32 = 50.0; // au-dela, machine occupee, on attend
const GPU_MAX: u32 = 30; // au-dela, jeu/rendu en cours, on attend
const SAMPLE_CAP: usize = 1800; // 2 s x 1800 = 1 h de graphe
const SAMPLE_PERIOD: Duration = Duration::from_secs(2);

/// Bouton On/Off manuel (true = disponible par defaut).
static MANUAL_ON: AtomicBool = AtomicBool::new(true);
/// Vrai pendant l'execution d'un chunk (pour le graphe).
static WORKING: AtomicBool = AtomicBool::new(false);

#[derive(Clone, Copy, Serialize)]
pub struct Sample {
    pub cpu: f32,
    pub gpu: u32,
    pub gpu_present: bool,
    pub available: bool,
    pub working: bool,
}

#[derive(Serialize)]
pub struct ActivityView {
    pub manual_on: bool,
    pub available: bool,
    pub cpu: f32,
    pub gpu: u32,
    pub gpu_present: bool,
    pub cpu_max: f32,
    pub gpu_max: u32,
    pub working: bool,
    pub samples: Vec<Sample>,
}

fn history() -> &'static Mutex<Vec<Sample>> {
    static CELL: OnceLock<Mutex<Vec<Sample>>> = OnceLock::new();
    CELL.get_or_init(|| Mutex::new(Vec::new()))
}

fn current() -> Sample {
    let mut sys = System::new_all();
    sys.refresh_cpu_usage();
    // sysinfo a besoin d'un intervalle entre 2 rafraichissements pour
    // calculer un usage moyen : petit sommeil avant la vraie lecture.
    std::thread::sleep(Duration::from_millis(250));
    sys.refresh_cpu_usage();
    let cpu = sys.global_cpu_usage() as f32;

    let (gpu, gpu_present) = match Nvml::init() {
        Ok(nvml) => match nvml.device_by_index(0) {
            Ok(dev) => (dev.utilization_rates().map(|u| u.gpu).unwrap_or(0), true),
            Err(_) => (0, false),
        },
        Err(_) => (0, false),
    };

    Sample {
        cpu,
        gpu,
        gpu_present,
        available: available_given(cpu, gpu),
        working: WORKING.load(Ordering::Relaxed),
    }
}

fn available_given(cpu: f32, gpu: u32) -> bool {
    MANUAL_ON.load(Ordering::Relaxed) && cpu < CPU_MAX && gpu < GPU_MAX
}

/// Le noeud accepte-t-il du travail ? (bouton + charge CPU + charge GPU)
pub fn available() -> bool {
    // Reutilise le dernier echantillon connu si le thread tourne deja
    // (evite de payer un refresh CPU a chaque claim), sinon echantillonne.
    if let Some(s) = history().lock().unwrap().last() {
        return s.available;
    }
    current().available
}

/// Demarre (une seule fois) le thread d'echantillonnage du graphe.
pub fn ensure_sampler() {
    static STARTED: OnceLock<()> = OnceLock::new();
    STARTED.get_or_init(|| {
        std::thread::spawn(|| loop {
            let s = current();
            let mut h = history().lock().unwrap();
            h.push(s);
            let len = h.len();
            if len > SAMPLE_CAP {
                h.drain(0..len - SAMPLE_CAP);
            }
            std::thread::sleep(SAMPLE_PERIOD);
        });
    });
}

#[tauri::command]
pub fn set_available(on: bool) {
    MANUAL_ON.store(on, Ordering::Relaxed);
}

#[tauri::command]
pub fn get_activity() -> ActivityView {
    ensure_sampler();
    let h = history().lock().unwrap().clone();
    let last = h.last().copied();
    ActivityView {
        manual_on: MANUAL_ON.load(Ordering::Relaxed),
        available: last.map(|s| s.available).unwrap_or_else(|| {
            MANUAL_ON.load(Ordering::Relaxed)
        }),
        cpu: last.map(|s| s.cpu).unwrap_or(0.0),
        gpu: last.map(|s| s.gpu).unwrap_or(0),
        gpu_present: last.map(|s| s.gpu_present).unwrap_or(false),
        cpu_max: CPU_MAX,
        gpu_max: GPU_MAX,
        working: WORKING.load(Ordering::Relaxed),
        samples: h,
    }
}

/// Appelle par node.rs : marque le debut/fin d'un chunk pour le graphe.
pub fn set_working(w: bool) {
    WORKING.store(w, Ordering::Relaxed);
}
