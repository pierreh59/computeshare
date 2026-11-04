//! Sprint 16 (LAI-62) - Configuration persistante de l'app fournisseur.
//! Objectif : ZERO configuration pour l'utilisateur. Le seul reel
//! parametrage est la connexion (e-mail + mot de passe) ; la gateway
//! par defaut est la plateforme de production et le jeton de noeud est
//! recupere automatiquement via le compte.
//!
//! Stockage : %APPDATA%\ComputeShare\config.json (Windows) - survit aux
//! nettoyages du cache WebView (contrairement au localStorage).

use serde::{Deserialize, Serialize};
use std::fs;
use std::path::PathBuf;

/// Gateway de production par defaut. Editable dans l'ecran de
/// connexion ("Parametres avances") uniquement pour les tests.
pub const DEFAULT_GATEWAY: &str = "https://api.computeshare.fr";

#[derive(Serialize, Deserialize, Clone)]
#[serde(default)]
pub struct Config {
    pub gateway: String,
    pub email: String,
    /// Jeton de noeud personnel csnode_... (recupere via le compte).
    /// Vide = non connecte.
    pub node_token: String,
    /// Identite stable du noeud (sans elle, chaque lancement creait un
    /// nouveau node_id et le wallet repartait de zero).
    pub node_id: String,
}

impl Default for Config {
    fn default() -> Self {
        Config {
            gateway: DEFAULT_GATEWAY.to_string(),
            email: String::new(),
            node_token: String::new(),
            node_id: String::new(),
        }
    }
}

fn config_dir() -> Option<PathBuf> {
    dirs::config_dir().map(|d| d.join("ComputeShare"))
}

/// Charge la configuration ; defauts si absente ou corrompue.
pub fn load() -> Config {
    config_dir()
        .and_then(|d| fs::read_to_string(d.join("config.json")).ok())
        .and_then(|s| serde_json::from_str(&s).ok())
        .unwrap_or_default()
}

/// Sauvegarde la configuration (cree le dossier au besoin).
pub fn save(cfg: &Config) -> Result<(), String> {
    let dir = config_dir().ok_or("repertoire de configuration introuvable")?;
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let json = serde_json::to_string_pretty(cfg).unwrap_or_default();
    fs::write(dir.join("config.json"), json).map_err(|e| e.to_string())
}

/// Genere un identifiant de noeud stable du style "node-ab12cd".
pub fn generate_node_id() -> String {
    use std::hash::{Hash, Hasher};
    let mut h = std::collections::hash_map::DefaultHasher::new();
    std::time::SystemTime::now().hash(&mut h);
    std::process::id().hash(&mut h);
    let s = format!("{:016x}", h.finish());
    format!("node-{}", &s[..6])
}
