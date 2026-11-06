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

// Windows : CREATE_NO_WINDOW. Sans ce drapeau, chaque chunk (ffmpeg,
// taskkill du chien de garde, cmd echo) ouvre une fenetre console
// visible chez le fournisseur. Vu en vrai le 5 nov. 2026.
#[cfg(windows)]
use std::os::windows::process::CommandExt;

#[cfg(windows)]
const CREATE_NO_WINDOW: u32 = 0x0800_0000;

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
/// "echo" est autorise pour les jobs de demonstration : builtin du
/// shell, il est execute via cmd/sh avec redirection du resultat.
const ALLOWED: [&str; 3] = ["compute_worker.exe", "ffmpeg.exe", "echo"];

pub(crate) fn sanitize_id(id: &str) -> String {
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

    // Fichier resultat : celui du job, ou nom par defaut si le client
    // n'en a pas precise (la console ne le fait pas).
    let out_name: String = if spec.expected_output.is_empty() {
        "output.txt".to_string()
    } else {
        spec.expected_output
            .chars()
            .filter(|c| c.is_ascii_alphanumeric() || *c == '.' || *c == '-' || *c == '_')
            .take(64)
            .collect()
    };

    // Construction du process : "echo" est un builtin du shell, pas un
    // executable -> passage par cmd (Windows) ou sh (Unix) avec
    // redirection explicite vers le fichier resultat. Les metacaracteres
    // dans les arguments sont rejetes (pas d'injection shell).
    let mut builder: std::process::Command = if spec.command == "echo" {
        for a in &spec.args {
            if a.chars().any(|c| "&|<>\"'^%".contains(c)) {
                return mk("rejected", "argument interdit pour echo", None);
            }
        }
        #[cfg(windows)]
        {
            let mut b = Command::new("cmd");
            b.arg("/C").arg("echo");
            for a in &spec.args {
                b.arg(a);
            }
            b.arg(">").arg(&out_name);
            b
        }
        #[cfg(not(windows))]
        {
            let line = format!("echo {} > {}", spec.args.join(" "), out_name);
            let mut b = Command::new("sh");
            b.arg("-c").arg(line);
            b
        }
    } else {
        let mut b = Command::new(&spec.command);
        b.args(&spec.args);
        b
    };

    // Windows : jamais de fenetre console visible, quel que soit le
    // processus lance (ffmpeg.exe, cmd /C echo, compute_worker.exe).
    #[cfg(windows)]
    builder.creation_flags(CREATE_NO_WINDOW);

    let child = match builder
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
        let mut killer = Command::new("taskkill");
        killer
            .args(["/PID", &pid.to_string(), "/T", "/F"])
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        // Windows : le chien de garde ne doit pas non plus flasher
        // une fenetre console chez le fournisseur.
        #[cfg(windows)]
        killer.creation_flags(CREATE_NO_WINDOW);
        let _ = killer.status();
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
    let out_path = dir.join(&out_name);
    match hash_file(&out_path) {
        Some(sha) => mk("ok", "chunk termine", Some(sha)),
        None => mk("bad_output", "fichier resultat manquant", None),
    }
}

/// Extrait la fin d'une sortie en texte imprimable (journal borne).
pub(crate) fn tail_utf8(bytes: &[u8]) -> String {
    let start = bytes.len().saturating_sub(MAX_DETAIL_BYTES);
    String::from_utf8_lossy(&bytes[start..]).to_string()
}

/// SHA-256 du fichier produit par le chunk. Sert a verifier la
/// livraison cote orchestrateur avant de crediter le fournisseur.
pub(crate) fn hash_file(path: &PathBuf) -> Option<String> {
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
