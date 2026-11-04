//! Client HTTP(S) du noeud : ureq + TLS (rustls), remplace le client
//! TCP brut de dev qui ne savait pas parler HTTPS (beta LAI-12).
//! Meme interface publique que l'ancien : get / post_json / Response.

pub struct Response {
    pub status: u16,
    pub body: String,
}

/// Jeton d'enrolement du noeud (Sprint 14) : identique a la variable
/// NODE_ENROLLMENT_TOKEN de l'orchestrateur. Defaut dev pour le local.
fn node_token() -> String {
    std::env::var("COMPUTERSHARE_NODE_TOKEN").unwrap_or_else(|_| "dev-node-token".to_string())
}

/// Construit l'URL complete depuis la gateway et le chemin.
/// "https://api.computeshare.fr" + "/register" -> "https://api.computeshare.fr/register"
fn full_url(url: &str, path: &str) -> String {
    format!("{}/{}", url.trim().trim_end_matches('/'), path)
}

fn agent() -> ureq::Agent {
    ureq::AgentBuilder::new()
        .timeout_connect(std::time::Duration::from_secs(10))
        .timeout(std::time::Duration::from_secs(30))
        .build()
}

fn request(method: &str, url: &str, path: &str, body: Option<&str>) -> Option<Response> {
    let url = full_url(url, path);
    let call = agent().request(method, &url)
        .set("X-Node-Token", &node_token());
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
