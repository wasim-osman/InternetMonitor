//! Network discovery: local interfaces, subnet sweeps, Tailscale peers, agent identity probes,
//! reverse DNS and de-duplication by agent nodeId. Mirrors app/Sources/IMCore/Discovery.swift.

use crate::agent::{probe_identity, Identity, AGENT_PORT};
use crate::icmp;
use crate::util;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::{BTreeMap, HashMap, HashSet};
use std::net::{IpAddr, Ipv4Addr};
use std::sync::Arc;
use std::time::Duration;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct LocalIface {
    pub name: String,
    pub ip: String,
    pub prefix: u8,
    pub is_tailscale: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Candidate {
    pub ip: String,
    pub name: String,
    pub network: String,
    pub is_tailscale: bool,
    pub ping_ms: Option<f64>,
    pub agent: Option<Identity>,
    #[serde(default)]
    pub extra_ips: Vec<String>,
    pub os: Option<String>,
}

#[derive(Debug, Clone, PartialEq)]
pub struct TsPeer {
    pub name: String,
    pub ips: Vec<String>,
    pub online: bool,
    pub os: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Default)]
pub struct TsSnapshot {
    pub self_ips: Vec<String>,
    pub peers: Vec<TsPeer>,
}

pub fn ipv4_to_u32(s: &str) -> Option<u32> {
    s.parse::<Ipv4Addr>().ok().map(u32::from)
}

pub fn is_tailscale_ip(ip: &str) -> bool {
    ipv4_to_u32(ip).is_some_and(|v| v & 0xFFC0_0000 == 0x6440_0000) // 100.64.0.0/10
}

pub fn mask_to_prefix(mask: Ipv4Addr) -> u8 {
    u32::from(mask).count_ones() as u8
}

/// Host addresses of the subnet to sweep (excluding network/broadcast and `ip` itself).
/// Subnets larger than /22 are clamped to the /24 around the address.
pub fn sweep_addresses(ip: &str, prefix: u8) -> Vec<Ipv4Addr> {
    let Some(me) = ipv4_to_u32(ip) else { return vec![] };
    let p = if prefix >= 22 { prefix } else { 24 };
    if p >= 31 {
        return vec![];
    }
    let mask: u32 = u32::MAX << (32 - u32::from(p));
    let net = me & mask;
    let bcast = net | !mask;
    if bcast <= net + 1 {
        return vec![];
    }
    ((net + 1)..bcast).filter(|&v| v != me).map(Ipv4Addr::from).collect()
}

/// Active, non-loopback, non-link-local IPv4 interfaces.
pub fn local_interfaces() -> Vec<LocalIface> {
    let Ok(list) = if_addrs::get_if_addrs() else { return vec![] };
    let mut out = vec![];
    for i in list {
        if i.is_loopback() {
            continue;
        }
        if let if_addrs::IfAddr::V4(v4) = &i.addr {
            let ip = v4.ip;
            if ip.is_link_local() || ip.is_loopback() || ip.is_unspecified() {
                continue;
            }
            let s = ip.to_string();
            out.push(LocalIface { name: i.name.clone(), is_tailscale: is_tailscale_ip(&s), prefix: mask_to_prefix(v4.netmask), ip: s });
        }
    }
    out
}

pub fn tailscale_binary() -> Option<std::path::PathBuf> {
    let home = std::env::var("HOME").unwrap_or_default();
    let hb = format!("{home}/.homebrew/bin/tailscale");
    util::find_executable(
        "tailscale",
        &[
            "/Applications/Tailscale.app/Contents/MacOS/Tailscale",
            "/opt/homebrew/bin/tailscale",
            "/usr/local/bin/tailscale",
            "/usr/bin/tailscale",
            "/usr/sbin/tailscale",
            "/snap/bin/tailscale",
            r"C:\Program Files\Tailscale\tailscale.exe",
            r"C:\Program Files (x86)\Tailscale\tailscale.exe",
            &hb,
        ],
    )
}

fn v4_list(v: Option<&Value>) -> Vec<String> {
    v.and_then(Value::as_array)
        .map(|a| a.iter().filter_map(Value::as_str).filter(|s| s.parse::<Ipv4Addr>().is_ok()).map(str::to_string).collect())
        .unwrap_or_default()
}

