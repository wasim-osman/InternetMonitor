//! Integration tests against a real running agent. Ignored by default; run with e.g.
//!   imonitord --listen 127.0.0.1:7899 --state-dir /tmp/st &
//!   IM_TEST_AGENT=127.0.0.1:7899 cargo test --test live_agent -- --ignored --nocapture

use internet_monitor_lib::{agent, discovery, icmp, ssh, updater};
use serde_json::json;
use std::time::Duration;

fn target() -> Option<(String, u16)> {
    let v = std::env::var("IM_TEST_AGENT").ok()?;
    let (h, p) = v.rsplit_once(':')?;
    Some((h.to_string(), p.parse().ok()?))
}

#[tokio::test]
#[ignore]
async fn pair_and_read_everything() {
    let (host, port) = target().expect("set IM_TEST_AGENT=host:port");
    let t = Duration::from_secs(8);
    let id = agent::probe_identity(&host, port, t).await.expect("identity");
    println!("identity: {} v{:?} node {}", id.display_name(), id.version, id.node_id);

    // pairing: loopback-only code → /v1/pair
    let code = agent::request(&host, port, None, "POST", "/v1/local/pair-code", None, t).await.expect("pair-code");
    let code = code["code"].as_str().unwrap().to_string();
    let bad = agent::request(&host, port, None, "POST", "/v1/pair", Some(&json!({"code":"000000","clientName":"test"})), t).await;
    assert_eq!(bad.unwrap_err().status, 401);
    let pr = agent::request(&host, port, None, "POST", "/v1/pair", Some(&json!({"code": code, "clientName":"desktop-test"})), t)
        .await
        .expect("pair");
    let token = pr["token"].as_str().unwrap().to_string();
    assert_eq!(token.len(), 64);

    // unauthorised access is rejected with 401
    let e = agent::request(&host, port, Some("nope"), "GET", "/v1/config", None, t).await.unwrap_err();
    assert_eq!(e.status, 401);

    let tok = Some(token.as_str());
    let mut cfg = agent::request(&host, port, tok, "GET", "/v1/config", None, t).await.expect("config");
    println!("config: interval {} retention {}d targets {}", cfg["intervalMs"], cfg["retentionRollupDays"], cfg["targets"].as_array().unwrap().len());
    cfg["retentionRollupDays"] = json!(60);
    let saved = agent::request(&host, port, tok, "PUT", "/v1/config", Some(&cfg), t).await.expect("put config");
    assert_eq!(saved["retentionRollupDays"], 60);

    let live = agent::request(&host, port, tok, "GET", "/v1/live", None, t).await.expect("live");
    println!("live: {} targets", live["targets"].as_array().unwrap().len());
    let ids: Vec<String> = cfg["targets"].as_array().unwrap().iter().map(|x| x["id"].as_str().unwrap().to_string()).collect();
    let now = live["now"].as_i64().unwrap();
    let series = agent::request(&host, port, tok, "GET", &format!("/v1/series?target={}&from={}&to={}&points=500", ids.join(","), now - 3600, now), None, Duration::from_secs(15))
        .await
        .expect("series");
    for s in series["series"].as_array().unwrap() {
        println!("series {}: {} points step {}", s["target"], s["points"].as_array().unwrap().len(), s["stepSec"]);
    }
    for p in ["/v1/summary", "/v1/incidents?limit=10", "/v1/system", "/v1/clients", "/v1/speedtest/history"] {
        agent::request(&host, port, tok, "GET", p, None, Duration::from_secs(15)).await.unwrap_or_else(|e| panic!("{p}: {e:?}"));
    }
    let pc = agent::request(&host, port, tok, "POST", "/v1/pair-code", None, t).await.expect("authed pair-code");
    assert_eq!(pc["code"].as_str().unwrap().len(), 6);
    println!("token for manual runs: {token}");
}

#[tokio::test]
#[ignore]
async fn icmp_and_interfaces() {
    let ifs = discovery::local_interfaces();
    println!("interfaces: {ifs:?}");
    let r = icmp::sweep(vec![std::net::Ipv4Addr::LOCALHOST], Duration::from_millis(800)).await;
    println!("icmp localhost: {r:?}");
    let ts = discovery::tailscale().await;
    println!("tailscale: {ts:?}");
    println!("ssh users: {:?}", ssh::guess_users(None));
}

#[tokio::test]
#[ignore]
async fn full_scan() {
    let res = discovery::scan(|p| println!("progress: {p}"), |l| println!("update: {} candidates", l.len())).await;
    for c in &res {
        println!("{:<16} {:<28} {:<22} ping {:?} agent {:?} extra {:?}", c.ip, c.name, c.network, c.ping_ms, c.agent.as_ref().map(|a| &a.node_id), c.extra_ips);
    }
}

#[tokio::test]
#[ignore]
async fn github_latest_release() {
    match updater::check("0.0.1").await {
        Ok(i) => println!("latest {} available {} mode {} asset {:?}", i.latest, i.available, i.mode, i.asset),
        Err(e) => println!("update check: {e}"),
    }
}
