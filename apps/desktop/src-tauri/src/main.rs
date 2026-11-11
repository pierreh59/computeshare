// ComputeShare desktop - LAI-18/20/21/22/23/53 + Sprint 16 (LAI-62 :
// connexion par compte fournisseur, zero configuration, autostart,
// mises a jour automatiques, installateur NSIS).
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod hw;
mod bench;
mod idle;
mod availability;
mod sandbox;
mod container;
mod jobs;
mod live;
mod http;
mod node;
mod config;
mod auth;

fn main() {
    // Sprint 36 : journaliser TOUTES les panics de l'app (n'importe quel
    // fil) dans %TEMP%/computeshare-node.log. Une app Windows sans
    // console avale les panics en silence : c'est ainsi qu'un fil mort
    // restait invisible le 11 nov. 2026 (noeud en ligne, zero job
    // traite). Ce hook ne remplace pas le catch_unwind du fil de poll
    // (node.rs) : il rend visibles les panics des AUTRES fils.
    std::panic::set_hook(Box::new(|info| {
        let fil = format!("{:?}", std::thread::current().id());
        crate::node::trace(&format!("PANIQUE {} : {}", fil, info));
    }));
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
            availability::set_available,
            availability::get_activity,
            auth::auth_status,
            auth::login,
            auth::logout,
        ])
        .run(tauri::generate_context!())
        .expect("erreur au lancement de ComputeShare");
}
