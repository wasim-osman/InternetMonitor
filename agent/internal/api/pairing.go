package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"sync"
	"time"
)

const (
	PairCodeTTL      = 10 * time.Minute
	PairAttemptLimit = 5
	PairWindow       = time.Minute
	maxOpenCodes     = 20
)

type pairCode struct {
	code string
	exp  time.Time
}

// Pairing manages one-time pairing codes and per-IP attempt rate limiting.
type Pairing struct {
	mu       sync.Mutex
	codes    []pairCode
	attempts map[string][]time.Time
}

func NewPairing() *Pairing { return &Pairing{attempts: make(map[string][]time.Time)} }

func (p *Pairing) pruneLocked(now time.Time) {
	kept := p.codes[:0]
	for _, c := range p.codes {
		if now.Before(c.exp) {
			kept = append(kept, c)
		}
	}
	p.codes = kept
}

// NewCode creates a fresh 6-digit code valid for 10 minutes.
func (p *Pairing) NewCode(now time.Time) (string, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(now)
	var code string
	for {
		n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
		if err != nil {
			panic(err)
		}
		code = fmt.Sprintf("%06d", n.Int64())
		dup := false
		for _, c := range p.codes {
			if c.code == code {
				dup = true
			}
		}
		if !dup {
			break
		}
	}
	if len(p.codes) >= maxOpenCodes {
		p.codes = p.codes[1:]
	}
	exp := now.Add(PairCodeTTL)
	p.codes = append(p.codes, pairCode{code: code, exp: exp})
	return code, exp
}

// Redeem consumes a valid code (single use). Comparison is constant-time.
func (p *Pairing) Redeem(code string, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(now)
	if len(code) != 6 {
		return false
	}
	idx := -1
	for i, c := range p.codes {
		if subtle.ConstantTimeCompare([]byte(c.code), []byte(code)) == 1 {
			idx = i
		}
	}
	if idx < 0 {
		return false
	}
	p.codes = append(p.codes[:idx], p.codes[idx+1:]...)
	return true
}

// Allow records an attempt from ip and reports whether it is within the limit
// (5 attempts per rolling minute).
func (p *Pairing) Allow(ip string, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	cut := now.Add(-PairWindow)
	if len(p.attempts) > 1000 { // GC stale entries
		for k, v := range p.attempts {
			if len(v) == 0 || !v[len(v)-1].After(cut) {
				delete(p.attempts, k)
			}
		}
	}
	a := p.attempts[ip]
	kept := a[:0]
	for _, t := range a {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= PairAttemptLimit {
		p.attempts[ip] = kept
		return false
	}
	p.attempts[ip] = append(kept, now)
	return true
}

// NewToken returns a 64-hex-char random token.
func NewToken() string {
	var b [32]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// HashToken returns hex(sha256(token)).
func HashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
