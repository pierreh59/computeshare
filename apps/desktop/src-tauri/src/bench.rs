//! LAI-20 - Benchmark d'entree standardise.
//! Mesure un debit de calcul reproductible (hashage SHA-256) pendant que NVML
//! echantillonne l'etat du GPU. Le score sert a :
//!   1. calibrer les gains du fournisseur,
//!   2. detecter un GPU falsifie (score aberrant vs baseline connue).

use serde::Serialize;
use sha2::Digest;
use std::time::{Duration, Instant};
use nvml_wrapper::Nvml;

#[derive(Serialize)]
pub struct BenchResult {
    pub score: f64,           // Mo/s de hashage
    pub gpu_util_max: u32,    // % utilisation GPU max pendant le test
    pub gpu_mem_used_mb: u32,
    pub duration_secs: u64,
    pub verdict: String,     // ok | suspicious
}

const DURATION: Duration = Duration::from_secs(5);

/// Baseline minimale par categorie (Mo/s). En dessous = materiel menteur
/// ou environneemnt hostile (VM sans GPU passe). A ajuster avec la beta.
fn baseline_for(gpu_category: &str) -> f64 {
    match gpu_category {
        "high_end" => 400.0,
        "prosumer" => 300.0,
        _ => 200.0,
    }
}

#[tauri::command]
pub fn run_benchmark(gpu_category: String) -> BenchResult {
    let start = Instant::now();
    let mut hashed_bytes: u64 = 0;
    let mut payload = [0u8; 1024 * 1024]; // 1 Mo de travail
    for (i, b) in payload.iter_mut().enumerate() { *b = (i % 251) as u8; }

    // GPU inconnu -> NVML indisponible : on note 0, le verdict reste CPU.
    let nvml = Nvml::init().ok();
    let dev = nvml.and_then(|n| n.device_by_index(0).ok());
    let mut gpu_util_max = 0u32;
    let mut gpu_mem_used_mb = 0u32;

    while start.elapsed() < DURATION {
        let t0 = Instant::now();
        let h = sha2::Sha256::digest(&payload);
        let _ = h; // le travail est le hashage lui-meme
        hashed_bytes += payload.len() as u64;
        if let Some(d) = &dev {
            if let Ok(u) = d.utilization_rates() {
                gpu_util_max = gpu_util_max.max(u.gpu);
            }
            if let Ok(m) = d.memory_info() {
                gpu_mem_used_mb = (m.used / (1024 * 1024)) as u32;
            }
        }
        // pas de pause : bench plein gaz
        let _ = t0.elapsed();
    }

    let secs = start.elapsed().as_secs_f64();
    let score = (hashed_bytes as f64 / (1024.0 * 1024.0)) / secs;
    let verdict = if score >= baseline_for(&gpu_category) { "ok" } else { "suspicious" }.to_string();

    BenchResult { score, gpu_util_max, gpu_mem_used_mb, duration_secs: secs as u64, verdict }
}

#[cfg(test)]
mod tests {
    #[test]
    fn baselines_croissantes() {
        assert!(super::baseline_for("high_end") > super::baseline_for("prosumer"));
        assert!(super::baseline_for("prosumer") > super::baseline_for("consumer"));
    }
}
