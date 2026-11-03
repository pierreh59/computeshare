// ComputeShare desktop - LAI-18/20/21/22/23/53
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod hw;
mod bench;
mod idle;
mod sandbox;
mod jobs;
mod live;
mod http;
mod node;

fn main() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![
            hw::detect_hardware,
            bench::run_benchmark,
            idle::get_idle_status,
            jobs::submit_chunk,
            jobs::list_results,
            live::get_live_stats,
            node::start_node,
            node::node_status
        ])
        .run(tauri::generate_context!())
        .expect("erreur au lancement de ComputeShare");
}
