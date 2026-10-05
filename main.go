package main

import (
	"fmt"
	"io"
	"os"

	"github.com/alexbabintsev/laradok/internal/config"
	"github.com/alexbabintsev/laradok/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

// version is the build version, injected at release time via -ldflags by
// GoReleaser. Defaults to "dev" for local builds.
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, runTUI))
}

// run handles the command line and returns the process exit code. startTUI
// runs the interactive program (replaced in tests).
func run(args []string, stdout, stderr io.Writer, startTUI func(*config.Config, string) error) int {
	// Handle --version / --help before anything else so they work without a
	// valid config. The first positional argument is otherwise a config path.
	if len(args) > 0 {
		switch args[0] {
		case "-v", "--version", "version":
			fmt.Fprintf(stdout, "laradok %s\n", version)
			return 0
		case "-h", "--help", "help":
			fmt.Fprintf(stdout, "laradok %s - a terminal UI for Laravel apps in Docker\n\n", version)
			fmt.Fprintln(stdout, "Usage:")
			fmt.Fprintln(stdout, "  laradok [config.yaml]   Launch the TUI (defaults to ~/.config/laradok/config.yaml)")
			fmt.Fprintln(stdout, "  laradok --version       Print the version")
			fmt.Fprintln(stdout, "  laradok --help          Show this help")
			return 0
		}
	}

	// Determine config path: arg > env > default
	cfgPath := os.Getenv("LARADOK_CONFIG")
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	if len(args) > 0 {
		cfgPath = args[0]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "Error loading config: %v\n", err)
		return 1
	}
	if err := startTUI(cfg, cfgPath); err != nil {
		fmt.Fprintf(stderr, "Error running laradok: %v\n", err)
		return 1
	}
	return 0
}

// runTUI runs the interactive program and, once it exits, stops whatever is
// still streaming and closes the server connection.
func runTUI(cfg *config.Config, cfgPath string) error {
	app := tui.NewApp(cfg, cfgPath)
	p := tea.NewProgram(
		app,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	_, err := p.Run()
	app.Shutdown()
	return err
}
