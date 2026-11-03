// ComputeShare desktop - LAI-18/20/21
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod hw;
mod bench;
mod idle;

fn main() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![
            hw::detect_hardware,
            bench::run_benchmark,
            idle::get_idle_status
        ])
        .run(tauri::generate_context!())
        .expect("erreur au lancement de ComputeShare");
}