/// Parses `tailscale status --json`.
pub fn parse_tailscale(json: &str) -> Option<TsSnapshot> {
    let v: Value = serde_json::from_str(json).ok()?;
    let self_ips = v4_list(v.get("Self").and_then(|s| s.get("TailscaleIPs")));
    let mut peers = vec![];
    if let Some(map) = v.get("Peer").and_then(Value::as_object) {
        for p in map.values() {
            let dns_first = p
                .get("DNSName")
                .and_then(Value::as_str)
                .and_then(|d| d.split('.').next())
                .unwrap_or("")
                .to_string();
            let mut name = p.get("HostName").and_then(Value::as_str).unwrap_or("").to_string();
            if name.is_empty() || name == "localhost" {
                name = dns_first;
            }
            peers.push(TsPeer {
                name,
                ips: v4_list(p.get("TailscaleIPs")),
                online: p.get("Online").and_then(Value::as_bool).unwrap_or(false),
                os: p.get("OS").and_then(Value::as_str).filter(|s| !s.is_empty()).map(str::to_string),
            });
        }
    }
    peers.sort_by(|a, b| a.name.cmp(&b.name));
    Some(TsSnapshot { self_ips, peers })
}

pub async fn tailscale() -> Option<TsSnapshot> {
    let bin = tailscale_binary()?;
    let r = util::run(&bin, &["status".into(), "--json".into()], Duration::from_secs(5), None).await;
    if !r.ok() && r.stdout.trim().is_empty() {
        return None;
    }
    parse_tailscale(&r.stdout)
}

/// Reverse DNS with a short-name cleanup for `.local` / `.lan` names. Blocking-safe wrapper.
pub async fn reverse_dns(ip: Ipv4Addr) -> Option<String> {
    let fut = tokio::task::spawn_blocking(move || dns_lookup::lookup_addr(&IpAddr::V4(ip)).ok());
    let name = tokio::time::timeout(Duration::from_millis(2500), fut).await.ok()?.ok()??;
    clean_rdns(&name, &ip.to_string())
}

pub fn clean_rdns(name: &str, ip: &str) -> Option<String> {
    let n = name.trim_end_matches('.');
    if n.is_empty() || n == ip {
        return None;
    }
    let lower = n.to_ascii_lowercase();
    if lower.ends_with(".local") || lower.ends_with(".lan") || lower.ends_with(".home") || lower.ends_with(".localdomain") {
        return n.split('.').next().map(str::to_string);
    }
    Some(n.to_string())
}

fn sort_key(ip: &str) -> u32 {
    ipv4_to_u32(ip).unwrap_or(0)
}

/// Merges candidates that are the same agent (same nodeId) reachable on several addresses.
/// The LAN address becomes primary; the others are listed in `extra_ips`.
pub fn dedupe(list: Vec<Candidate>) -> Vec<Candidate> {
    let mut result: Vec<Candidate> = vec![];
    let mut groups: BTreeMap<String, Vec<Candidate>> = BTreeMap::new();
    for c in list {
        match c.agent.as_ref().map(|a| a.node_id.clone()) {
            Some(id) => groups.entry(id).or_default().push(c),
            None => result.push(c),
        }
    }
    for (_, mut g) in groups {
        g.sort_by_key(|c| (c.is_tailscale, sort_key(&c.ip)));
        let mut primary = g.remove(0);
        let mut extras: Vec<String> = primary.extra_ips.clone();
        for o in &g {
            if !extras.contains(&o.ip) {
                extras.push(o.ip.clone());
            }
            if primary.ping_ms.is_none() {
                primary.ping_ms = o.ping_ms;
            }
            if primary.os.is_none() {
                primary.os.clone_from(&o.os);
            }
        }
        if !primary.is_tailscale && g.iter().any(|o| o.is_tailscale) {
            primary.network = format!("{} + Tailscale", primary.network);
        }
        primary.extra_ips = extras;
        result.push(primary);
    }
    result.sort_by_key(|c| sort_key(&c.ip));
    result
}

