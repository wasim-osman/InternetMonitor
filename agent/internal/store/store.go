// Package store persists raw samples (hourly JSONL segments), 1-minute rollups
// (daily JSONL files) and incidents in the state directory.
package store

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"imonitord/internal/stats"
)

const (
	flushEvery   = 5 * time.Second
	minuteValCap = 256 // > 120 samples/min at the 500 ms minimum interval
)

// segWriter appends to a time-segmented file with a buffered writer.
type segWriter struct {
	dir  string
	name string
	f    *os.File
	w    *bufio.Writer
}

func (s *segWriter) ensure(name string) error {
	if s.name == name && s.f != nil {
		return nil
	}
	s.close()
	path := filepath.Join(s.dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	// Repair a torn last line (crash mid-write) so the next record starts clean.
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], st.Size()-1); err == nil && last[0] != '\n' {
			f.Write([]byte{'\n'})
		}
	}
	s.f, s.name = f, name
	if s.w == nil {
		s.w = bufio.NewWriterSize(f, 16*1024)
	} else {
		s.w.Reset(f)
	}
	return nil
}

func (s *segWriter) write(name string, line []byte) {
	if err := s.ensure(name); err != nil {
		log.Printf("store: open %s: %v", name, err)
		return
	}
	s.w.Write(line)
}

func (s *segWriter) flush() {
	if s.f != nil && s.w != nil {
		if err := s.w.Flush(); err != nil {
			log.Printf("store: flush %s: %v", s.name, err)
		}
	}
}

func (s *segWriter) close() {
	if s.f != nil {
		s.flush()
		s.f.Close()
		s.f = nil
		s.name = ""
	}
}

type minuteAcc struct {
	minute int64
	agg    stats.Agg
	vals   [minuteValCap]float32
	nv     int
}

// Store is safe for concurrent use.
type Store struct {
	mu        sync.Mutex
	dir       string
	raw       segWriter
	roll      segWriter
	accs      map[string]*minuteAcc
	lastFlush time.Time
	line      []byte // scratch buffer
}

func New(dir string) *Store {
	return &Store{
		dir:  dir,
		raw:  segWriter{dir: dir},
		roll: segWriter{dir: dir},
		accs: make(map[string]*minuteAcc),
		line: make([]byte, 0, 256),
	}
}

func RawName(t int64) string {
	return "raw-" + time.Unix(t, 0).UTC().Format("2006010215") + ".jsonl"
}

func RollupName(t int64) string {
	return "rollup-" + time.Unix(t, 0).UTC().Format("20060102") + ".jsonl"
}

// AddSample records one probe result. rtt is ignored when !ok. errStr may be
// set on a successful sample (e.g. HTTP 5xx).
func (s *Store) AddSample(id string, t int64, rtt float64, ok bool, errStr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.line[:0]
	b = append(b, `{"t":`...)
	b = appendInt(b, t)
	b = append(b, `,"target":`...)
	b = appendJSONString(b, id)
	if ok {
		b = append(b, `,"ms":`...)
		b = appendNum(b, rtt)
	}
	if errStr != "" {
		b = append(b, `,"err":`...)
		b = appendJSONString(b, errStr)
	}
	b = append(b, "}\n"...)
	s.line = b
	s.raw.write(RawName(t), b)

	minute := t - mod(t, 60)
	acc := s.accs[id]
	if acc == nil {
		acc = &minuteAcc{minute: minute}
		s.accs[id] = acc
	}
	if acc.minute != minute {
		s.finalizeLocked(id, acc)
		acc.minute = minute
	}
	acc.agg.Add(rtt, ok)
	if ok && acc.nv < minuteValCap {
		acc.vals[acc.nv] = float32(rtt)
		acc.nv++
	}
}

func appendInt(b []byte, v int64) []byte {
	if v < 0 {
		b = append(b, '-')
		v = -v
	}
	var tmp [20]byte
	i := len(tmp)
	for {
		i--
		tmp[i] = byte('0' + v%10)
		v /= 10
		if v == 0 {
			break
		}
	}
	return append(b, tmp[i:]...)
}

func mod(a, b int64) int64 {
	m := a % b
	if m < 0 {
		m += b
	}
	return m
}

func (s *Store) finalizeLocked(id string, acc *minuteAcc) {
	if acc.agg.N == 0 {
		return
	}
	b := s.line[:0]
	b = append(b, `{"t":`...)
	b = appendInt(b, acc.minute)
	b = append(b, `,"target":`...)
	b = appendJSONString(b, id)
	b = append(b, `,"n":`...)
	b = appendInt(b, int64(acc.agg.N))
	b = append(b, `,"fails":`...)
	b = appendInt(b, int64(acc.agg.Fails))
	if acc.agg.Successes() > 0 {
		b = append(b, `,"min":`...)
		b = appendNum(b, acc.agg.Min)
		b = append(b, `,"avg":`...)
		b = appendNum(b, acc.agg.Avg())
		b = append(b, `,"max":`...)
		b = appendNum(b, acc.agg.Max)
		b = append(b, `,"p95":`...)
		b = appendNum(b, stats.Percentile32(acc.vals[:acc.nv], 95))
		if acc.agg.JitN > 0 {
			b = append(b, `,"jit":`...)
			b = appendNum(b, acc.agg.Jitter())
		}
	}
	b = append(b, "}\n"...)
	s.line = b
	s.roll.write(RollupName(acc.minute), b)
	acc.agg = stats.Agg{}
	acc.nv = 0
}

