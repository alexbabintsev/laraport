package docker

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// LiveSample is a single point-in-time stats reading for one container, with
// values parsed into numbers for charting.
type LiveSample struct {
	CPUPercent float64 // 0..100 (can exceed 100 on multi-core)
	MemBytes   float64 // current memory usage in bytes
	MemLimit   float64 // memory limit in bytes
	NetTotal   float64 // cumulative network bytes (rx+tx)
	BlockTotal float64 // cumulative block I/O bytes (read+write)
}

// SampleContainerStats fetches a one-shot stats reading for a single container.
func SampleContainerStats(r Runner, containerID string) (LiveSample, error) {
	format := `{"cpu":{{json .CPUPerc}},"mem":{{json .MemUsage}},"net":{{json .NetIO}},"block":{{json .BlockIO}}}`
	cmd := "docker stats --no-stream --format " + shellQuote(format) + " " + shellQuote(containerID)

	out, err := r.RunOutput(cmd, "")
	if err != nil {
		return LiveSample{}, fmt.Errorf("docker stats: %w", err)
	}

	line := ""
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "{") {
			line = l
			break
		}
	}
	if line == "" {
		return LiveSample{}, fmt.Errorf("no stats output (container not running?)")
	}

	var raw struct {
		CPU   string `json:"cpu"`
		Mem   string `json:"mem"`
		Net   string `json:"net"`
		Block string `json:"block"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return LiveSample{}, fmt.Errorf("parse stats: %w", err)
	}

	mem, limit := parseUsageLimit(raw.Mem)
	net := parsePairSum(raw.Net)
	block := parsePairSum(raw.Block)
	return LiveSample{
		CPUPercent: parsePercent(raw.CPU),
		MemBytes:   mem,
		MemLimit:   limit,
		NetTotal:   net,
		BlockTotal: block,
	}, nil
}

// parsePercent turns "2.10%" into 2.10.
func parsePercent(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// parseUsageLimit turns "120MiB / 512MiB" into (usage, limit) in bytes.
func parseUsageLimit(s string) (usage, limit float64) {
	parts := strings.SplitN(s, "/", 2)
	usage = parseSize(strings.TrimSpace(parts[0]))
	if len(parts) == 2 {
		limit = parseSize(strings.TrimSpace(parts[1]))
	}
	return usage, limit
}

// parsePairSum turns "1.2MB / 0.8MB" into the sum of both sides in bytes.
func parsePairSum(s string) float64 {
	parts := strings.SplitN(s, "/", 2)
	total := parseSize(strings.TrimSpace(parts[0]))
	if len(parts) == 2 {
		total += parseSize(strings.TrimSpace(parts[1]))
	}
	return total
}

// parseSize converts a docker size string ("120MiB", "1.2GB", "0B") to bytes.
func parseSize(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	// Find where the numeric part ends.
	i := 0
	for i < len(s) && (s[i] == '.' || s[i] == '-' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	num, _ := strconv.ParseFloat(s[:i], 64)
	unit := strings.ToLower(strings.TrimSpace(s[i:]))

	mult := 1.0
	switch unit {
	case "b", "":
		mult = 1
	case "kb", "kib", "k":
		mult = 1024
	case "mb", "mib", "m":
		mult = 1024 * 1024
	case "gb", "gib", "g":
		mult = 1024 * 1024 * 1024
	case "tb", "tib", "t":
		mult = 1024 * 1024 * 1024 * 1024
	}
	return num * mult
}
