package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"imonitord/internal/config"
)

type Incident struct {
	ID         string   `json:"id"`
	TargetID   string   `json:"targetId"`
	TargetName string   `json:"targetName"`
	Kind       string   `json:"kind"` // down | degraded
	StartedAt  int64    `json:"startedAt"`
	EndedAt    *int64   `json:"endedAt"`
	Detail     string   `json:"detail"`
	PeakMs     *float64 `json:"peakMs"`
}

// IncidentLog is an append-only JSONL file; a later line with the same id
// supersedes an earlier one (open -> closed).
type IncidentLog struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

func NewIncidentLog(dir string) *IncidentLog {
	return &IncidentLog{path: filepath.Join(dir, "incidents.jsonl")}
}

// Load returns incidents sorted by StartedAt ascending (last line per id wins).
func (l *IncidentLog) Load() []Incident {
	l.mu.Lock()
	defer l.mu.Unlock()
	byID := map[string]Incident{}
	scanFile(l.path, func(line []byte) {
		var inc Incident
		if json.Unmarshal(line, &inc) == nil && inc.ID != "" {
			byID[inc.ID] = inc
		}
	})
	out := make([]Incident, 0, len(byID))
	for _, inc := range byID {
		out = append(out, inc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt != out[j].StartedAt {
			return out[i].StartedAt < out[j].StartedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Write appends the current state of an incident (written immediately).
func (l *IncidentLog) Write(inc Incident) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		if st, err := f.Stat(); err == nil && st.Size() > 0 {
			var last [1]byte
			if _, err := f.ReadAt(last[:], st.Size()-1); err == nil && last[0] != '\n' {
				f.Write([]byte{'\n'})
			}
		}
		l.f = f
	}
	data, err := json.Marshal(inc)
	if err != nil {
		return err
	}
	_, err = l.f.Write(append(data, '\n'))
	return err
}

// Rewrite atomically replaces the file with the given incidents (compaction).
func (l *IncidentLog) Rewrite(incs []Incident) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
	var buf []byte
	for _, inc := range incs {
		data, err := json.Marshal(inc)
		if err != nil {
			return err
		}
		buf = append(buf, data...)
		buf = append(buf, '\n')
	}
	return config.WriteFileAtomic(l.path, buf, 0o600)
}

func (l *IncidentLog) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
}
