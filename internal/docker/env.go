package docker

import (
	"encoding/json"
	"fmt"
	"strings"
)

// containerEnv returns the container's configured environment (Config.Env)
// as a map. Values are read as JSON, so they may contain any characters.
func containerEnv(r Runner, containerID string) (map[string]string, error) {
	out, err := r.RunOutput("docker inspect --format '{{json .Config.Env}}' "+shellQuote(containerID), "")
	if err != nil {
		return nil, fmt.Errorf("docker inspect: %w", err)
	}
	var list []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &list); err != nil {
		return nil, fmt.Errorf("parsing container env: %w", err)
	}
	env := make(map[string]string, len(list))
	for _, kv := range list {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env, nil
}
