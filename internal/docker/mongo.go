package docker

import (
	"fmt"
	"strings"
)

// DetectMongoCredentials reads MongoDB root credentials from the container env
// (MONGO_INITDB_ROOT_USERNAME / MONGO_INITDB_ROOT_PASSWORD, or the credentials
// embedded in MONGO_URL / MONGODB_URI). Empty user means an unauthenticated
// server.
func DetectMongoCredentials(r Runner, containerID string) (user, password string, err error) {
	cmd := fmt.Sprintf(`docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' %s`, containerID)
	out, err := r.RunCommand(cmd)
	if err != nil {
		return "", "", fmt.Errorf("docker inspect: %w", err)
	}
	out = strings.Map(func(r rune) rune {
		if r == 0 {
			return -1
		}
		return r
	}, out)

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "MONGO_INITDB_ROOT_USERNAME":
			user = v
		case "MONGO_INITDB_ROOT_PASSWORD":
			password = v
		case "MONGO_URL", "MONGODB_URI":
			if u, p, ok := parseMongoURICreds(v); ok {
				if user == "" {
					user = u
				}
				if password == "" {
					password = p
				}
			}
		}
	}
	return user, password, nil
}

// parseMongoURICreds extracts user:password from a mongodb:// or mongodb+srv://
// connection string.
func parseMongoURICreds(uri string) (user, password string, ok bool) {
	rest := uri
	for _, prefix := range []string{"mongodb+srv://", "mongodb://"} {
		if strings.HasPrefix(rest, prefix) {
			rest = strings.TrimPrefix(rest, prefix)
			break
		}
	}
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return "", "", false
	}
	cred := rest[:at]
	if c := strings.IndexByte(cred, ':'); c >= 0 {
		return cred[:c], cred[c+1:], true
	}
	return cred, "", true
}

// MongoEvalCmd builds the host command that runs a JS expression via the mongo
// shell. mongoBin is "mongosh" or "mongo"; dbName scopes the shell to a
// database (empty = default). Auth args are added when a user is present.
func MongoEvalCmd(containerID, mongoBin, user, password, dbName, js string) string {
	auth := ""
	if user != "" {
		// --authenticationDatabase admin matches the default root user setup.
		auth = fmt.Sprintf(
			` -u %s -p %s --authenticationDatabase admin`,
			shellQuote(user), shellQuote(password),
		)
	}
	db := ""
	if dbName != "" {
		db = " " + shellQuote(dbName)
	}
	return fmt.Sprintf(
		`docker exec %s %s%s%s --quiet --eval %s`,
		containerID, mongoBin, auth, db, shellQuote(js),
	)
}
