//! Every catalog status feed + probe URL against the real internet. Ignored by default:
//!   cargo test --test live_services -- --ignored --nocapture

use internet_monitor_lib::services::{self, State};
use serde_json::Value;

#[tokio::test]
#[ignore]
async fn all_catalog_feeds_and_probes() {
    let cat: Value = serde_json::from_str(include_str!("../../../services/catalog.json")).unwrap();
    let list = cat["services"].as_array().unwrap().clone();
    let tasks = list.into_iter().map(|s| async move {
        let id = s["id"].as_str().unwrap().to_string();
        let adapter = s["adapter"].as_str().unwrap().to_string();
        let o = services::fetch_official(&adapter, s["statusUrl"].as_str()).await;
        let p = services::probe(s["probeUrl"].as_str().unwrap()).await;
        (id, adapter, o, p)
    });
    let mut feed_failures = vec![];
    let mut probe_failures = vec![];
    for (id, adapter, o, p) in futures_join(tasks).await {
        println!("{id:14} {adapter:10} {:12} {:50} probe ok={} {:?}ms status={:?} {:?}", format!("{:?}", o.state), o.summary.chars().take(50).collect::<String>(), p.ok, p.ms.map(|m| m as i64), p.status, p.error);
        if adapter != "none" && (o.state == State::Unknown || o.error.is_some()) {
            feed_failures.push(id.clone());
        }
        if !p.ok {
            probe_failures.push(id);
        }
    }
    assert!(feed_failures.is_empty(), "feeds failed: {feed_failures:?}");
    assert!(probe_failures.is_empty(), "probes failed: {probe_failures:?}");
}

async fn futures_join<F: std::future::Future + Send + 'static>(fs: impl Iterator<Item = F>) -> Vec<F::Output>
where
    F::Output: Send + 'static,
{
    let hs: Vec<_> = fs.map(tokio::spawn).collect();
    let mut out = vec![];
    for h in hs {
        out.push(h.await.unwrap());
    }
    out
}
