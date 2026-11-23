//! Unprivileged host liveness probing.
//!
//! * Linux/macOS: ICMP datagram sockets (`SOCK_DGRAM` + `IPPROTO_ICMP`). On Linux these need the
//!   user's group inside `net.ipv4.ping_group_range` (default on most desktop distros).
//! * Windows: `IcmpSendEcho` from iphlpapi (no admin rights needed).
//! * Fallback (ICMP unavailable): TCP connect probing of common ports — a refused connection
//!   (RST) proves the host is alive just as well as an accepted one.

use std::collections::HashMap;
use std::net::{Ipv4Addr, SocketAddr};
use std::sync::Arc;
use std::time::{Duration, Instant};

/// Ports used by the TCP fallback sweep (agent port first).
pub const FALLBACK_PORTS: [u16; 9] = [7878, 22, 80, 443, 445, 139, 3389, 8080, 53];

#[derive(Debug)]
pub struct Unavailable(pub String);

/// Internet checksum (RFC 1071).
pub fn checksum(data: &[u8]) -> u16 {
    let mut sum: u32 = 0;
    let mut chunks = data.chunks_exact(2);
    for c in &mut chunks {
        sum += u32::from(u16::from_be_bytes([c[0], c[1]]));
    }
    if let [b] = chunks.remainder() {
        sum += u32::from(*b) << 8;
    }
    while sum >> 16 != 0 {
        sum = (sum & 0xFFFF) + (sum >> 16);
    }
    !(sum as u16)
}

pub fn echo_request(ident: u16, seq: u16) -> Vec<u8> {
    let mut pkt = vec![8u8, 0, 0, 0];
    pkt.extend_from_slice(&ident.to_be_bytes());
    pkt.extend_from_slice(&seq.to_be_bytes());
    pkt.extend_from_slice(b"InternetMon");
    let ck = checksum(&pkt);
    pkt[2..4].copy_from_slice(&ck.to_be_bytes());
    pkt
}

/// True when `buf` (as received on a datagram ICMP socket) is an echo reply. macOS prepends the
/// IPv4 header, Linux does not.
pub fn is_echo_reply(buf: &[u8]) -> bool {
    let icmp = if !buf.is_empty() && buf[0] >> 4 == 4 {
        let ihl = usize::from(buf[0] & 0x0F) * 4;
        if buf.len() <= ihl {
            return false;
        }
        &buf[ihl..]
    } else {
        buf
    };
    icmp.first() == Some(&0)
}

/// Pings every address once; returns RTT (ms) for those that answered.
pub async fn sweep(ips: Vec<Ipv4Addr>, timeout: Duration) -> Result<HashMap<Ipv4Addr, f64>, Unavailable> {
    if ips.is_empty() {
        return Ok(HashMap::new());
    }
    platform_sweep(ips, timeout).await
}

#[cfg(unix)]
async fn platform_sweep(ips: Vec<Ipv4Addr>, timeout: Duration) -> Result<HashMap<Ipv4Addr, f64>, Unavailable> {
    tokio::task::spawn_blocking(move || dgram_sweep_blocking(&ips, timeout))
        .await
        .map_err(|e| Unavailable(e.to_string()))?
}

