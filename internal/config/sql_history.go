package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const maxPersistedSQLHistory = 200

func sqlHistoryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "sql_history.json"
	}
	return filepath.Join(home, ".config", "laradok", "sql_history.json")
}

func loadAllSQLHistory() map[string][]string {
	data, err := os.ReadFile(sqlHistoryPath())
	if err != nil {
		return map[string][]string{}
	}
	var h map[string][]string
	if err := json.Unmarshal(data, &h); err != nil {
		return map[string][]string{}
	}
	return h
}

// LoadSQLHistory returns persisted query history for a specific DB key.
// Key format: "serverName/containerName/dbName"
func LoadSQLHistory(key string) []string {
	all := loadAllSQLHistory()
	return all[key]
}

// SaveSQLHistory persists an updated query list for a specific DB key.
func SaveSQLHistory(key string, history []string) error {
	all := loadAllSQLHistory()
	if len(history) > maxPersistedSQLHistory {
		history = history[len(history)-maxPersistedSQLHistory:]
	}
	all[key] = history
	data, err := json.Marshal(all)
	if err != nil {
		return err
	}
	p := sqlHistoryPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}
