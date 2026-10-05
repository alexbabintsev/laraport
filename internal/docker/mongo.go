package docker

import (
	"strings"
)

// DetectMongoCredentials reads MongoDB root credentials from the container env
// (MONGO_INITDB_ROOT_USERNAME / MONGO_INITDB_ROOT_PASSWORD, or the credentials
// embedded in MONGO_URL / MONGODB_URI). Empty user means an unauthenticated
// server.
func DetectMongoCredentials(r Runner, containerID string) (user, password string, err error) {
	env, err := containerEnv(r, containerID)
	if err != nil {
		return "", "", err
	}
	user, password = env["MONGO_INITDB_ROOT_USERNAME"], env["MONGO_INITDB_ROOT_PASSWORD"]
	for _, k := range []string{"MONGO_URL", "MONGODB_URI"} {
		if u, p, ok := parseURLCreds(env[k]); ok {
			if user == "" {
				user = u
			}
			if password == "" {
				password = p
			}
		}
	}
	return user, password, nil
}

// mongoSecrets passes the credentials via stdin as LARADOK_MONGO_USER /
// LARADOK_MONGO_PASS.
func mongoSecrets(user, password string) []Secret {
	if user == "" {
		return nil
	}
	return []Secret{
		{Name: "LARADOK_MONGO_USER", Value: user},
		{Name: "LARADOK_MONGO_PASS", Value: password},
	}
}

// MongoEvalCmd builds the command that runs a JS expression via the mongo
// shell. mongoBin is "mongosh" or "mongo"; dbName scopes the shell to a
// database (empty = default).
//
// With mongosh the credentials never appear on a command line: the script
// authenticates against "admin" from environment variables (delivered via
// stdin) before running js. The legacy `mongo` shell has no access to the
// environment, so it falls back to -u/-p arguments.
func MongoEvalCmd(containerID, mongoBin, user, password, dbName, js string) HostCommand {
	var b strings.Builder
	b.WriteString(mongoBin)
	if dbName != "" {
		b.WriteString(" " + shellQuote(dbName))
	}
	b.WriteString(" --quiet")
	if user != "" && mongoBin == "mongo" {
		b.WriteString(` -u "$LARADOK_MONGO_USER" -p "$LARADOK_MONGO_PASS" --authenticationDatabase admin`)
	}
	if user != "" && mongoBin != "mongo" {
		js = `db.getSiblingDB('admin').auth(process.env.LARADOK_MONGO_USER, process.env.LARADOK_MONGO_PASS); ` + js
	}
	b.WriteString(" --eval " + shellQuote(js))
	return ExecStreamScript(containerID, b.String(), mongoSecrets(user, password)...)
}

// yamlQuote renders s as a double-quoted YAML scalar.
func yamlQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

// DumpMongo downloads a gzipped archive of all MongoDB databases via
// `mongodump --archive --gzip`, saved as mongo_<ts>.archive.gz (restore with
// `mongorestore --archive=… --gzip`). The password goes into a private
// --config file inside the container (removed afterwards) rather than argv.
func DumpMongo(r Runner, containerID, user, password string) (<-chan string, func(), error) {
	script := "mongodump --archive --gzip"
	var secrets []Secret
	if user != "" {
		secrets = []Secret{{Name: "LARADOK_MONGO_CFG", Value: "password: " + yamlQuote(password)}}
		script = `umask 077; f=$(mktemp) || exit 1; trap 'rm -f "$f"' EXIT; trap 'exit 143' TERM; ` +
			`printf '%s\n' "$LARADOK_MONGO_CFG" > "$f"; ` +
			"mongodump --config \"$f\" -u " + shellQuote(user) + " --authenticationDatabase admin --archive --gzip"
	}
	hc := ExecStreamScript(containerID, script, secrets...)
	return download(r, hc, "Starting mongodump...", "mongo_"+timestamp()+".archive.gz", nil)
}