#[cfg(unix)]
fn dgram_sweep_blocking(ips: &[Ipv4Addr], timeout: Duration) -> Result<HashMap<Ipv4Addr, f64>, Unavailable> {
    use socket2::{Domain, Protocol, SockAddr, Socket, Type};
    use std::mem::MaybeUninit;

    let sock = Socket::new(Domain::IPV4, Type::DGRAM, Some(Protocol::ICMPV4))
        .map_err(|e| Unavailable(format!("ICMP socket unavailable: {e}")))?;
    let _ = sock.set_recv_buffer_size(1 << 20);
    sock.set_read_timeout(Some(Duration::from_millis(10))).map_err(|e| Unavailable(e.to_string()))?;

    let ident = (std::process::id() & 0xFFFF) as u16;
    let mut sent: HashMap<Ipv4Addr, Instant> = HashMap::new();
    let mut results: HashMap<Ipv4Addr, f64> = HashMap::new();
    let mut buf = [MaybeUninit::<u8>::uninit(); 1500];

    let mut drain = |sent: &HashMap<Ipv4Addr, Instant>, results: &mut HashMap<Ipv4Addr, f64>| {
        while let Ok((n, from)) = sock.recv_from(&mut buf) {
            // SAFETY: recv_from initialised the first `n` bytes.
            let data: Vec<u8> = buf[..n].iter().map(|b| unsafe { b.assume_init() }).collect();
            if !is_echo_reply(&data) {
                continue;
            }
            if let Some(SocketAddr::V4(a)) = from.as_socket() {
                let ip = *a.ip();
                if let (Some(t), None) = (sent.get(&ip), results.get(&ip)) {
                    results.insert(ip, t.elapsed().as_secs_f64() * 1000.0);
                }
            }
        }
    };

    let mut seq: u16 = 1;
    for chunk in ips.chunks(32) {
        for ip in chunk {
            let pkt = echo_request(ident, seq);
            seq = seq.wrapping_add(1);
            let addr = SockAddr::from(SocketAddr::from((*ip, 0)));
            sent.insert(*ip, Instant::now());
            let _ = sock.send_to(&pkt, &addr);
        }
        drain(&sent, &mut results);
        std::thread::sleep(Duration::from_millis(8));
    }
    let deadline = Instant::now() + timeout;
    while Instant::now() < deadline && results.len() < ips.len() {
        drain(&sent, &mut results);
    }
    drain(&sent, &mut results);
    Ok(results)
}

#[cfg(windows)]
async fn platform_sweep(ips: Vec<Ipv4Addr>, timeout: Duration) -> Result<HashMap<Ipv4Addr, f64>, Unavailable> {
    let sem = Arc::new(tokio::sync::Semaphore::new(64));
    let mut set = tokio::task::JoinSet::new();
    for ip in ips {
        let sem = sem.clone();
        set.spawn(async move {
            let _permit = sem.acquire_owned().await.ok()?;
            let r = tokio::task::spawn_blocking(move || win::ping_once(ip, timeout)).await.ok()??;
            Some((ip, r))
        });
    }
    let mut out = HashMap::new();
    while let Some(r) = set.join_next().await {
        if let Ok(Some((ip, ms))) = r {
            out.insert(ip, ms);
        }
    }
    Ok(out)
}

#[cfg(windows)]
mod win {
    use std::net::Ipv4Addr;
    use std::time::{Duration, Instant};
    use windows_sys::Win32::Foundation::INVALID_HANDLE_VALUE;
    use windows_sys::Win32::NetworkManagement::IpHelper::{IcmpCloseHandle, IcmpCreateFile, IcmpSendEcho, ICMP_ECHO_REPLY};

    /// One echo via iphlpapi. Returns RTT in ms on success.
    pub fn ping_once(ip: Ipv4Addr, timeout: Duration) -> Option<f64> {
        unsafe {
            let h = IcmpCreateFile();
            if h == INVALID_HANDLE_VALUE || h.is_null() {
                return None;
            }
            let payload = *b"InternetMon";
            let mut reply = vec![0u8; std::mem::size_of::<ICMP_ECHO_REPLY>() + payload.len() + 64];
            let dest = u32::from_ne_bytes(ip.octets());
            let start = Instant::now();
            let n = IcmpSendEcho(
                h,
                dest,
                payload.as_ptr().cast(),
                payload.len() as u16,
                std::ptr::null(),
                reply.as_mut_ptr().cast(),
                reply.len() as u32,
                timeout.as_millis().clamp(100, 10_000) as u32,
            );
            let elapsed = start.elapsed().as_secs_f64() * 1000.0;
            IcmpCloseHandle(h);
            if n == 0 {
                return None;
            }
            let r = &*(reply.as_ptr() as *const ICMP_ECHO_REPLY);
            // IP_SUCCESS == 0, and the reply must come from the address we pinged.
            if r.Status != 0 || r.Address != dest {
                return None;
            }
            Some(if r.RoundTripTime > 0 { f64::from(r.RoundTripTime) } else { elapsed.min(1.0) })
        }
    }
}

