//! LAI-19 - Detection du materiel.
//! GPU + VRAM via NVML (NVIDIA), CPU/RAM via sysinfo.

use serde::Serialize;

#[derive(Serialize)]
pub struct HardwareInfo {
    pub gpu_model: String,
    pub gpu_vram_mb: u32,
    pub gpu_category: String,
    pub cpu_model: String,
    pub cpu_cores: u32,
    pub ram_gb: u32,
    pub os: String,
}

fn categorize(gpu_model: &str, vram_mb: u32) -> String {
    let m = gpu_model.to_lowercase();
    if m.contains("3090") || m.contains("4090") || m.contains("4080") || vram_mb >= 20000 {
        "high_end"
    } else if m.contains("4070") || m.contains("3080") || m.contains("7800") || vram_mb >= 10000 {
        "prosumer"
    } else {
        "consumer"
    }
}

#[tauri::command]
pub fn detect_hardware() -> Result<HardwareInfo, String> {
    let (gpu_model, vram_mb) = match nvml_wrapper::Nvml::init() {
        Ok(nvml) => {
            let dev = nvml.device_by_index(0).map_err(|e| e.to_string())?;
            let name = dev.name().map_err(|e| e.to_string())?.to_string();
            let vram = (dev.memory_info().map_err(|e| e.to_string())?.total) / (1024 * 1024);
            (name, vram as u32)
        }
        Err(_) => ("Aucun GPU NVIDIA detecte".to_string(), 0),
    };

    let sys = sysinfo::System::new_all();
    let cpu_model = sys.cpus().first().map(|c| c.brand().to_string()).unwrap_or_default();
    let ram_gb = (sys.total_memory() / (1024 * 1024 * 1024)) as u32;
    let cpu_cores = sys.cpus().len() as u32;

    Ok(HardwareInfo {
        gpu_category: categorize(&gpu_model, vram_mb),
        gpu_model,
        gpu_vram_mb: vram_mb,
        cpu_model,
        cpu_cores,
        ram_gb,
        os: std::env::consts::OS.to_string(),
    })
}

#[cfg(test)]
mod tests {
    use super::categorize;

    #[test]
    fn test_categorize() {
        assert_eq!(categorize("NVIDIA GeForce RTX 3090", 24576), "high_end");
        assert_eq!(categorize("NVIDIA GeForce RTX 4070", 12288), "prosumer");
        assert_eq!(categorize("NVIDIA GeForce RTX 3060", 12288), "consumer");
    }
}
