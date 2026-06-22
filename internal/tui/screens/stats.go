package screens

import (
	"fmt"
	"strings"
	"time"

	"github.com/alexbabintsev/laradok/internal/docker"
	"github.com/alexbabintsev/laradok/internal/msgs"
	"github.com/alexbabintsev/laradok/internal/tui/styles"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	statsHistoryLen      = 60 // samples kept per metric (also the max sparkline width)
	statsMaxWidth        = statsHistoryLen
	statsIntervalSeconds = 2.0
)

// scheduleSample requests the next stats poll after the refresh interval. The
// App handles StatsTickMsg by sampling docker stats and replying with a
// StatsSampleMsg, keeping the runner out of this screen.
func scheduleSample(sortBy docker.ProcSortBy) tea.Cmd {
	return tea.Tick(time.Duration(statsIntervalSeconds*float64(time.Second)), func(time.Time) tea.Msg {
		return msgs.StatsTickMsg{SortProcs: sortBy}
	})
}

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// StatsScreen shows live CPU / Memory / Network / Disk charts for one container,
// polling `docker stats` on an interval.
type StatsScreen struct {
	containerName string

	cpuHist   []float64 // CPU %
	memHist   []float64 // memory bytes
	netHist   []float64 // network bytes/sec
	diskHist  []float64 // block I/O bytes/sec
	memLimit  float64
	lastNet   float64
	lastBlock float64
	hasLast   bool

	curCPU  float64
	curMem  float64
	curNet  float64
	curDisk float64

	procs  []docker.ProcessInfo
	sortBy docker.ProcSortBy

	errMsg string
	width  int
	height int
}

func NewStatsScreen(containerName string, width, height int) *StatsScreen {
	return &StatsScreen{
		containerName: containerName,
		width:         width,
		height:        height,
	}
}

func (s *StatsScreen) Init() tea.Cmd { return nil }

// resortProcs reorders the already-fetched process list so a sort toggle takes
// effect immediately, without waiting for the next poll.
func (s *StatsScreen) resortProcs() {
	for i := 1; i < len(s.procs); i++ {
		for j := i; j > 0; j-- {
			var swap bool
			if s.sortBy == docker.SortByMem {
				swap = s.procs[j].Mem > s.procs[j-1].Mem
			} else {
				swap = s.procs[j].CPU > s.procs[j-1].CPU
			}
			if !swap {
				break
			}
			s.procs[j], s.procs[j-1] = s.procs[j-1], s.procs[j]
		}
	}
}

func pushHist(h []float64, v float64) []float64 {
	h = append(h, v)
	if len(h) > statsHistoryLen {
		h = h[len(h)-statsHistoryLen:]
	}
	return h
}

func (s *StatsScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case msgs.StatsSampleMsg:
		if msg.Err != nil {
			s.errMsg = msg.Err.Error()
			// keep polling — the container may just be starting
			return s, scheduleSample(s.sortBy)
		}
		s.errMsg = ""
		sm := msg.Sample
		s.procs = msg.Procs

		s.curCPU = sm.CPUPercent
		s.curMem = sm.MemBytes
		s.memLimit = sm.MemLimit
		s.cpuHist = pushHist(s.cpuHist, sm.CPUPercent)
		s.memHist = pushHist(s.memHist, sm.MemBytes)

		// Network/Disk are cumulative totals — chart the per-interval rate.
		if s.hasLast {
			net := (sm.NetTotal - s.lastNet) / statsIntervalSeconds
			disk := (sm.BlockTotal - s.lastBlock) / statsIntervalSeconds
			if net < 0 {
				net = 0
			}
			if disk < 0 {
				disk = 0
			}
			s.curNet = net
			s.curDisk = disk
			s.netHist = pushHist(s.netHist, net)
			s.diskHist = pushHist(s.diskHist, disk)
		}
		s.lastNet = sm.NetTotal
		s.lastBlock = sm.BlockTotal
		s.hasLast = true

		return s, scheduleSample(s.sortBy)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return s, tea.Quit
		case "esc":
			return s, func() tea.Msg { return msgs.PopMsg{} }
		case "c":
			s.sortBy = docker.SortByCPU
			s.resortProcs()
		case "m":
			s.sortBy = docker.SortByMem
			s.resortProcs()
		}

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
	}
	return s, nil
}

// panelWidth/panelHeight derive the size of each of the 2x2 quadrants.
func (s *StatsScreen) panelInnerWidth() int {
	w := (s.width - 6) / 2 // two panels, borders + gap
	if w > statsMaxWidth {
		w = statsMaxWidth
	}
	if w < 10 {
		w = 10
	}
	return w
}

func (s *StatsScreen) panelHeight() int {
	h := (s.height - 6) / 2
	if h < 5 {
		h = 5
	}
	if h > 12 {
		h = 12
	}
	return h
}

