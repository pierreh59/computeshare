// ComputeShare desktop - LAI-18/20/21/22
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod hw;
mod bench;
mod idle;
mod sandbox;
mod jobs;

fn main() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![
            hw::detect_hardware,
            bench::run_benchmark,
            idle::get_idle_status,
            jobs::submit_chunk,
            jobs::list_results
        ])
        .run(tauri::generate_context!())
        .expect("erreur au lancement de ComputeShare");
}