/// Full scan. `progress` receives status strings, `update` the growing candidate list.
/// This machine is never included (all local and own-Tailscale addresses are excluded).
pub async fn scan(
    progress: impl Fn(String) + Send + Sync + 'static,
    update: impl Fn(Vec<Candidate>) + Send + Sync + 'static,
) -> Vec<Candidate> {
    let progress = Arc::new(progress);
    let ifaces = local_interfaces();
    progress("Checking Tailscale…".into());
    let ts = tailscale().await;
    let mut self_ips: HashSet<String> = ifaces.iter().map(|i| i.ip.clone()).collect();
    if let Some(t) = &ts {
        self_ips.extend(t.self_ips.iter().cloned());
    }
    self_ips.insert("127.0.0.1".into());

    let mut found: HashMap<String, Candidate> = HashMap::new();
    let publish = |found: &HashMap<String, Candidate>| {
        let mut v: Vec<Candidate> = found.values().cloned().collect();
        v.sort_by_key(|c| sort_key(&c.ip));
        update(v);
    };

    // 1. Tailscale peers (online only)
    if let Some(t) = &ts {
        let peers: Vec<(TsPeer, String)> = t
            .peers
            .iter()
            .filter(|p| p.online)
            .filter_map(|p| p.ips.first().map(|ip| (p.clone(), ip.clone())))
            .filter(|(_, ip)| !self_ips.contains(ip))
            .collect();
        if !peers.is_empty() {
            progress(format!("Probing {} Tailscale peers…", peers.len()));
            let ips: Vec<Ipv4Addr> = peers.iter().filter_map(|(_, ip)| ip.parse().ok()).collect();
            let (pings, _) = icmp::liveness(ips, Duration::from_millis(1500)).await;
            let mut set = tokio::task::JoinSet::new();
            for (p, ip) in peers {
                set.spawn(async move {
                    let id = probe_identity(&ip, AGENT_PORT, Duration::from_millis(1500)).await;
                    (p, ip, id)
                });
            }
            while let Some(Ok((p, ip, id))) = set.join_next().await {
                let ping = ip.parse::<Ipv4Addr>().ok().and_then(|a| pings.get(&a).copied());
                let name = id.as_ref().map(Identity::display_name).unwrap_or_else(|| p.name.clone());
                found.insert(
                    ip.clone(),
                    Candidate { ip, name, network: "Tailscale".into(), is_tailscale: true, ping_ms: ping, agent: id, extra_ips: vec![], os: p.os },
                );
            }
            publish(&found);
        }
    }

    // 2. LAN sweeps on every non-Tailscale interface
    let mut seen_subnets: HashSet<(u32, usize)> = HashSet::new();
    for itf in ifaces.iter().filter(|i| !i.is_tailscale) {
        let targets: Vec<Ipv4Addr> =
            sweep_addresses(&itf.ip, itf.prefix).into_iter().filter(|a| !self_ips.contains(&a.to_string())).collect();
        let Some(first) = targets.first() else { continue };
        if !seen_subnets.insert((u32::from(*first), targets.len())) {
            continue;
        }
        progress(format!("Sweeping {} ({}/{})…", itf.name, itf.ip, itf.prefix));
        let (alive, method) = icmp::liveness(targets.clone(), Duration::from_millis(1200)).await;
        if method == "tcp" {
            progress(format!("Sweeping {} via TCP (ICMP unavailable)…", itf.name));
        }
        // Agent port on every address: some hosts drop ICMP but run the agent.
        let sem = Arc::new(tokio::sync::Semaphore::new(64));
        let mut set = tokio::task::JoinSet::new();
        for a in &targets {
            let ip = a.to_string();
            let sem = sem.clone();
            set.spawn(async move {
                let _p = sem.acquire_owned().await.ok()?;
                probe_identity(&ip, AGENT_PORT, Duration::from_millis(800)).await.map(|id| (ip, id))
            });
        }
        let mut agents: HashMap<String, Identity> = HashMap::new();
        while let Some(r) = set.join_next().await {
            if let Ok(Some((ip, id))) = r {
                agents.insert(ip, id);
            }
        }
        let mut live: Vec<String> = alive.keys().map(|a| a.to_string()).collect();
        live.extend(agents.keys().cloned());
        live.sort_by_key(|s| sort_key(s));
        live.dedup();
        let net = format!("LAN {}", itf.name);
        let mut rdns_set = tokio::task::JoinSet::new();
        for ip in live {
            if found.contains_key(&ip) || self_ips.contains(&ip) {
                continue;
            }
            let addr: Ipv4Addr = ip.parse().unwrap_or(Ipv4Addr::UNSPECIFIED);
            let agent = agents.get(&ip).cloned();
            let c = Candidate {
                name: agent.as_ref().map(Identity::display_name).unwrap_or_else(|| ip.clone()),
                ip: ip.clone(),
                network: net.clone(),
                is_tailscale: false,
                ping_ms: alive.get(&addr).copied(),
                os: agent.as_ref().and_then(|a| a.os.clone()),
                agent,
                extra_ips: vec![],
            };
            if c.agent.is_none() {
                rdns_set.spawn(async move { (ip, reverse_dns(addr).await) });
            }
            found.insert(c.ip.clone(), c);
        }
        publish(&found);
        if !rdns_set.is_empty() {
            progress(format!("Resolving names on {}…", itf.name));
        }
        while let Some(r) = rdns_set.join_next().await {
            if let Ok((ip, Some(name))) = r {
                if let Some(c) = found.get_mut(&ip) {
                    c.name = name;
                }
            }
        }
        publish(&found);
    }

    let result = dedupe(found.into_values().collect());
    update(result.clone());
    progress(format!("Scan complete — {} machine{} found", result.len(), if result.len() == 1 { "" } else { "s" }));
    result
}

