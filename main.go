package main

import (
	"fmt"
	"os"

	"github.com/alexbabintsev/laradok/internal/config"
	"github.com/alexbabintsev/laradok/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

// version is the build version, injected at release time via -ldflags by
// GoReleaser. Defaults to "dev" for local builds.
var version = "dev"

func main() {
	// Handle --version / --help before anything else so they work without a
	// valid config. The first positional argument is otherwise a config path.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-v", "--version", "version":
			fmt.Printf("laradok %s\n", version)
			return
		case "-h", "--help", "help":
			fmt.Printf("laradok %s - a terminal UI for Laravel apps in Docker\n\n", version)
			fmt.Println("Usage:")
			fmt.Println("  laradok [config.yaml]   Launch the TUI (defaults to ~/.config/laradok/config.yaml)")
			fmt.Println("  laradok --version       Print the version")
			fmt.Println("  laradok --help          Show this help")
			return
		}
	}

	// Determine config path: arg > env > default
	cfgPath := os.Getenv("LARADOK_CONFIG")
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	app := tui.NewApp(cfg, cfgPath)

	p := tea.NewProgram(
		app,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running laradok: %v\n", err)
		os.Exit(1)
	}
}
