// Package dbg provides conditional debug logging to a file.
// Activate with: laradok_DEBUG=1 ./laradok
// Output goes to /tmp/laradok-debug.log
package dbg

import (
	"fmt"
	"log"
	"os"
	"sync"
)

var (
	once    sync.Once
	logger  *log.Logger
	enabled bool
)

func init() {
	enabled = os.Getenv("laradok_DEBUG") == "1"
	if !enabled {
		return
	}
	once.Do(func() {
		f, err := os.OpenFile("/tmp/laradok-debug.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return
		}
		logger = log.New(f, "", log.Ltime|log.Lmicroseconds)
		logger.Println("=== laradok debug log started ===")
	})
}

// Log writes a formatted debug line. No-op if laradok_DEBUG != "1".
func Log(format string, args ...any) {
	if !enabled || logger == nil {
		return
	}
	logger.Output(2, fmt.Sprintf(format, args...))
}