/// Probe a single host given by the user (IP or hostname). Returns None if nothing answers.
pub async fn probe_host(host: &str) -> Result<Candidate, String> {
    let host = host.trim();
    let ip: Ipv4Addr = match host.parse() {
        Ok(ip) => ip,
        Err(_) => tokio::net::lookup_host((host, 0))
            .await
            .map_err(|e| format!("Cannot resolve {host}: {e}"))?
            .find_map(|a| match a.ip() {
                IpAddr::V4(v) => Some(v),
                IpAddr::V6(_) => None,
            })
            .ok_or_else(|| format!("{host} has no IPv4 address"))?,
    };
    let ip_s = ip.to_string();
    let self_ips: HashSet<String> = local_interfaces().into_iter().map(|i| i.ip).collect();
    if self_ips.contains(&ip_s) || ip.is_loopback() {
        return Err("That is this machine — only other machines can be monitored.".into());
    }
    let (id, (pings, _)) = tokio::join!(
        probe_identity(&ip_s, AGENT_PORT, Duration::from_millis(2500)),
        icmp::liveness(vec![ip], Duration::from_millis(1500))
    );
    let ping = pings.get(&ip).copied();
    if id.is_none() && ping.is_none() {
        return Err(format!("{host} did not answer ping or the agent port ({AGENT_PORT})."));
    }
    let ts = is_tailscale_ip(&ip_s);
    let mut name = id.as_ref().map(Identity::display_name).unwrap_or_else(|| host.to_string());
    if id.is_none() && name == ip_s {
        if let Some(rd) = reverse_dns(ip).await {
            name = rd;
        }
    }
    Ok(Candidate {
        ip: ip_s,
        name,
        network: if ts { "Tailscale".into() } else { "Manual".into() },
        is_tailscale: ts,
        ping_ms: ping,
        os: id.as_ref().and_then(|a| a.os.clone()),
        agent: id,
        extra_ips: vec![],
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ident(node: &str) -> Identity {
        serde_json::from_value(serde_json::json!({"service":"internetmonitor-agent","nodeId":node,"hostname":"h"})).unwrap()
    }
    fn cand(ip: &str, ts: bool, agent: Option<&str>) -> Candidate {
        Candidate {
            ip: ip.into(),
            name: ip.into(),
            network: if ts { "Tailscale".into() } else { "LAN en0".into() },
            is_tailscale: ts,
            ping_ms: if ts { Some(30.0) } else { None },
            agent: agent.map(ident),
            extra_ips: vec![],
            os: None,
        }
    }

    #[test]
    fn tailscale_range() {
        assert!(is_tailscale_ip("100.64.0.1"));
        assert!(is_tailscale_ip("100.127.255.254"));
        assert!(!is_tailscale_ip("100.128.0.1"));
        assert!(!is_tailscale_ip("192.168.1.1"));
        assert!(!is_tailscale_ip("nonsense"));
    }

    #[test]
    fn prefixes() {
        assert_eq!(mask_to_prefix(Ipv4Addr::new(255, 255, 255, 0)), 24);
        assert_eq!(mask_to_prefix(Ipv4Addr::new(255, 255, 252, 0)), 22);
        assert_eq!(mask_to_prefix(Ipv4Addr::new(255, 0, 0, 0)), 8);
    }

    #[test]
    fn sweep_ranges() {
        let a = sweep_addresses("192.168.1.10", 24);
        assert_eq!(a.len(), 253);
        assert_eq!(a.first(), Some(&Ipv4Addr::new(192, 168, 1, 1)));
        assert_eq!(a.last(), Some(&Ipv4Addr::new(192, 168, 1, 254)));
        assert!(!a.contains(&Ipv4Addr::new(192, 168, 1, 10)));
        // /22 is swept fully (1022 hosts minus self)
        assert_eq!(sweep_addresses("10.0.5.7", 22).len(), 1021);
        // /16 is clamped to the /24 around us
        let b = sweep_addresses("10.1.2.3", 16);
        assert_eq!(b.len(), 253);
        assert!(b.iter().all(|x| x.octets()[2] == 2));
        assert!(sweep_addresses("10.0.0.1", 31).is_empty());
        assert!(sweep_addresses("10.0.0.1", 32).is_empty());
        assert_eq!(sweep_addresses("10.0.0.1", 30), vec![Ipv4Addr::new(10, 0, 0, 2)]);
    }

    #[test]
    fn tailscale_json() {
        let j = r#"{"Self":{"TailscaleIPs":["100.100.1.1","fd7a:115c::1"]},
          "Peer":{"k1":{"HostName":"raspberrypi","DNSName":"raspberrypi.tail1.ts.net.","TailscaleIPs":["100.100.0.2","fd7a::2"],"Online":true,"OS":"linux"},
                  "k2":{"HostName":"localhost","DNSName":"phone.tail1.ts.net.","TailscaleIPs":["100.90.0.3"],"Online":false,"OS":"iOS"}}}"#;
        let s = parse_tailscale(j).unwrap();
        assert_eq!(s.self_ips, vec!["100.100.1.1"]);
        assert_eq!(s.peers.len(), 2);
        let pi = s.peers.iter().find(|p| p.name == "raspberrypi").unwrap();
        assert_eq!(pi.ips, vec!["100.100.0.2"]);
        assert!(pi.online);
        let ph = s.peers.iter().find(|p| p.name == "phone").unwrap();
        assert!(!ph.online);
        assert_eq!(ph.os.as_deref(), Some("iOS"));
        assert!(parse_tailscale("not json").is_none());
    }

    #[test]
    fn dedupe_by_node_id() {
        let list = vec![
            cand("100.100.0.2", true, Some("node-a")),
            cand("192.168.1.20", false, Some("node-a")),
            cand("192.168.1.5", false, None),
            cand("192.168.1.30", false, Some("node-b")),
            cand("100.70.0.9", true, Some("node-c")),
        ];
        let out = dedupe(list);
        assert_eq!(out.len(), 4);
        let a = out.iter().find(|c| c.agent.as_ref().map(|x| x.node_id.as_str()) == Some("node-a")).unwrap();
        assert_eq!(a.ip, "192.168.1.20", "LAN address is primary");
        assert_eq!(a.extra_ips, vec!["100.100.0.2"]);
        assert_eq!(a.network, "LAN en0 + Tailscale");
        assert_eq!(a.ping_ms, Some(30.0), "ping borrowed from the other address");
        let c = out.iter().find(|c| c.ip == "100.70.0.9").unwrap();
        assert!(c.extra_ips.is_empty());
        // sorted by numeric IP
        let ips: Vec<&str> = out.iter().map(|c| c.ip.as_str()).collect();
        assert_eq!(ips, vec!["100.70.0.9", "192.168.1.5", "192.168.1.20", "192.168.1.30"]);
    }

    #[test]
    fn rdns_cleanup() {
        assert_eq!(clean_rdns("raspberrypi.local.", "1.2.3.4").as_deref(), Some("raspberrypi"));
        assert_eq!(clean_rdns("nas.lan", "1.2.3.4").as_deref(), Some("nas"));
        assert_eq!(clean_rdns("host.example.com", "1.2.3.4").as_deref(), Some("host.example.com"));
        assert_eq!(clean_rdns("1.2.3.4", "1.2.3.4"), None);
    }
}
