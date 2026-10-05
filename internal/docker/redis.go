package docker

import (
	"net/url"
	"strings"
)

// DetectRedisPassword reads a Redis password from the container env
// (REDIS_PASSWORD, or the password embedded in REDIS_URL). Empty means
// the server is unauthenticated.
func DetectRedisPassword(r Runner, containerID string) (string, error) {
	env, err := containerEnv(r, containerID)
	if err != nil {
		return "", err
	}
	if p := env["REDIS_PASSWORD"]; p != "" {
		return p, nil
	}
	if _, p, ok := parseURLCreds(env["REDIS_URL"]); ok {
		return p, nil
	}
	return "", nil
}

// parseURLCreds extracts the (percent-decoded) user and password from a URL
// such as redis://user:pass@host:6379/0 or mongodb://user:pass@h1,h2/db.
// It is parsed by hand because multi-host connection strings are not valid
// for net/url.
func parseURLCreds(raw string) (user, password string, ok bool) {
	_, rest, found := strings.Cut(raw, "://")
	if !found {
		return "", "", false
	}
	// The authority ends at the first '/', '?' or '#'.
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return "", "", false
	}
	userinfo := rest[:at]
	u, p, _ := strings.Cut(userinfo, ":")
	if du, err := url.PathUnescape(u); err == nil {
		u = du
	}
	if dp, err := url.PathUnescape(p); err == nil {
		p = dp
	}
	return u, p, true
}

// redisSecrets passes the password via stdin → REDISCLI_AUTH, which redis-cli
// reads instead of the "-a" flag (visible in process listings).
func redisSecrets(password string) []Secret {
	if password == "" {
		return nil
	}
	return []Secret{{Name: "REDISCLI_AUTH", Value: password}}
}

// RedisCmd builds the command running `redis-cli <args>` in the container.
// args is a trusted, built-in argument string and may contain a pipe (e.g.
// "--scan | head -20"), which is then evaluated inside the container.
func RedisCmd(containerID, password, args string) HostCommand {
	return ExecStreamScript(containerID, "redis-cli "+strings.TrimSpace(args), redisSecrets(password)...)
}

// DumpRedis downloads an RDB snapshot of the Redis instance. `redis-cli --rdb`
// asks the server for a fresh dump and writes it to a private temp file inside
// the container, which is then streamed back and removed.
func DumpRedis(r Runner, containerID, password string) (<-chan string, func(), error) {
	script := `f=$(mktemp) || exit 1; trap 'rm -f "$f"' EXIT; trap 'exit 143' TERM; ` +
		`redis-cli --rdb "$f" >/dev/null || exit $?; cat "$f"`
	hc := ExecStreamScript(containerID, script, redisSecrets(password)...)
	return download(r, hc, "Starting Redis RDB snapshot...", "redis_"+timestamp()+".rdb", nil)
}