#[cfg(not(any(unix, windows)))]
async fn platform_sweep(_ips: Vec<Ipv4Addr>, _timeout: Duration) -> Result<HashMap<Ipv4Addr, f64>, Unavailable> {
    Err(Unavailable("ICMP not supported on this platform".into()))
}

/// TCP-connect liveness sweep: a host is alive if any port accepts or actively refuses.
pub async fn tcp_sweep(ips: Vec<Ipv4Addr>, ports: &[u16], timeout: Duration) -> HashMap<Ipv4Addr, f64> {
    let sem = Arc::new(tokio::sync::Semaphore::new(256));
    let mut set = tokio::task::JoinSet::new();
    for ip in ips {
        for &port in ports {
            let sem = sem.clone();
            set.spawn(async move {
                let _p = sem.acquire_owned().await.ok()?;
                let start = Instant::now();
                let addr = SocketAddr::from((ip, port));
                match tokio::time::timeout(timeout, tokio::net::TcpStream::connect(addr)).await {
                    Ok(Ok(_)) => Some((ip, start.elapsed().as_secs_f64() * 1000.0)),
                    Ok(Err(e)) if e.kind() == std::io::ErrorKind::ConnectionRefused => {
                        Some((ip, start.elapsed().as_secs_f64() * 1000.0))
                    }
                    _ => None,
                }
            });
        }
    }
    let mut out: HashMap<Ipv4Addr, f64> = HashMap::new();
    while let Some(r) = set.join_next().await {
        if let Ok(Some((ip, ms))) = r {
            let e = out.entry(ip).or_insert(ms);
            if ms < *e {
                *e = ms;
            }
        }
    }
    out
}

/// ICMP when possible, else TCP probing. Second value says which method was used.
pub async fn liveness(ips: Vec<Ipv4Addr>, timeout: Duration) -> (HashMap<Ipv4Addr, f64>, &'static str) {
    match sweep(ips.clone(), timeout).await {
        Ok(m) => (m, "icmp"),
        Err(_) => (tcp_sweep(ips, &FALLBACK_PORTS, Duration::from_millis(700)).await, "tcp"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn checksum_of_request_verifies() {
        let p = echo_request(0x1234, 7);
        assert_eq!(p[0], 8);
        // checksum over a packet including its checksum must be zero
        assert_eq!(checksum(&p), 0);
        assert_eq!(checksum(&[0x45, 0x00, 0x00, 0x1c]), !(0x4500u16 + 0x001c));
    }

    #[test]
    fn reply_detection() {
        // Linux style: raw ICMP
        assert!(is_echo_reply(&[0, 0, 0, 0, 0, 1, 0, 1]));
        assert!(!is_echo_reply(&[8, 0, 0, 0]));
        // macOS style: IPv4 header (IHL 5 = 20 bytes) + ICMP
        let mut v = vec![0x45u8];
        v.extend(std::iter::repeat_n(0u8, 19));
        v.extend([0, 0, 0, 0]);
        assert!(is_echo_reply(&v));
        assert!(!is_echo_reply(&[0x45, 0, 0]));
        assert!(!is_echo_reply(&[]));
    }

    /// Runs on every CI OS: loopback must be found alive by ICMP or by the TCP fallback.
    /// On Windows this exercises IcmpSendEcho for real.
    #[tokio::test]
    async fn liveness_loopback() {
        let (m, method) = liveness(vec![Ipv4Addr::LOCALHOST], Duration::from_millis(1500)).await;
        assert!(m.contains_key(&Ipv4Addr::LOCALHOST), "loopback not alive via {method}");
        if cfg!(windows) {
            assert_eq!(method, "icmp", "IcmpSendEcho should work unprivileged");
        }
    }

    #[tokio::test]
    async fn tcp_refused_counts_as_alive() {
        // Bind then drop a listener to get a port that refuses connections on loopback.
        let l = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let port = l.local_addr().unwrap().port();
        drop(l);
        // Windows retries a refused SYN for ~2 s before reporting it, hence the generous timeout.
        let m = tcp_sweep(vec![Ipv4Addr::LOCALHOST], &[port], Duration::from_millis(3000)).await;
        assert!(m.contains_key(&Ipv4Addr::LOCALHOST));
    }
}
