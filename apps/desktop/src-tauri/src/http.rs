//! Client HTTP(S) du noeud : ureq + TLS (rustls), remplace le client
//! TCP brut de dev qui ne savait pas parler HTTPS (beta LAI-12).
//! Meme interface publique que l'ancien : get / post_json / Response.

pub struct Response {
    pub status: u16,
    pub body: String,
}

/// Jeton du noeud. Sprint 16 : priorite a la variable d'environnement
/// (COMPUTERSHARE_NODE_TOKEN, dev/tests), sinon au jeton personnel
/// csnode_... recupere via la connexion (auth.rs), sinon dev local.
static NODE_TOKEN: std::sync::OnceLock<std::sync::Mutex<String>> = std::sync::OnceLock::new();

pub fn set_node_token(token: &str) {
    let cell = NODE_TOKEN.get_or_init(|| std::sync::Mutex::new(String::new()));
    *cell.lock().unwrap() = token.to_string();
}

fn node_token() -> String {
    if let Ok(t) = std::env::var("COMPUTERSHARE_NODE_TOKEN") {
        return t;
    }
    if let Some(cell) = NODE_TOKEN.get() {
        let t = cell.lock().unwrap().clone();
        if !t.is_empty() {
            return t;
        }
    }
    "dev-node-token".to_string()
}

/// Construit l'URL complete depuis la gateway et le chemin, en evitant
/// le double slash "https://gw//path" qui declenchait une redirection
/// 301 (et la perte du corps POST) cote serveur.
/// "https://api.computeshare.fr" + "/register" -> "https://api.computeshare.fr/register"
fn full_url(url: &str, path: &str) -> String {
    let base = url.trim().trim_end_matches('/');
    let p = path.trim_start_matches('/');
    format!("{}/{}", base, p)
}

fn agent() -> ureq::Agent {
    ureq::AgentBuilder::new()
        .timeout_connect(std::time::Duration::from_secs(10))
        .timeout(std::time::Duration::from_secs(30))
        .build()
}

fn request(method: &str, url: &str, path: &str, body: Option<&str>) -> Option<Response> {
    request_bearer(method, url, path, body, None)
}

/// Variante avec en-tete Authorization Bearer (API du compte, Sprint 16).
fn request_bearer(method: &str, url: &str, path: &str, body: Option<&str>, bearer: Option<&str>) -> Option<Response> {
    let url = full_url(url, path);
    let mut call = agent().request(method, &url)
        .set("X-Node-Token", &node_token());
    if let Some(b) = bearer {
        call = call.set("Authorization", &format!("Bearer {}", b));
    }
    let resp = match body {
        Some(b) => call.set("Content-Type", "application/json").send_string(b),
        None => call.call(),
    };
    // Les reponses d'erreur HTTP (4xx/5xx) restent des reponses :
    // on garde le status et le corps pour que l'appelant decide.
    let resp = match resp {
        Ok(r) => r,
        Err(ureq::Error::Status(_, r)) => r,
        Err(_) => return None, // reseau injoignable (DNS, TLS, timeout)
    };
    let status = resp.status();
    let body = resp.into_string().unwrap_or_default();
    Some(Response { status, body })
}

pub fn get(url: &str, path: &str) -> Option<Response> {
    request("GET", url, path, None)
}

pub fn post_json(url: &str, path: &str, body: &str) -> Option<Response> {
    request("POST", url, path, Some(body))
}

pub fn post_json_bearer(bearer: &str, url: &str, path: &str, body: &str) -> Option<Response> {
    request_bearer("POST", url, path, Some(body), Some(bearer))
}

/// Sprint 27 : telechargement BINAIRE d'un asset d'entree vers un
/// fichier. Le corps n'est pas de l'UTF-8 fiable : on ne passe
/// surtout PAS par into_string(). Timeout eleve (asset jusqu'a
/// 100 Mo). Le corps n'est ecrit que si HTTP 200.
pub fn get_file(url: &str, path: &str, dest: &std::path::Path) -> Option<Response> {
    let full = full_url(url, path);
    let agent = ureq::AgentBuilder::new()
        .timeout_connect(std::time::Duration::from_secs(10))
        .timeout(std::time::Duration::from_secs(600))
        .build();
    let resp = match agent
        .request("GET", &full)
        .set("X-Node-Token", &node_token())
        .call()
    {
        Ok(r) => r,
        Err(ureq::Error::Status(_, r)) => r,
        Err(_) => return None,
    };
    let status = resp.status();
    if status == 200 {
        let mut reader = resp.into_reader();
        if let Ok(mut f) = std::fs::File::create(dest) {
            let _ = std::io::copy(&mut reader, &mut f);
        }
    }
    Some(Response { status, body: String::new() })
}

/// Sprint 26 : upload du fichier de resultat d'un chunk (octets bruts,
/// en-tete X-Chunk-Id). Timeout dedie : un gros fichier (jusqu'a 5 Go)
/// peut prendre longtemps a monter, on ne reutilise pas le timeout de
/// 30 s des appels JSON.
pub fn post_file(url: &str, path: &str, chunk_id: &str, file: std::fs::File) -> Option<Response> {
    let full = full_url(url, path);
    let agent = ureq::AgentBuilder::new()
        .timeout_connect(std::time::Duration::from_secs(10))
        .timeout(std::time::Duration::from_secs(3600))
        .build();
    let resp = agent
        .request("POST", &full)
        .set("X-Node-Token", &node_token())
        .set("X-Chunk-Id", chunk_id)
        .set("Content-Type", "application/octet-stream")
        .send(file);
    let resp = match resp {
        Ok(r) => r,
        Err(ureq::Error::Status(_, r)) => r,
        Err(_) => return None,
    };
    let status = resp.status();
    let body = resp.into_string().unwrap_or_default();
    Some(Response { status, body })
}
