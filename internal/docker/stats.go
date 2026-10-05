package docker

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ContainerStat holds the live resource usage of one container, as reported by
// `docker stats --no-stream`.
type ContainerStat struct {
	ID       string // short ID (matches Container.ID prefix)
	Name     string
	CPUPerc  string // e.g. "2.10%"
	MemUsage string // e.g. "120MiB / 512MiB"
	MemPerc  string // e.g. "23.40%"
	NetIO    string // e.g. "1.2MB / 0.8MB"
	BlockIO  string // e.g. "0B / 4.1MB"
	PIDs     string // e.g. "18"
}

// ListContainerStats returns a one-shot resource snapshot for all running
// containers in a single `docker stats --no-stream` call. The returned map is
// keyed by both container name and short ID for easy lookup.
//
// docker stats is slower than docker ps (it samples metrics), so callers should
// run this lazily/in the background after the container list is already shown.
func ListContainerStats(r Runner) (map[string]ContainerStat, error) {
	format := `{"id":"{{.ID}}","name":"{{.Name}}","cpu":"{{.CPUPerc}}","mem":"{{.MemUsage}}","memperc":"{{.MemPerc}}","net":"{{.NetIO}}","block":"{{.BlockIO}}","pids":"{{.PIDs}}"}`
	cmd := fmt.Sprintf(`docker stats --no-stream --format '%s'`, format)

	out, err := r.RunCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("docker stats: %w\n%s", err, out)
	}
	out = stripNUL(out)

	result := make(map[string]ContainerStat)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var raw struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			CPU     string `json:"cpu"`
			Mem     string `json:"mem"`
			MemPerc string `json:"memperc"`
			Net     string `json:"net"`
			Block   string `json:"block"`
			PIDs    string `json:"pids"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		st := ContainerStat{
			ID:       raw.ID,
			Name:     strings.TrimPrefix(raw.Name, "/"),
			CPUPerc:  raw.CPU,
			MemUsage: raw.Mem,
			MemPerc:  raw.MemPerc,
			NetIO:    raw.Net,
			BlockIO:  raw.Block,
			PIDs:     raw.PIDs,
		}
		if st.Name != "" {
			result[st.Name] = st
		}
		if st.ID != "" {
			result[st.ID] = st
		}
	}
	return result, nil
}

// Lookup returns the stat for a container by name, falling back to a short-ID
// match (docker ps IDs and docker stats IDs share a 12-char prefix).
func LookupStat(stats map[string]ContainerStat, name, id string) (ContainerStat, bool) {
	if st, ok := stats[name]; ok {
		return st, true
	}
	if len(id) >= 12 {
		if st, ok := stats[id[:12]]; ok {
			return st, true
		}
	}
	if st, ok := stats[id]; ok {
		return st, true
	}
	return ContainerStat{}, false
}
