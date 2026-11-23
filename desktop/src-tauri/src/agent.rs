//! Thin HTTP client for the imonitord agent API (docs/API.md). The frontend talks to agents
//! exclusively through `agent_request`, so the webview needs no network permissions / CORS.

use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::sync::OnceLock;
use std::time::Duration;

pub const AGENT_PORT: u16 = 7878;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Identity {
    #[serde(default)]
    pub service: String,
    #[serde(default)]
    pub api_version: Option<i64>,
    #[serde(default)]
    pub version: Option<String>,
    pub node_id: String,
    #[serde(default)]
    pub hostname: Option<String>,
    #[serde(default)]
    pub node_name: Option<String>,
    #[serde(default)]
    pub os: Option<String>,
    #[serde(default)]
    pub arch: Option<String>,
    #[serde(default)]
    pub uptime_sec: Option<i64>,
    #[serde(default)]
    pub started_at: Option<i64>,
    #[serde(default)]
    pub paired: Option<bool>,
}

impl Identity {
    pub fn is_agent(&self) -> bool {
        self.service == "internetmonitor-agent"
    }
    pub fn display_name(&self) -> String {
        if let Some(n) = self.node_name.as_ref().filter(|n| !n.is_empty()) {
            return n.clone();
        }
        self.hostname.clone().unwrap_or_else(|| self.node_id.clone())
    }
}

/// Error shape returned to the frontend: `status` 0 = transport error.
#[derive(Debug, Clone, Serialize)]
pub struct AgentError {
    pub status: u16,
    pub message: String,
}

impl AgentError {
    fn transport(e: impl std::fmt::Display) -> Self {
        AgentError { status: 0, message: e.to_string() }
    }
}

pub fn client() -> &'static reqwest::Client {
    static C: OnceLock<reqwest::Client> = OnceLock::new();
    C.get_or_init(|| {
        reqwest::Client::builder()
            .no_proxy()
            .pool_max_idle_per_host(4)
            .pool_idle_timeout(Duration::from_secs(30))
            .connect_timeout(Duration::from_secs(4))
            .user_agent(concat!("InternetMonitor-Desktop/", env!("CARGO_PKG_VERSION")))
            .build()
            .expect("http client")
    })
}

pub fn base_url(host: &str, port: u16) -> String {
    if host.contains(':') && !host.starts_with('[') {
        format!("http://[{host}]:{port}")
    } else {
        format!("http://{host}:{port}")
    }
}

/// Extracts the agent's `{"error": "..."}` message from an error body.
pub fn error_message(body: &str) -> String {
    serde_json::from_str::<Value>(body)
        .ok()
        .and_then(|v| v.get("error").and_then(|e| e.as_str()).map(str::to_string))
        .unwrap_or_else(|| {
            let t: String = body.chars().take(200).collect();
            if t.trim().is_empty() { "request failed".into() } else { t.trim().to_string() }
        })
}

pub async fn request(
    host: &str,
    port: u16,
    token: Option<&str>,
    method: &str,
    path: &str,
    body: Option<&Value>,
    timeout: Duration,
) -> Result<Value, AgentError> {
    let url = format!("{}{}", base_url(host, port), path);
    let m = reqwest::Method::from_bytes(method.as_bytes()).map_err(AgentError::transport)?;
    let mut rb = client().request(m, &url).timeout(timeout);
    if let Some(t) = token.filter(|t| !t.is_empty()) {
        rb = rb.bearer_auth(t);
    }
    if let Some(b) = body {
        rb = rb.json(b);
    }
    let resp = rb.send().await.map_err(|e| {
        let msg = if e.is_timeout() {
            "timed out".to_string()
        } else if e.is_connect() {
            "could not connect".to_string()
        } else {
            e.to_string()
        };
        AgentError { status: 0, message: msg }
    })?;
    let status = resp.status().as_u16();
    let text = resp.text().await.map_err(AgentError::transport)?;
    if !(200..300).contains(&status) {
        return Err(AgentError { status, message: error_message(&text) });
    }
    if text.trim().is_empty() {
        return Ok(Value::Null);
    }
    serde_json::from_str(&text).map_err(|e| AgentError { status, message: format!("bad response: {e}") })
}

/// GET /v1/identity (open). Returns None unless it is really an InternetMonitor agent.
pub async fn probe_identity(host: &str, port: u16, timeout: Duration) -> Option<Identity> {
    let v = request(host, port, None, "GET", "/v1/identity", None, timeout).await.ok()?;
    let id: Identity = serde_json::from_value(v).ok()?;
    id.is_agent().then_some(id)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn urls() {
        assert_eq!(base_url("10.0.0.2", 7878), "http://10.0.0.2:7878");
        assert_eq!(base_url("fe80::1", 7878), "http://[fe80::1]:7878");
        assert_eq!(base_url("[fe80::1]", 1), "http://[fe80::1]:1");
    }

    #[test]
    fn error_bodies() {
        assert_eq!(error_message(r#"{"error":"bad token"}"#), "bad token");
        assert_eq!(error_message(""), "request failed");
        assert_eq!(error_message("oops"), "oops");
    }

    #[test]
    fn identity_decode() {
        let j = r#"{"service":"internetmonitor-agent","apiVersion":1,"version":"1.0.0","nodeId":"abc",
            "hostname":"pi","nodeName":"","os":"linux","arch":"arm64","uptimeSec":5,"paired":true,"extra":1}"#;
        let id: Identity = serde_json::from_str(j).unwrap();
        assert!(id.is_agent());
        assert_eq!(id.display_name(), "pi");
        let other: Identity = serde_json::from_str(r#"{"service":"x","nodeId":"n"}"#).unwrap();
        assert!(!other.is_agent());
        assert_eq!(other.display_name(), "n");
    }
}
