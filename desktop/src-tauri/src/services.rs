//! Official status feeds of online services (../services/catalog.json) and desktop reachability probes.
//! Fetching happens here (not in the webview) to avoid CORS; parsing is split into pure functions so
//! every adapter is unit tested against small fixtures.

use serde::Serialize;
use serde_json::Value;
use std::sync::OnceLock;
use std::time::{Duration, Instant};

#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(rename_all = "lowercase")]
pub enum State {
    Operational,
    Maintenance,
    Unknown,
    Degraded,
    Outage,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct Incident {
    pub id: String,
    pub title: String,
    pub impact: String,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct Component {
    pub name: String,
    pub status: String,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Official {
    pub state: State,
    pub summary: String,
    pub incidents: Vec<Incident>,
    pub components: Vec<Component>,
    pub error: Option<String>,
}

impl Official {
    fn new(state: State, summary: impl Into<String>) -> Self {
        Official { state, summary: summary.into(), incidents: vec![], components: vec![], error: None }
    }
    pub fn failed(e: impl Into<String>) -> Self {
        let e = e.into();
        Official { state: State::Unknown, summary: format!("Status feed unavailable: {e}"), incidents: vec![], components: vec![], error: Some(e) }
    }
}

fn s(v: &Value, k: &str) -> String {
    v.get(k).and_then(|x| x.as_str()).unwrap_or("").trim().to_string()
}

fn incidents_summary(n: usize) -> String {
    match n {
        0 => "No active incidents".into(),
        1 => "1 active incident".into(),
        n => format!("{n} active incidents"),
    }
}

/// Atlassian Statuspage `/api/v2/summary.json`.
pub fn parse_statuspage(body: &[u8]) -> Result<Official, String> {
    let v: Value = serde_json::from_slice(body).map_err(|e| format!("bad JSON: {e}"))?;
    let st = v.get("status").ok_or("missing status")?;
    let state = match s(st, "indicator").as_str() {
        "none" => State::Operational,
        "minor" => State::Degraded,
        "major" | "critical" => State::Outage,
        "maintenance" => State::Maintenance,
        _ => State::Unknown,
    };
    let mut o = Official::new(state, s(st, "description"));
    for i in v.get("incidents").and_then(|x| x.as_array()).into_iter().flatten() {
        o.incidents.push(Incident { id: s(i, "id"), title: s(i, "name"), impact: s(i, "impact") });
    }
    for c in v.get("components").and_then(|x| x.as_array()).into_iter().flatten() {
        let status = s(c, "status");
        if !status.is_empty() && status != "operational" {
            o.components.push(Component { name: s(c, "name"), status });
        }
    }
    Ok(o)
}

/// Google Cloud / Google Workspace `incidents.json` (array of incidents; active = no `end`).
pub fn parse_gcp(body: &[u8]) -> Result<Official, String> {
    let v: Value = serde_json::from_slice(body).map_err(|e| format!("bad JSON: {e}"))?;
    let arr = v.as_array().ok_or("expected a JSON array")?;
    let mut o = Official::new(State::Operational, "");
    for i in arr {
        if i.get("end").is_some_and(|e| !e.is_null()) {
            continue;
        }
        let (st, impact) = match s(i, "status_impact").as_str() {
            "SERVICE_OUTAGE" => (State::Outage, "outage"),
            "SERVICE_DISRUPTION" => (State::Degraded, "disruption"),
            _ => continue, // SERVICE_INFORMATION and unknowns are informational
        };
        o.state = o.state.max(st);
        let title = format!("{}: {}", s(i, "service_name"), s(i, "external_desc")).trim().to_string();
        o.incidents.push(Incident { id: s(i, "id"), title, impact: impact.into() });
    }
    o.summary = incidents_summary(o.incidents.len());
    Ok(o)
}

/// UTF-16 (BOM-detected, big-endian by default as AWS serves it) or UTF-8 body → String.
pub fn decode_text(body: &[u8]) -> String {
    let utf16 = |be: bool, b: &[u8]| {
        let u: Vec<u16> = b.chunks_exact(2).map(|c| if be { u16::from_be_bytes([c[0], c[1]]) } else { u16::from_le_bytes([c[0], c[1]]) }).collect();
        String::from_utf16_lossy(&u)
    };
    match body {
        [0xFE, 0xFF, rest @ ..] => utf16(true, rest),
        [0xFF, 0xFE, rest @ ..] => utf16(false, rest),
        [0xEF, 0xBB, 0xBF, rest @ ..] => String::from_utf8_lossy(rest).into_owned(),
        // BOM-less UTF-16BE JSON starts with 00 '[' / 00 '{'
        [0, b'[' | b'{', ..] => utf16(true, body),
        _ => String::from_utf8_lossy(body).into_owned(),
    }
}

/// AWS Health `public/currentevents` (UTF-16BE JSON array). Status "2"/"3" → degraded (never outage:
/// long-running regional events would otherwise keep AWS red for weeks).
pub fn parse_aws(body: &[u8]) -> Result<Official, String> {
    let text = decode_text(body);
    let t = text.trim();
    let v: Value = if t.is_empty() { Value::Array(vec![]) } else { serde_json::from_str(t).map_err(|e| format!("bad JSON: {e}"))? };
    let arr = v.as_array().ok_or("expected a JSON array")?;
    let mut o = Official::new(State::Operational, "");
    for e in arr {
        let status = match e.get("status") {
            Some(Value::String(x)) => x.trim().parse::<i64>().unwrap_or(0),
            Some(Value::Number(n)) => n.as_i64().unwrap_or(0),
            _ => 0,
        };
        if status < 2 {
            continue;
        }
        o.state = State::Degraded;
        let title = format!("{}: {}", s(e, "region_name"), s(e, "summary")).trim().to_string();
        let impact = if status >= 3 { "disruption" } else { "performance" };
        o.incidents.push(Incident { id: s(e, "arn"), title, impact: impact.into() });
    }
    o.summary = incidents_summary(o.incidents.len());
    Ok(o)
}

fn xml_unescape(v: &str) -> String {
    let v = v.trim();
    let v = v.strip_prefix("<![CDATA[").and_then(|x| x.strip_suffix("]]>")).map(str::to_string).unwrap_or_else(|| {
        v.replace("&lt;", "<").replace("&gt;", ">").replace("&quot;", "\"").replace("&apos;", "'").replace("&#39;", "'").replace("&amp;", "&")
    });
    v.trim().to_string()
}

fn tag_text<'a>(xml: &'a str, tag: &str) -> Option<&'a str> {
    let open = format!("<{tag}");
    let mut from = 0;
    while let Some(p) = xml[from..].find(&open) {
        let start = from + p;
        let after = &xml[start + open.len()..];
        // must be exactly <tag> or <tag attr…>, not <tagother>
        if after.starts_with('>') || after.starts_with(' ') || after.starts_with('\t') || after.starts_with('\n') {
            let gt = after.find('>')?;
            let body = &after[gt + 1..];
            let end = body.find(&format!("</{tag}>"))?;
            return Some(&body[..end]);
        }
        from = start + open.len();
    }
    None
}

/// RSS feed (Azure): every `<item>` is an active issue.
pub fn parse_rss(body: &[u8]) -> Result<Official, String> {
    let xml = decode_text(body);
    if !xml.contains("<rss") && !xml.contains("<channel") {
        return Err("not an RSS feed".into());
    }
    let mut o = Official::new(State::Operational, "");
    let mut rest = xml.as_str();
    while let Some(p) = rest.find("<item") {
        let tail = &rest[p..];
        let Some(end) = tail.find("</item>") else { break };
        let item = &tail[..end];
        let title = tag_text(item, "title").map(xml_unescape).unwrap_or_else(|| "Active issue".into());
        let id = tag_text(item, "guid").or_else(|| tag_text(item, "link")).map(xml_unescape).unwrap_or_else(|| title.clone());
        o.incidents.push(Incident { id, title, impact: "issue".into() });
        rest = &tail[end + 7..];
    }
    if !o.incidents.is_empty() {
        o.state = State::Degraded;
    }
    o.summary = incidents_summary(o.incidents.len());
    Ok(o)
}

/// Slack `api/v2.0.0/current`.
pub fn parse_slack(body: &[u8]) -> Result<Official, String> {
    let v: Value = serde_json::from_slice(body).map_err(|e| format!("bad JSON: {e}"))?;
    let status = s(&v, "status");
    if status.is_empty() {
        return Err("missing status".into());
    }
    let mut o = Official::new(State::Operational, "");
    for i in v.get("active_incidents").and_then(|x| x.as_array()).into_iter().flatten() {
        let id = match i.get("id") {
            Some(Value::Number(n)) => n.to_string(),
            _ => s(i, "id"),
        };
        o.incidents.push(Incident { id, title: s(i, "title"), impact: s(i, "type") });
    }
    if status != "ok" {
        o.state = if o.incidents.iter().any(|i| i.impact == "outage") { State::Outage } else { State::Degraded };
    }
    o.summary = if o.state == State::Operational { "All systems operational".into() } else { incidents_summary(o.incidents.len()) };
    Ok(o)
}

/// Stripe `status.stripe.com/current`.
pub fn parse_stripe(body: &[u8]) -> Result<Official, String> {
    let v: Value = serde_json::from_slice(body).map_err(|e| format!("bad JSON: {e}"))?;
    let ls = s(&v, "largestatus");
    if ls.is_empty() {
        return Err("missing largestatus".into());
    }
    let state = match ls.as_str() {
        "up" => State::Operational,
        "down" => State::Outage,
        _ => State::Degraded,
    };
    Ok(Official::new(state, s(&v, "message")))
}

pub const ADAPTERS: &[&str] = &["statuspage", "gcp", "aws", "rss", "slack", "stripe", "none"];

pub fn parse(adapter: &str, body: &[u8]) -> Result<Official, String> {
    match adapter {
        "statuspage" => parse_statuspage(body),
        "gcp" => parse_gcp(body),
        "aws" => parse_aws(body),
        "rss" => parse_rss(body),
        "slack" => parse_slack(body),
        "stripe" => parse_stripe(body),
        "none" => Ok(Official::new(State::Unknown, "No public status feed — reachability only")),
        a => Err(format!("unknown adapter {a}")),
    }
}

fn client() -> &'static reqwest::Client {
    static C: OnceLock<reqwest::Client> = OnceLock::new();
    C.get_or_init(|| {
        reqwest::Client::builder()
            .redirect(reqwest::redirect::Policy::limited(10))
            .connect_timeout(Duration::from_secs(8))
            .pool_idle_timeout(Duration::from_secs(50))
            .user_agent("InternetMonitor")
            .build()
            .expect("http client")
    })
}

