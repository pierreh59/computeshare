//! Client HTTP minimal pour le MVP : gateway en HTTP local uniquement.
//! Sera remplace par un client avec TLS (ureq) avant la beta.

use std::io::{Read, Write};
use std::net::TcpStream;

pub struct Response {
    pub status: u16,
    pub body: String,
}

/// "http://localhost:8080" -> ("localhost", 8080)
pub fn split_url(url: &str) -> (String, u16) {
    let cleaned = url.trim().trim_start_matches("http://").trim_end_matches('/');
    match cleaned.rsplit_once(':') {
        Some((h, p)) => (h.to_string(), p.parse().unwrap_or(80)),
        None => (cleaned.to_string(), 80),
    }
}

fn request(method: &str, url: &str, path: &str, body: Option<&str>) -> Option<Response> {
    let (host, port) = split_url(url);
    let mut stream = TcpStream::connect((host.as_str(), port)).ok()?;
    let req = match body {
        Some(b) => format!(
            "{} {} HTTP/1.1\r\nHost: {}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
            method, path, host, b.len(), b
        ),
        None => format!(
            "{} {} HTTP/1.1\r\nHost: {}\r\nConnection: close\r\n\r\n",
            method, path, host
        ),
    };
    stream.write_all(req.as_bytes()).ok()?;
    let mut raw = Vec::new();
    stream.read_to_end(&mut raw).ok()?;
    let text = String::from_utf8_lossy(&raw).to_string();
    let status = text.get(9..12)?.parse().ok()?;
    let split = text.find("\r\n\r\n")?;
    Some(Response { status, body: text[split + 4..].to_string() })
}

pub fn get(url: &str, path: &str) -> Option<Response> {
    request("GET", url, path, None)
}

pub fn post_json(url: &str, path: &str, body: &str) -> Option<Response> {
    request("POST", url, path, Some(body))
}
