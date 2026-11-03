// LAI-18 - Squelette app desktop ComputeShare (Tauri 2)
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod hw;

fn main() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![hw::detect_hardware])
        .run(tauri::generate_context!())
        .expect("erreur au lancement de ComputeShare");
}
