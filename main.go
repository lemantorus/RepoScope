package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- Логика сканирования ---

type Project struct {
	Name      string
	Marker    string
	Size      int64
	FileCount int
	Status    string
	Path      string
}

var markers = map[string]string{
	".git": "Git", "package.json": "JS", "go.mod": "Go", "requirements.txt": "Py",
	"pyproject.toml": "Py", "Cargo.toml": "Rust", "pom.xml": "Java", "composer.json": "PHP",
}

var blacklist = map[string]bool{
	"node_modules": true, "venv": true, ".venv": true, ".git": true,
	"dist": true, "build": true, "vendor": true, "target": true,
}

func scanProjects(root string) []Project {
	var projects []Project
	processed := make(map[string]bool)

	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil { return nil }
		if d.IsDir() && (blacklist[d.Name()] || (strings.HasPrefix(d.Name(), ".") && d.Name() != ".git")) {
			return filepath.SkipDir
		}

		if marker, ok := markers[d.Name()]; ok {
			projectRoot := filepath.Dir(path)
			if processed[projectRoot] { return filepath.SkipDir }

			processed[projectRoot] = true
			projects = append(projects, analyzeProject(projectRoot, marker))
			return filepath.SkipDir
		}
		return nil
	})
	return projects
}

func analyzeProject(path string, marker string) Project {
	p := Project{Name: filepath.Base(path), Marker: marker, Path: path}
	
	// Подсчет веса (пропуская тяжелые папки зависимостей внутри)
	filepath.WalkDir(path, func(_ string, d fs.DirEntry, _ error) error {
		if d == nil { return nil }
		if d.IsDir() && blacklist[d.Name()] { return filepath.SkipDir }
		if !d.IsDir() {
			p.FileCount++
			if info, err := d.Info(); err == nil { p.Size += info.Size() }
		}
		return nil
	})
	
	p.Status = getGitSmartStatus(path)
	return p
}

// Умная проверка Git
func getGitSmartStatus(path string) string {
	if _, err := os.Stat(filepath.Join(path, ".git")); os.IsNotExist(err) {
		return "—"
	}

	// 1. Проверяем, есть ли вообще коммиты
	commitCheck := exec.Command("git", "rev-parse", "--verify", "HEAD")
	commitCheck.Dir = path
	if err := commitCheck.Run(); err != nil {
		return "No Commits"
	}

	// 2. Проверяем, есть ли Remote (связь с облаком)
	remoteCheck := exec.Command("git", "remote")
	remoteCheck.Dir = path
	remotes, _ := remoteCheck.Output()
	hasRemote := len(strings.TrimSpace(string(remotes))) > 0

	// 3. Проверяем незакоммиченные изменения
	statusCheck := exec.Command("git", "status", "--short")
	statusCheck.Dir = path
	changes, _ := statusCheck.Output()
	hasChanges := len(strings.TrimSpace(string(changes))) > 0

	if hasChanges {
		return "Uncommitted"
	}
	if !hasRemote {
		return "No Remote"
	}

	return "Synced"
}

// --- Интерфейс ---

var baseStyle = lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("240"))

type model struct {
	table         table.Model
	projects      []Project
	sortColumnIdx int
	ascending     bool
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "ctrl+c":
			return m, tea.Quit
		case "left", "h":
			m.sortColumnIdx = (m.sortColumnIdx - 1 + 5) % 5
			m.sortData()
		case "right", "l":
			m.sortColumnIdx = (m.sortColumnIdx + 1) % 5
			m.sortData()
		case "s":
			m.ascending = !m.ascending
			m.sortData()
		case "enter":
			currIdx := m.table.Cursor()
			if currIdx >= 0 && currIdx < len(m.projects) {
				openFolder(m.projects[currIdx].Path)
			}
		}
	}
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func openFolder(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows": cmd = exec.Command("explorer", path)
	case "darwin": cmd = exec.Command("open", path)
	default: cmd = exec.Command("xdg-open", path)
	}
	cmd.Run()
}

func (m *model) sortData() {
	sort.Slice(m.projects, func(i, j int) bool {
		res := false
		p1, p2 := m.projects[i], m.projects[j]
		switch m.sortColumnIdx {
		case 0: res = strings.ToLower(p1.Name) < strings.ToLower(p2.Name)
		case 1: res = p1.Marker < p2.Marker
		case 2: res = p1.Size < p2.Size
		case 3: res = p1.FileCount < p2.FileCount
		case 4: res = p1.Status < p2.Status
		}
		if !m.ascending { return !res }
		return res
	})

	rows := []table.Row{}
	for _, p := range m.projects {
		rows = append(rows, table.Row{
			p.Name, p.Marker, formatSize(p.Size), fmt.Sprintf("%d", p.FileCount), p.Status,
		})
	}
	m.table.SetColumns(m.getTableColumns())
	m.table.SetRows(rows)
}

func (m model) getTableColumns() []table.Column {
	headers := []string{"Project", "Type", "Size", "Files", "Git Status"}
	widths := []int{25, 8, 10, 8, 15}
	cols := []table.Column{}
	for i, h := range headers {
		title := h
		if i == m.sortColumnIdx {
			arrow := " 🔽"
			if m.ascending { arrow = " 🔼" }
			title += arrow
		}
		cols = append(cols, table.Column{Title: title, Width: widths[i]})
	}
	return cols
}

func (m model) View() string {
	return baseStyle.Render(m.table.View()) + "\n" +
		" ←/→: Sort | Enter: Open | S: Order | Q: Quit\n"
}

func formatSize(b int64) string {
	if b < 1024 { return fmt.Sprintf("%d B", b) }
	if b < 1024*1024 { return fmt.Sprintf("%.1f KB", float64(b)/1024) }
	return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
}

func main() {
	flag.Parse()
	path := flag.Arg(0)
	if path == "" { path, _ = os.Getwd() }

	fmt.Printf("Scanning %s...\n", path)
	projs := scanProjects(path)

	if len(projs) == 0 {
		fmt.Println("No projects found.")
		return
	}

	m := model{projects: projs, sortColumnIdx: 0, ascending: true}
	t := table.New(table.WithColumns(m.getTableColumns()), table.WithFocused(true), table.WithHeight(15))

	s := table.DefaultStyles()
	s.Header = s.Header.BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("240")).BorderBottom(true).Bold(true)
	s.Selected = s.Selected.Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57"))
	t.SetStyles(s)

	m.table = t
	m.sortData()

	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
