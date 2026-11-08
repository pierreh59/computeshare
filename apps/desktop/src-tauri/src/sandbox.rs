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
    /// Sprint 30 : temps de telechargement de l'image Docker
    /// (0 pour les jobs sandbox classiques). Facture au client a
    /// tarif reduit, jamais paye au vendeur.
    pub pull_ms: u64,
    /// Sprint 30 : temps de CALCUL reel (execution). Le vendeur
    /// n'est paye que sur cette duree.
    pub compute_ms: u64,
    pub output_sha256: Option<String>,
    /// Sprint 26 : chemin du fichier de resultat sur le noeud, a
    /// rapatrier vers le serveur apres le rapport (None si echec).
    pub output_path: Option<String>,
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
    let mk = |outcome: &str, detail: &str, sha: Option<String>, path: Option<String>| {
        let total = started.elapsed().as_millis();
        ChunkResult {
            chunk_id: spec.chunk_id.clone(),
            outcome: outcome.to_string(),
            detail: detail.to_string(),
            duration_ms: total,
            // Sprint 30 : un job sandbox n'a pas d'image a
            // telecharger : tout le temps est du calcul.
            pull_ms: 0,
            compute_ms: total as u64,
            output_sha256: sha,
            output_path: path,
        }
    };

    if !is_allowed(&spec.command) {
        return mk("rejected", "commande hors allowlist", None, None);
    }
    let dir = PathBuf::from(std::env::temp_dir())
        .join("computeshare-sandbox")
        .join(sanitize_id(&spec.chunk_id));
    let _ = fs::remove_dir_all(&dir);
    if fs::create_dir_all(&dir).is_err() {
        return mk("failed", "impossible de creer le repertoire sandbox", None, None);
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
                return mk("rejected", "argument interdit pour echo", None, None);
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
        Err(e) => return mk("failed", &format!("spawn: {}", e), None, None),
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
        Err(e) => return mk("failed", &format!("wait: {}", e), None, None),
    };
    if started.elapsed() >= timeout {
        return mk("timeout", "delai depasse, processus tue", None, None);
    }
    if !output.status.success() {
        return mk("failed", &tail_utf8(&output.stderr), None, None);
    }
    let out_path = dir.join(&out_name);
    match hash_file(&out_path) {
        // Sprint 26 : le fichier de resultat est rapatrie apres rapport.
        Some(sha) => mk("ok", "chunk termine", Some(sha), Some(out_path.to_string_lossy().into_owned())),
        None => mk("bad_output", "fichier resultat manquant", None, None),
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

/// Sprint 27c : efface les dossiers locaux d'un chunk (sandbox de
/// travail + assets d'entree) une fois le job termine. Les fichiers
/// du client ne doivent pas rester sur le PC du fournisseur apres
/// la remontee du resultat (confidentialite des donnees clientes).
pub(crate) fn cleanup_chunk_dirs(chunk_id: &str) {
    let id = sanitize_id(chunk_id);
    let base = PathBuf::from(std::env::temp_dir());
    let _ = fs::remove_dir_all(base.join("computeshare-sandbox").join(&id));
    let _ = fs::remove_dir_all(base.join("computeshare-assets").join(&id));
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

    #[test]
    fn cleanup_efface_les_dossiers_du_chunk() {
        let id = super::sanitize_id("test-cleanup-27c");
        let base = std::path::PathBuf::from(std::env::temp_dir());
        let sandbox = base.join("computeshare-sandbox").join(&id);
        let assets = base.join("computeshare-assets").join(&id);
        std::fs::create_dir_all(&sandbox).unwrap();
        std::fs::create_dir_all(&assets).unwrap();
        std::fs::write(sandbox.join("entree.txt"), "donnees cliente").unwrap();
        std::fs::write(assets.join("entree.txt"), "donnees cliente").unwrap();
        super::cleanup_chunk_dirs("test-cleanup-27c");
        assert!(!sandbox.exists());
        assert!(!assets.exists());
    }
}
