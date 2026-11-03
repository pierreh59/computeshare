//! LAI-22 - File locale de chunks : soumission, execution, historique.

use crate::sandbox::{run_chunk, ChunkResult, ChunkSpec};
use std::sync::{Mutex, OnceLock};

fn history() -> &'static Mutex<Vec<ChunkResult>> {
    static CELL: OnceLock<Mutex<Vec<ChunkResult>>> = OnceLock::new();
    CELL.get_or_init(|| Mutex::new(Vec::new()))
}

/// Soumet un chunk : execute dans le sandbox, enregistre le resultat.
#[tauri::command]
pub fn submit_chunk(spec: ChunkSpec) -> ChunkResult {
    let result = run_chunk(spec);
    history().lock().unwrap().push(result.clone());
    result
}

/// Historique des chunks executes sur ce noeud (plus recents en dernier).
#[tauri::command]
pub fn list_results() -> Vec<ChunkResult> {
    history().lock().unwrap().clone()
}