func (s *StatsScreen) View() string {
	title := styles.TitleBar.Render("Stats — " + s.containerName)

	pw := s.panelInnerWidth()
	ph := s.panelHeight()
	chartH := ph - 2 // minus title row and value already on title row

	cpu := s.panel("CPU", fmt.Sprintf("%.1f%%", s.curCPU), s.cpuHist, pw, chartH, styles.ColorDanger, 100)
	mem := s.panel("Memory", formatBytes(int64(s.curMem)), s.memHist, pw, chartH, styles.ColorAccent, s.memLimit)
	net := s.panel("Network", formatRate(s.curNet), s.netHist, pw, chartH, styles.ColorSuccess, 0)
	disk := s.panel("Disk", formatRate(s.curDisk), s.diskHist, pw, chartH, styles.ColorWarning, 0)

	top := lipgloss.JoinHorizontal(lipgloss.Top, cpu, "  ", mem)
	bottom := lipgloss.JoinHorizontal(lipgloss.Top, net, "  ", disk)
	grid := lipgloss.JoinVertical(lipgloss.Left, top, "", bottom)

	procTable := s.renderProcs()

	parts := []string{grid}
	if procTable != "" {
		parts = append(parts, "", procTable)
	}
	if s.errMsg != "" {
		parts = append(parts, "", lipgloss.NewStyle().Foreground(styles.ColorDanger).Render("  "+s.errMsg))
	}
	body := lipgloss.JoinVertical(lipgloss.Left, parts...)

	sortLabel := "CPU"
	if s.sortBy == docker.SortByMem {
		sortLabel = "MEM"
	}
	help := styles.StatusBar.Width(s.width).Render(
		styles.StatusBarKey.Render("c") + " sort CPU  " +
			styles.StatusBarKey.Render("m") + " sort MEM  " +
			styles.StatusBarKey.Render("esc") + " back  " +
			lipgloss.NewStyle().Foreground(styles.ColorMuted).Render("(by "+sortLabel+", 2s)"),
	)

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", body)
	return styles.PinToBottom(s.height, content, help)
}

// panel renders one bordered metric box with a header (label + current value)
// and a sparkline chart. fixedMax > 0 scales the chart to that ceiling;
// otherwise it auto-scales to the series max.
func (s *StatsScreen) panel(label, value string, hist []float64, innerW, chartH int, color lipgloss.Color, fixedMax float64) string {
	labelStyle := lipgloss.NewStyle().Foreground(styles.ColorText).Bold(true)
	valStyle := lipgloss.NewStyle().Foreground(styles.ColorMuted)

	header := lipgloss.PlaceHorizontal(innerW, lipgloss.Left, labelStyle.Render(label+":"))
	// overlay the right-aligned value
	valRendered := valStyle.Render(value)
	pad := innerW - lipgloss.Width(label+":") - lipgloss.Width(value)
	if pad < 1 {
		pad = 1
	}
	header = labelStyle.Render(label+":") + strings.Repeat(" ", pad) + valRendered

	chart := sparkChart(hist, innerW, chartH, color, fixedMax)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.ColorBorder).
		Padding(0, 1).
		Width(innerW + 2).
		Render(lipgloss.JoinVertical(lipgloss.Left, header, chart))
	return box
}

// sparkChart renders a multi-row sparkline-style area chart from the history.
func sparkChart(hist []float64, width, rows int, color lipgloss.Color, fixedMax float64) string {
	if rows < 1 {
		rows = 1
	}
	style := lipgloss.NewStyle().Foreground(color)

	// Determine the scale ceiling.
	max := fixedMax
	if max <= 0 {
		for _, v := range hist {
			if v > max {
				max = v
			}
		}
	}
	if max <= 0 {
		max = 1
	}

	// Take the last `width` samples.
	data := hist
	if len(data) > width {
		data = data[len(data)-width:]
	}

	// Build a single sparkline row of runes (compact, like the mockup).
	var line strings.Builder
	// left-pad with blanks so the line is right-aligned and grows from the right
	blanks := width - len(data)
	for i := 0; i < blanks; i++ {
		line.WriteByte(' ')
	}
	for _, v := range data {
		frac := v / max
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		idx := int(frac * float64(len(sparkRunes)-1))
		line.WriteRune(sparkRunes[idx])
	}

	// Pad vertically so panels are equal height; the sparkline sits at the bottom.
	var out []string
	for i := 0; i < rows-1; i++ {
		out = append(out, "")
	}
	out = append(out, style.Render(line.String()))
	return strings.Join(out, "\n")
}

// renderProcs draws the top-processes table beneath the charts. It shows as
// many rows as the remaining vertical space allows.
func (s *StatsScreen) renderProcs() string {
	if len(s.procs) == 0 {
		return ""
	}
	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F1C40F")).Bold(true)
	colStyle := lipgloss.NewStyle().Foreground(styles.ColorMuted)
	cmdStyle := lipgloss.NewStyle().Foreground(styles.ColorText)

	// Rows left after charts grid (2 panels high) + spacing + help.
	avail := s.height - 2*s.panelHeight() - 6
	if avail < 3 {
		avail = 3
	}
	maxRows := avail - 1 // header row
	if maxRows < 1 {
		maxRows = 1
	}

	cmdWidth := s.width - 24
	if cmdWidth < 8 {
		cmdWidth = 8
	}

	var lines []string
	lines = append(lines, headerStyle.Render(
		fmt.Sprintf("  %-7s %6s %6s  %s", "PID", "CPU%", "MEM%", "COMMAND")))

	procs := s.procs
	if len(procs) > maxRows {
		procs = procs[:maxRows]
	}
	for _, p := range procs {
		cmd := truncate(p.Command, cmdWidth)
		lines = append(lines, fmt.Sprintf("  %s %s %s  %s",
			colStyle.Render(fmt.Sprintf("%-7s", p.PID)),
			colStyle.Render(fmt.Sprintf("%6.1f", p.CPU)),
			colStyle.Render(fmt.Sprintf("%6.1f", p.Mem)),
			cmdStyle.Render(cmd),
		))
	}
	return strings.Join(lines, "\n")
}

func formatRate(bytesPerSec float64) string {
	switch {
	case bytesPerSec >= 1024*1024:
		return fmt.Sprintf("%.1f MB/s", bytesPerSec/1024/1024)
	case bytesPerSec >= 1024:
		return fmt.Sprintf("%.1f KB/s", bytesPerSec/1024)
	default:
		return fmt.Sprintf("%.0f B/s", bytesPerSec)
	}
}