// Tick finalises minutes that can no longer receive samples (now past the
// minute end plus graceSec) and flushes buffers every 5 s.
func (s *Store) Tick(now time.Time, graceSec int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := now.Unix()
	for id, acc := range s.accs {
		if acc.agg.N > 0 && ns >= acc.minute+60+graceSec {
			s.finalizeLocked(id, acc)
		}
	}
	if now.Sub(s.lastFlush) >= flushEvery {
		s.raw.flush()
		s.roll.flush()
		s.lastFlush = now
	}
}

// Forget drops the in-progress minute of a removed target (after emitting it).
func (s *Store) Forget(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if acc := s.accs[id]; acc != nil {
		s.finalizeLocked(id, acc)
		delete(s.accs, id)
	}
}

// Flush writes buffered data to disk (in-progress minutes are kept).
func (s *Store) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.raw.flush()
	s.roll.flush()
}

// Close finalises all in-progress minutes and closes files.
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, acc := range s.accs {
		s.finalizeLocked(id, acc)
	}
	s.raw.close()
	s.roll.close()
}

// Reset deletes all samples and rollups.
func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.raw.close()
	s.roll.close()
	s.accs = make(map[string]*minuteAcc)
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		n := e.Name()
		if (strings.HasPrefix(n, "raw-") || strings.HasPrefix(n, "rollup-")) && strings.HasSuffix(n, ".jsonl") {
			os.Remove(filepath.Join(s.dir, n))
		}
	}
	return nil
}

// Prune deletes raw segments and rollup files that are entirely past retention.
func (s *Store) Prune(now int64, rawHours, rollupDays int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	rawCut := now - int64(rawHours)*3600
	rollCut := now - int64(rollupDays)*86400
	for _, e := range ents {
		n := e.Name()
		if !strings.HasSuffix(n, ".jsonl") {
			continue
		}
		var del bool
		switch {
		case strings.HasPrefix(n, "raw-"):
			t, err := time.Parse("2006010215", strings.TrimSuffix(strings.TrimPrefix(n, "raw-"), ".jsonl"))
			del = err == nil && t.Unix()+3600 <= rawCut
		case strings.HasPrefix(n, "rollup-"):
			t, err := time.Parse("20060102", strings.TrimSuffix(strings.TrimPrefix(n, "rollup-"), ".jsonl"))
			del = err == nil && t.Unix()+86400 <= rollCut
		}
		if del {
			if n == s.raw.name {
				s.raw.close()
			}
			if n == s.roll.name {
				s.roll.close()
			}
			os.Remove(filepath.Join(s.dir, n))
		}
	}
}

// scanFile calls fn for every line of a file; torn/garbage lines are the
// callee's problem (scanFlat rejects them).
func scanFile(path string, fn func(line []byte)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := r.ReadSlice('\n')
		if len(line) > 0 && err != bufio.ErrBufferFull {
			fn(bytes.TrimRight(line, "\r\n"))
		}
		if err == bufio.ErrBufferFull {
			// absurdly long line: skip to the next newline
			for err == bufio.ErrBufferFull {
				_, err = r.ReadSlice('\n')
			}
			continue
		}
		if err == io.EOF || err != nil {
			return
		}
	}
}

// rollupRec is one parsed rollup line.
type rollupRec struct {
	t                  int64
	n, fails           int
	min, avg, max, p95 float64
	jit                float64
}

func parseRollup(line []byte, wantTarget func(raw []byte) string) (string, rollupRec, bool) {
	r := rollupRec{t: -1, min: math.NaN(), avg: math.NaN(), max: math.NaN(), p95: math.NaN(), jit: math.NaN()}
	id := ""
	ok := scanFlat(line, func(k, v []byte) {
		switch bstr(k) {
		case "t":
			r.t = intVal(v)
		case "target":
			id = wantTarget(v)
		case "n":
			r.n = int(intVal(v))
		case "fails":
			r.fails = int(intVal(v))
		case "min":
			r.min = numVal(v)
		case "avg":
			r.avg = numVal(v)
		case "max":
			r.max = numVal(v)
		case "p95":
			r.p95 = numVal(v)
		case "jit":
			r.jit = numVal(v)
		}
	})
	if !ok || id == "" || r.t < 0 || r.n <= 0 {
		return "", r, false
	}
	return id, r, true
}

func dayFiles(dir string, from, to int64) []string {
	var out []string
	d := from - mod(from, 86400)
	for ; d <= to; d += 86400 {
		out = append(out, filepath.Join(dir, RollupName(d)))
	}
	return out
}

func hourFiles(dir string, from, to int64) []string {
	var out []string
	h := from - mod(from, 3600)
	for ; h <= to; h += 3600 {
		out = append(out, filepath.Join(dir, RawName(h)))
	}
	return out
}

// targetMatcher returns a function mapping a raw quoted target value to the
// canonical id string when it is in the wanted set ("" otherwise).
func targetMatcher(ids []string) func(raw []byte) string {
	set := make(map[string]string, len(ids))
	for _, id := range ids {
		set[id] = id
	}
	return func(raw []byte) string {
		if len(raw) < 2 || raw[0] != '"' {
			return ""
		}
		return set[bstr(raw[1:len(raw)-1])]
	}
}

func sortedKeys[V any](m map[int64]V) []int64 {
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
