package docker

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ProcessInfo is one process row inside a container.
type ProcessInfo struct {
	PID     string
	CPU     float64 // percent
	Mem     float64 // percent
	Command string
}

// ProcSortBy selects the ordering of TopProcesses results.
type ProcSortBy int

const (
	SortByCPU ProcSortBy = iota
	SortByMem
)

// TopProcesses returns the processes running inside a container, sorted by CPU
// or memory. It uses `ps` inside the container (for %CPU/%MEM); if ps is not
// available the result will be empty.
func TopProcesses(r Runner, containerID string, by ProcSortBy, limit int) ([]ProcessInfo, error) {
	// -ww avoids truncating the command; we read comm+args via "args".
	cmd := fmt.Sprintf(
		`docker exec %s ps -eo pid,pcpu,pmem,comm --no-headers 2>/dev/null`,
		containerID,
	)
	out, err := r.RunCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	out = strings.Map(func(r rune) rune {
		if r == 0 {
			return -1
		}
		return r
	}, out)

	var procs []ProcessInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		cpu, _ := strconv.ParseFloat(fields[1], 64)
		mem, _ := strconv.ParseFloat(fields[2], 64)
		procs = append(procs, ProcessInfo{
			PID:     fields[0],
			CPU:     cpu,
			Mem:     mem,
			Command: strings.Join(fields[3:], " "),
		})
	}

	sort.Slice(procs, func(i, j int) bool {
		if by == SortByMem {
			return procs[i].Mem > procs[j].Mem
		}
		return procs[i].CPU > procs[j].CPU
	})
	if limit > 0 && len(procs) > limit {
		procs = procs[:limit]
	}
	return procs, nil
}
