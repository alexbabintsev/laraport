package docker

import (
	"fmt"
	"strings"
)

// DetectRedisPassword reads a Redis password from the container env
// (REDIS_PASSWORD, or the password embedded in REDIS_URL). Empty means
// the server is unauthenticated.
func DetectRedisPassword(r Runner, containerID string) (string, error) {
	cmd := fmt.Sprintf(`docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' %s`, shellQuote(containerID))
	out, err := r.RunCommand(cmd)
	if err != nil {
		return "", fmt.Errorf("docker inspect: %w", err)
	}
	out = stripNUL(out)

	var pass string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "REDIS_PASSWORD":
			pass = v
		case "REDIS_URL":
			// redis://[:password@]host:port[/db]
			if u := strings.TrimPrefix(v, "redis://"); u != v {
				if at := strings.LastIndex(u, "@"); at >= 0 {
					cred := u[:at]
					if c := strings.IndexByte(cred, ':'); c >= 0 {
						cred = cred[c+1:]
					}
					if cred != "" && pass == "" {
						pass = cred
					}
				}
			}
		}
	}
	return pass, nil
}

// RedisCLIPrefix builds the `docker exec ... redis-cli [-a pass]` prefix that
// Redis commands are appended to. The password (if any) is passed via -a.
func RedisCLIPrefix(containerID, password string) string {
	if password != "" {
		// --no-auth-warning suppresses the stderr notice about -a on the CLI.
		return fmt.Sprintf(
			`docker exec %s redis-cli --no-auth-warning -a %s`,
			shellQuote(containerID), shellQuote(password),
		)
	}
	return fmt.Sprintf(`docker exec %s redis-cli`, shellQuote(containerID))
}