fn http_err(e: reqwest::Error) -> String {
    if e.is_timeout() {
        "timed out".into()
    } else if e.is_connect() {
        "could not connect".into()
    } else {
        e.to_string()
    }
}

/// Fetches and parses one official status feed (10 s timeout). Never fails: errors become state `unknown`.
pub async fn fetch_official(adapter: &str, url: Option<&str>) -> Official {
    if adapter == "none" {
        return parse("none", b"").unwrap_or_else(Official::failed);
    }
    let Some(url) = url.filter(|u| u.starts_with("https://") || u.starts_with("http://")) else {
        return Official::failed("no status URL");
    };
    let r = async {
        let resp = client().get(url).timeout(Duration::from_secs(10)).send().await.map_err(http_err)?;
        let code = resp.status();
        if !code.is_success() {
            return Err(format!("HTTP {}", code.as_u16()));
        }
        let body = resp.bytes().await.map_err(http_err)?;
        parse(adapter, &body)
    }
    .await;
    r.unwrap_or_else(Official::failed)
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ProbeResult {
    pub ok: bool,
    /// time until response headers arrived
    pub ms: Option<f64>,
    pub status: Option<u16>,
    pub error: Option<String>,
}

/// GET with redirects, 8 s timeout. Any HTTP response (403/404/421 too) means reachable.
pub async fn probe(url: &str) -> ProbeResult {
    if !(url.starts_with("https://") || url.starts_with("http://")) {
        return ProbeResult { ok: false, ms: None, status: None, error: Some("invalid URL".into()) };
    }
    let t0 = Instant::now();
    match client().get(url).timeout(Duration::from_secs(8)).send().await {
        Ok(resp) => {
            let ms = t0.elapsed().as_secs_f64() * 1000.0;
            let status = resp.status().as_u16();
            drop(resp); // headers are all we need
            ProbeResult { ok: true, ms: Some(ms), status: Some(status), error: None }
        }
        Err(e) => ProbeResult { ok: false, ms: None, status: None, error: Some(http_err(e)) },
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn statuspage() {
        let j = br#"{"page":{"id":"x"},"status":{"indicator":"minor","description":"Partially Degraded Service"},
            "components":[{"name":"Jira","status":"operational"},{"name":"Confluence","status":"degraded_performance"},{"name":"Trello","status":"partial_outage"}],
            "incidents":[{"id":"abc","name":"Slow page loads","impact":"minor","status":"investigating"}],"scheduled_maintenances":[]}"#;
        let o = parse_statuspage(j).unwrap();
        assert_eq!(o.state, State::Degraded);
        assert_eq!(o.summary, "Partially Degraded Service");
        assert_eq!(o.incidents, vec![Incident { id: "abc".into(), title: "Slow page loads".into(), impact: "minor".into() }]);
        assert_eq!(o.components.iter().map(|c| c.name.as_str()).collect::<Vec<_>>(), ["Confluence", "Trello"]);
        for (ind, st) in [("none", State::Operational), ("major", State::Outage), ("critical", State::Outage), ("maintenance", State::Maintenance), ("weird", State::Unknown)] {
            let b = format!(r#"{{"status":{{"indicator":"{ind}","description":"d"}}}}"#);
            assert_eq!(parse_statuspage(b.as_bytes()).unwrap().state, st, "{ind}");
        }
        assert!(parse_statuspage(b"<html>").is_err());
        assert!(parse_statuspage(b"{}").is_err());
    }

    #[test]
    fn gcp() {
        let j = br#"[
            {"id":"a","begin":"2026-11-01T00:00:00+00:00","end":"2026-11-01T02:00:00+00:00","service_name":"Compute","status_impact":"SERVICE_OUTAGE","external_desc":"old"},
            {"id":"b","begin":"2026-11-23T00:00:00+00:00","service_name":"Cloud SQL","status_impact":"SERVICE_DISRUPTION","external_desc":"  Elevated latency \n"},
            {"id":"c","begin":"2026-11-23T00:00:00+00:00","end":null,"service_name":"BigQuery","status_impact":"SERVICE_INFORMATION","external_desc":"FYI"}
        ]"#;
        let o = parse_gcp(j).unwrap();
        assert_eq!(o.state, State::Degraded);
        assert_eq!(o.incidents.len(), 1);
        assert_eq!(o.incidents[0].title, "Cloud SQL: Elevated latency");
        assert_eq!(o.summary, "1 active incident");
        let j2 = br#"[{"id":"d","end":null,"service_name":"Gmail","status_impact":"SERVICE_OUTAGE","external_desc":"Down"},
                      {"id":"e","service_name":"Drive","status_impact":"SERVICE_DISRUPTION","external_desc":"Slow"}]"#;
        let o2 = parse_gcp(j2).unwrap();
        assert_eq!(o2.state, State::Outage);
        assert_eq!(o2.summary, "2 active incidents");
        assert_eq!(parse_gcp(b"[]").unwrap().state, State::Operational);
        assert!(parse_gcp(b"{}").is_err());
    }

    fn utf16be(s: &str) -> Vec<u8> {
        let mut v = vec![0xFE, 0xFF];
        for u in s.encode_utf16() {
            v.extend_from_slice(&u.to_be_bytes());
        }
        v
    }

    #[test]
    fn aws() {
        let j = r#"[{"date":"1772369485","arn":"arn:aws:health:me-central-1::event/X","region_name":"UAE","status":"3","service":"multipleservices-me-central-1","service_name":"Multiple services","summary":"Region Availability"},
                    {"arn":"arn:2","region_name":"N. Virginia","status":"1","summary":"Informational"},
                    {"arn":"arn:3","region_name":"Ohio","status":"2","summary":"Increased latency — é"}]"#;
        let o = parse_aws(&utf16be(j)).unwrap();
        assert_eq!(o.state, State::Degraded);
        assert_eq!(o.incidents.len(), 2);
        assert_eq!(o.incidents[0].id, "arn:aws:health:me-central-1::event/X");
        assert_eq!(o.incidents[0].title, "UAE: Region Availability");
        assert_eq!(o.incidents[1].title, "Ohio: Increased latency — é");
        assert_eq!(parse_aws(&utf16be("[]")).unwrap().state, State::Operational);
        assert_eq!(parse_aws(&[0xFE, 0xFF]).unwrap().state, State::Operational); // BOM only (empty)
        assert_eq!(parse_aws(b"[]").unwrap().state, State::Operational); // plain UTF-8 works too
        let le: Vec<u8> = [0xFF, 0xFE].into_iter().chain("[]".encode_utf16().flat_map(|u| u.to_le_bytes())).collect();
        assert_eq!(decode_text(&le), "[]");
    }

    #[test]
    fn rss() {
        let empty = br#"<?xml version="1.0" encoding="utf-8"?><rss version="2.0"><channel><title>Azure Status</title><link>https://azure.status.microsoft/</link></channel></rss>"#;
        let o = parse_rss(empty).unwrap();
        assert_eq!(o.state, State::Operational);
        assert_eq!(o.summary, "No active incidents");
        let one = br#"<rss version="2.0"><channel><title>Azure Status</title>
            <item><guid isPermaLink="false">ABC-123</guid><title><![CDATA[Azure Front Door - Mitigated]]></title><link>https://x</link></item>
            <item><title>Storage &amp; Compute issues</title></item></channel></rss>"#;
        let o = parse_rss(one).unwrap();
        assert_eq!(o.state, State::Degraded);
        assert_eq!(o.incidents[0].id, "ABC-123");
        assert_eq!(o.incidents[0].title, "Azure Front Door - Mitigated");
        assert_eq!(o.incidents[1].title, "Storage & Compute issues");
        assert!(parse_rss(b"{}").is_err());
    }

    #[test]
    fn slack() {
        let ok = br#"{"status":"ok","date_created":"2026-11-11T07:24:01-08:00","active_incidents":[]}"#;
        assert_eq!(parse_slack(ok).unwrap().state, State::Operational);
        let inc = br#"{"status":"active","active_incidents":[{"id":123,"title":"Messages delayed","type":"incident"}]}"#;
        let o = parse_slack(inc).unwrap();
        assert_eq!(o.state, State::Degraded);
        assert_eq!(o.incidents[0].id, "123");
        let out = br#"{"status":"active","active_incidents":[{"id":"1","title":"a","type":"notice"},{"id":"2","title":"b","type":"outage"}]}"#;
        assert_eq!(parse_slack(out).unwrap().state, State::Outage);
        assert!(parse_slack(b"{}").is_err());
    }

    #[test]
    fn stripe() {
        let up = br#"{"statuses":{"api":"up"},"largestatus":"up","message":"All services are online."}"#;
        let o = parse_stripe(up).unwrap();
        assert_eq!((o.state, o.summary.as_str()), (State::Operational, "All services are online."));
        assert_eq!(parse_stripe(br#"{"largestatus":"down","message":"m"}"#).unwrap().state, State::Outage);
        assert_eq!(parse_stripe(br#"{"largestatus":"delayed","message":"m"}"#).unwrap().state, State::Degraded);
        assert!(parse_stripe(b"{}").is_err());
    }

    #[test]
    fn none_and_unknown() {
        let o = parse("none", b"").unwrap();
        assert_eq!(o.state, State::Unknown);
        assert_eq!(o.summary, "No public status feed — reachability only");
        assert!(parse("bogus", b"").is_err());
        assert_eq!(serde_json::to_value(State::Outage).unwrap(), "outage");
    }

    #[test]
    fn catalog_adapters_supported() {
        let cat: Value = serde_json::from_str(include_str!("../../../services/catalog.json")).unwrap();
        for svc in cat["services"].as_array().unwrap() {
            let a = svc["adapter"].as_str().unwrap();
            assert!(ADAPTERS.contains(&a), "{a}");
            assert!(a == "none" || svc["statusUrl"].is_string(), "{}", svc["id"]);
        }
    }
}
