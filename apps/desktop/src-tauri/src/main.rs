// ComputeShare desktop - LAI-18/20/21/22/23/53 + Sprint 16 (LAI-62 :
// connexion par compte fournisseur, zero configuration, autostart,
// mises a jour automatiques, installateur NSIS).
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod hw;
mod bench;
mod idle;
mod sandbox;
mod container;
mod jobs;
mod live;
mod http;
mod node;
mod config;
mod auth;

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_autostart::init(
            tauri_plugin_autostart::MacosLauncher::LaunchAgent,
            None,
        ))
        .invoke_handler(tauri::generate_handler![
            hw::detect_hardware,
            bench::run_benchmark,
            idle::get_idle_status,
            jobs::submit_chunk,
            jobs::list_results,
            live::get_live_stats,
            node::start_node,
            node::node_status,
            auth::auth_status,
            auth::login,
            auth::logout,
        ])
        .run(tauri::generate_context!())
        .expect("erreur au lancement de ComputeShare");
}
