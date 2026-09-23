// Package report collects diagnostics (warnings/info) emitted during conversion
// so the CLI can surface everything that could not be mapped automatically.
package report

import (
	"fmt"
	"sort"
	"strings"
)

type Level string

const (
	LevelInfo Level = "INFO"
	LevelWarn Level = "WARN"
)

type Entry struct {
	Level    Level
	Resource string
	Message  string
}

type Reporter struct {
	Entries []Entry
}

func New() *Reporter { return &Reporter{} }

func (r *Reporter) Warnf(resource, format string, a ...any) {
	r.Entries = append(r.Entries, Entry{LevelWarn, resource, fmt.Sprintf(format, a...)})
}

func (r *Reporter) Infof(resource, format string, a ...any) {
	r.Entries = append(r.Entries, Entry{LevelInfo, resource, fmt.Sprintf(format, a...)})
}

func (r *Reporter) HasWarnings() bool {
	for _, e := range r.Entries {
		if e.Level == LevelWarn {
			return true
		}
	}
	return false
}

// String renders entries grouped by level, warnings first.
func (r *Reporter) String() string {
	if len(r.Entries) == 0 {
		return ""
	}
	entries := make([]Entry, len(r.Entries))
	copy(entries, r.Entries)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Level == LevelWarn && entries[j].Level != LevelWarn
	})
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "[%s] %s: %s\n", e.Level, e.Resource, e.Message)
	}
	return b.String()
}
