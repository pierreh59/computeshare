//! LAI-22 - Sandbox d'execution embarque.
//! Un chunk client s'execute dans un repertoire isole, sous contraintes :
//! allowlist de commandes, timeout dur, checksum SHA-256 du resultat.

use serde::{Deserialize, Serialize};
use sha2::Digest;
use std::fs::{self, File};
use std::io::Read;
use std::path::PathBuf;
use std::process::{Command, Stdio};
use std::thread;
use std::time::{Duration, Instant};

#[derive(Deserialize)]
pub struct ChunkSpec {
    pub chunk_id: String,
    pub command: String,
    pub args: Vec<String>,
    pub expected_output: String,
    pub timeout_secs: u64,
}

#[derive(Serialize, Clone)]
pub struct ChunkResult {
    pub chunk_id: String,
    pub outcome: String, // ok | timeout | failed | bad_output | rejected
    pub detail: String,
    pub duration_ms: u128,
    pub output_sha256: Option<String>,
}

const MAX_DETAIL_BYTES: usize = 16 * 1024;
const DEFAULT_TIMEOUT_SECS: u64 = 300;
const MAX_TIMEOUT_SECS: u64 = 3600;

/// Seules ces commandes peuvent tourner. Tout le reste est rejete.
const ALLOWED: [&str; 2] = ["compute_worker.exe", "ffmpeg.exe"];

fn sanitize_id(id: &str) -> String {
    id.chars()
        .filter(|c| c.is_ascii_alphanumeric() || *c == '-')
        .take(64)
        .collect()
}

/// Allowlist stricte : nom nu uniquement, pas de chemin complet,
/// pas de tentative de traverser l'arborescence.
fn is_allowed(command: &str) -> bool {
    if command.contains('/') || command.contains('\\') {
        return false;
    }
    ALLOWED.contains(&command)
}

/// Prepare et execute un chunk dans son repertoire sandbox dedie.
pub fn run_chunk(spec: ChunkSpec) -> ChunkResult {
    let started = Instant::now();
    let mk = |outcome: &str, detail: &str, sha: Option<String>| ChunkResult {
        chunk_id: spec.chunk_id.clone(),
        outcome: outcome.to_string(),
        detail: detail.to_string(),
        duration_ms: started.elapsed().as_millis(),
        output_sha256: sha,
    };

    if !is_allowed(&spec.command) {
        return mk("rejected", "commande hors allowlist", None);
    }
    let dir = PathBuf::from(std::env::temp_dir())
        .join("computeshare-sandbox")
        .join(sanitize_id(&spec.chunk_id));
    let _ = fs::remove_dir_all(&dir);
    if fs::create_dir_all(&dir).is_err() {
        return mk("failed", "impossible de creer le repertoire sandbox", None);
    }

    let timeout = if spec.timeout_secs == 0 {
        Duration::from_secs(DEFAULT_TIMEOUT_SECS)
    } else {
        Duration::from_secs(spec.timeout_secs.min(MAX_TIMEOUT_SECS))
    };

    let mut child = match Command::new(&spec.command)
        .args(&spec.args)
        .current_dir(&dir)
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
    {
        Ok(c) => c,
        Err(e) => return mk("failed", &format!("spawn: {}", e), None),
    };

    // Chien de garde : tue l'arbre de processus au bout du timeout.
    let pid = child.id();
    thread::spawn(move || {
        thread::sleep(timeout);
        let _ = Command::new("taskkill")
            .args(["/PID", &pid.to_string(), "/T", "/F"])
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .status();
    });

    let output = match child.wait_with_output() {
        Ok(o) => o,
        Err(e) => return mk("failed", &format!("wait: {}", e), None),
    };
    if started.elapsed() >= timeout {
        return mk("timeout", "delai depasse, processus tue", None);
    }
    if !output.status.success() {
        return mk("failed", &tail_utf8(&output.stderr), None);
    }
    let out_path = dir.join(&spec.expected_output);
    match hash_file(&out_path) {
        Some(sha) => mk("ok", "chunk termine", Some(sha)),
        None => mk("bad_output", "fichier resultat manquant", None),
    }
}

/// Extrait la fin d'une sortie en texte imprimable (journal borne).
fn tail_utf8(bytes: &[u8]) -> String {
    let start = bytes.len().saturating_sub(MAX_DETAIL_BYTES);
    String::from_utf8_lossy(&bytes[start..]).to_string()
}

/// SHA-256 du fichier produit par le chunk. Sert a verifier la
/// livraison cote orchestrateur avant de crediter le fournisseur.
fn hash_file(path: &PathBuf) -> Option<String> {
    let mut f = File::open(path).ok()?;
    let mut buf = Vec::new();
    f.read_to_end(&mut buf).ok()?;
    let digest = sha2::Sha256::digest(&buf);
    Some(format!("{:x}", digest))
}

#[cfg(test)]
mod tests {
    #[test]
    fn ids_sont_nettoyes() {
        let s = super::sanitize_id("abc../../..\\..&X");
        assert!(!s.contains('/'));
        assert!(!s.contains('\\'));
        assert!(!s.contains('.'));
        assert!(s.contains("X"));
    }

    #[test]
    fn chemins_complets_refuses() {
        assert!(super::is_allowed("ffmpeg.exe"));
        assert!(!super::is_allowed("C:\\evil\\ffmpeg.exe"));
        assert!(!super::is_allowed("cmd.exe"));
    }
}
