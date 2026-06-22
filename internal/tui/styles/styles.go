package styles

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// PinToBottom returns a string that places statusBar at the bottom of a
// totalHeight-tall screen, filling the gap between content and the bar with
// empty lines.
func PinToBottom(totalHeight int, content, statusBar string) string {
	used := strings.Count(content, "\n") + 1
	gap := totalHeight - used - 1 // -1 for the status bar line itself
	if gap < 0 {
		gap = 0
	}
	return content + "\n" + strings.Repeat("\n", gap) + statusBar
}

var (
	// Base colors
	ColorPrimary   = lipgloss.Color("#4F46E5") // indigo
	ColorSecondary = lipgloss.Color("#6366F1")
	ColorAccent    = lipgloss.Color("#818CF8")
	ColorSuccess   = lipgloss.Color("#22C55E")
	ColorWarning   = lipgloss.Color("#F59E0B")
	ColorDanger    = lipgloss.Color("#EF4444")
	ColorMuted     = lipgloss.Color("#6B7280")
	ColorBg        = lipgloss.Color("#0F172A")
	ColorSurface   = lipgloss.Color("#1E293B")
	ColorBorder    = lipgloss.Color("#334155")
	ColorText      = lipgloss.Color("#F1F5F9")
	ColorTextDim   = lipgloss.Color("#94A3B8")

	// Title bar
	TitleBar = lipgloss.NewStyle().
			Background(ColorPrimary).
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true).
			Padding(0, 2)

	// App container
	AppStyle = lipgloss.NewStyle().
			Padding(1, 2)

	// Section header (group name)
	GroupHeader = lipgloss.NewStyle().
			Foreground(ColorTextDim).
			Bold(true).
			MarginBottom(1)

	// Card / panel box
	CardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(1, 2)

	// Selected card
	CardStyleSelected = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorPrimary).
				Padding(1, 2)

	// Command button — normal
	CmdButton = lipgloss.NewStyle().
			Background(ColorPrimary).
			Foreground(lipgloss.Color("#FFFFFF")).
			Padding(0, 2).
			Width(22).
			Align(lipgloss.Center)

	// Command button — focused
	CmdButtonFocused = lipgloss.NewStyle().
				Background(ColorSecondary).
				Foreground(lipgloss.Color("#FFFFFF")).
				Bold(true).
				Padding(0, 2).
				Width(22).
				Align(lipgloss.Center)

	// Status bar at the bottom
	StatusBar = lipgloss.NewStyle().
			Background(ColorSurface).
			Foreground(ColorTextDim).
			Padding(0, 2)

	StatusBarKey = lipgloss.NewStyle().
			Background(ColorBorder).
			Foreground(ColorText).
			Padding(0, 1)

	// Output / log viewport
	OutputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	// Breadcrumb
	Breadcrumb = lipgloss.NewStyle().
			Foreground(ColorMuted)

	BreadcrumbSep = lipgloss.NewStyle().
			Foreground(ColorBorder)

	BreadcrumbActive = lipgloss.NewStyle().
				Foreground(ColorAccent).
				Bold(true)

	// Server badge
	ServerBadgeSSH = lipgloss.NewStyle().
			Background(ColorPrimary).
			Foreground(lipgloss.Color("#FFFFFF")).
			Padding(0, 1)

	ServerBadgeLocal = lipgloss.NewStyle().
				Background(ColorSuccess).
				Foreground(lipgloss.Color("#FFFFFF")).
				Padding(0, 1)

	// Spinner label
	SpinnerStyle = lipgloss.NewStyle().
			Foreground(ColorAccent)

	// Error text
	ErrorStyle = lipgloss.NewStyle().
			Foreground(ColorDanger)

	// Success text
	SuccessStyle = lipgloss.NewStyle().
			Foreground(ColorSuccess)

	// Dim text
	DimStyle = lipgloss.NewStyle().
			Foreground(ColorTextDim)
)
