//! Sprint 16 (LAI-62) - Connexion par identifiants du compte
//! fournisseur. L'utilisateur colle son e-mail + mot de passe (compte
//! cree sur https://computeshare.fr) ; l'app recupere TOUT le reste :
//! session, jeton de noeud personnel, node_id stable.
//!
//! Flux : POST /auth/login -> session ; POST /auth/seller-token
//! (rotation) -> jeton csnode_... persiste dans config.json.

use serde::Serialize;

#[derive(Serialize, Clone)]
pub struct AuthState {
    pub logged_in: bool,
    pub email: String,
    pub gateway: String,
    pub node_id: String,
}

fn state_from(cfg: &crate::config::Config) -> AuthState {
    AuthState {
        logged_in: !cfg.node_token.is_empty(),
        email: cfg.email.clone(),
        gateway: cfg.gateway.clone(),
        node_id: cfg.node_id.clone(),
    }
}

/// Etat de connexion (appelle au lancement pour choisir l'ecran).
#[tauri::command]
pub fn auth_status() -> AuthState {
    state_from(&crate::config::load())
}

/// Connexion : e-mail + mot de passe du compte fournisseur. En cas de
/// succes, recupere un jeton de noeud frais et le persiste. La rotation
/// remplace l'eventuel ancien jeton (une machine active par compte en
/// beta).
#[tauri::command]
pub fn login(gateway: String, email: String, password: String) -> Result<AuthState, String> {
    let gateway = if gateway.trim().is_empty() {
        crate::config::DEFAULT_GATEWAY.to_string()
    } else {
        gateway.trim().trim_end_matches('/').to_string()
    };
    let email = email.trim().to_lowercase();
    if email.is_empty() || password.is_empty() {
        return Err("E-mail et mot de passe requis.".into());
    }

    // 1. Connexion au compte.
    let body = serde_json::json!({"email": email, "password": password}).to_string();
    let resp = match crate::http::post_json(&gateway, "/auth/login", &body) {
        Some(r) => r,
        None => return Err("Serveur injoignable : verifiez votre connexion Internet.".into()),
    };
    match resp.status {
        200 => {}
        401 => return Err("E-mail ou mot de passe incorrect.".into()),
        429 => return Err("Trop de tentatives : reessayez dans 15 minutes.".into()),
        s => return Err(format!("Erreur serveur (HTTP {}).", s)),
    }
    let parsed: serde_json::Value =
        serde_json::from_str(&resp.body).map_err(|_| "Reponse du serveur invalide.")?;
    let session = parsed["session_token"].as_str().unwrap_or_default().to_string();
    if session.is_empty() {
        return Err("Session absente de la reponse du serveur.".into());
    }
    // Seuls les comptes fournisseur pilotent un noeud.
    let role = parsed["user"]["role"].as_str().unwrap_or_default();
    if role != "seller" {
        return Err("Ce compte n'est pas un compte fournisseur. Creez un compte fournisseur sur https://computeshare.fr/signup.".into());
    }

    // 2. Jeton de noeud personnel (rotation : il remplace l'ancien).
    let resp = match crate::http::post_json_bearer(&session, &gateway, "/auth/seller-token", "{}") {
        Some(r) => r,
        None => return Err("Serveur injoignable pendant la recuperation du jeton.".into()),
    };
    if resp.status != 200 {
        return Err(format!("Recuperation du jeton impossible (HTTP {}).", resp.status));
    }
    let parsed: serde_json::Value =
        serde_json::from_str(&resp.body).map_err(|_| "Reponse du serveur invalide.")?;
    let node_token = parsed["node_token"].as_str().unwrap_or_default().to_string();
    if node_token.is_empty() {
        return Err("Jeton de noeud absent de la reponse du serveur.".into());
    }

    // 3. Persistence : gateway + email + jeton + node_id stable.
    let mut cfg = crate::config::load();
    if cfg.node_id.is_empty() {
        cfg.node_id = crate::config::generate_node_id();
    }
    cfg.gateway = gateway;
    cfg.email = email;
    cfg.node_token = node_token.clone();
    crate::config::save(&cfg)
        .map_err(|e| format!("Sauvegarde de la configuration impossible : {}", e))?;
    crate::http::set_node_token(&node_token);
    Ok(state_from(&cfg))
}

/// Deconnexion : efface le jeton (l'e-mail reste pre-rempli). Le noeud
/// cesse de se declarer ; les gains restent attaches au compte.
#[tauri::command]
pub fn logout() -> Result<(), String> {
    let mut cfg = crate::config::load();
    cfg.node_token = String::new();
    crate::config::save(&cfg)?;
    Ok(())
}
