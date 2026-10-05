package docker

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// MountInfo describes a single bind mount or volume attached to a container.
type MountInfo struct {
	Source      string
	Destination string
	Type        string // "bind", "volume", etc.
	RW          bool
}

// NetworkInfo describes a container's attachment to one network.
type NetworkInfo struct {
	Name      string
	IPAddress string
}

// KeyValue is a generic label/value pair, kept ordered for stable display.
type KeyValue struct {
	Key   string
	Value string
}

// ContainerInfo is the parsed subset of `docker inspect` shown on the Info screen.
type ContainerInfo struct {
	Name     string
	ID       string
	Image    string
	Status   string // human-readable, derived from State
	Networks []NetworkInfo
	Mounts   []MountInfo
	Labels   []KeyValue
}

// rawInspect mirrors the fields we read out of `docker inspect`.
type rawInspect struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		ExitCode   int    `json:"ExitCode"`
	} `json:"State"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	Image  string `json:"Image"` // image digest/id
	Mounts []struct {
		Type        string `json:"Type"`
		Source      string `json:"Source"`
		Name        string `json:"Name"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// InspectContainer runs `docker inspect` and returns the parsed info subset.
func InspectContainer(r Runner, containerID string) (ContainerInfo, error) {
	cmd := fmt.Sprintf(`docker inspect %s`, shellQuote(containerID))
	out, err := r.RunCommand(cmd)
	if err != nil {
		return ContainerInfo{}, fmt.Errorf("docker inspect: %w\n%s", err, out)
	}
	out = stripNUL(out)

	var raws []rawInspect
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &raws); err != nil {
		return ContainerInfo{}, fmt.Errorf("parse docker inspect: %w", err)
	}
	if len(raws) == 0 {
		return ContainerInfo{}, fmt.Errorf("container not found")
	}
	ri := raws[0]

	info := ContainerInfo{
		Name:   strings.TrimPrefix(ri.Name, "/"),
		ID:     ri.ID,
		Image:  ri.Config.Image,
		Status: humanState(ri),
	}

	for name, n := range ri.NetworkSettings.Networks {
		info.Networks = append(info.Networks, NetworkInfo{Name: name, IPAddress: n.IPAddress})
	}
	sort.Slice(info.Networks, func(i, j int) bool { return info.Networks[i].Name < info.Networks[j].Name })

	for _, m := range ri.Mounts {
		src := m.Source
		if src == "" {
			src = m.Name // named volumes report Name instead of Source
		}
		info.Mounts = append(info.Mounts, MountInfo{
			Source:      src,
			Destination: m.Destination,
			Type:        m.Type,
			RW:          m.RW,
		})
	}

	keys := make([]string, 0, len(ri.Config.Labels))
	for k := range ri.Config.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		info.Labels = append(info.Labels, KeyValue{Key: k, Value: ri.Config.Labels[k]})
	}

	return info, nil
}

// humanState renders a "Up 10 seconds" / "Exited (0)" style status.
func humanState(ri rawInspect) string {
	if ri.State.Running {
		return "Up (" + ri.State.Status + ")"
	}
	if ri.State.Status == "" {
		return "unknown"
	}
	return fmt.Sprintf("%s (exit %d)", strings.Title(ri.State.Status), ri.State.ExitCode) //nolint:staticcheck
}
