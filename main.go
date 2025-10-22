package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Stash represents one git stash entry.
type Stash struct {
	Ref, Message, Created string
}

func (s Stash) Title() string       { return s.Message }
func (s Stash) Description() string { return fmt.Sprintf("%s (%s)", s.Ref, s.Created) }
func (s Stash) FilterValue() string { return s.Message }

// model represents the entire TUI state.
type model struct {
	stashList list.Model
	diff      string
	width     int
	height    int
	loading   bool
	err       error
}

// Messages for async updates
type stashDiffMsg struct {
	ref  string
	diff string
	err  error
}
type stashDeletedMsg struct {
	ref string
	err error
}

// --- Commands -------------------------------------------------------------

func getStashDiff(ref string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("git", "-c", "color.ui=always", "stash", "show", "-u", "-p", ref)
		out, err := cmd.CombinedOutput()
		return stashDiffMsg{ref: ref, diff: string(out), err: err}
	}
}

func dropStash(ref string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("git", "stash", "drop", ref)
		err := cmd.Run()
		return stashDeletedMsg{ref: ref, err: err}
	}
}

// --- UI Styling -----------------------------------------------------------

var (
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	borderStyle   = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(1)
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("36"))
)

// --- Init -----------------------------------------------------------------

func initialModel() model {
	stashes, err := listStashes()
	items := make([]list.Item, len(stashes))
	for i, s := range stashes {
		items[i] = s
	}

	l := list.New(items, list.NewDefaultDelegate(), 30, 10)
	l.Title = "Git Stashes"

	m := model{
		stashList: l,
		diff:      "",
		err:       err,
	}
	return m
}

// --- Update ---------------------------------------------------------------

func (m model) Init() tea.Cmd {
	if len(m.stashList.Items()) > 0 {
		ref := m.stashList.SelectedItem().(Stash).Ref
		return getStashDiff(ref)
	}
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.stashList.SetSize(msg.Width/3, msg.Height-2)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "enter":
			if sel, ok := m.stashList.SelectedItem().(Stash); ok {
				m.loading = true
				return m, getStashDiff(sel.Ref)
			}
		case "d":
			if sel, ok := m.stashList.SelectedItem().(Stash); ok {
				return m, dropStash(sel.Ref)
			}
		}

	case stashDiffMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.diff = fmt.Sprintf("Error loading diff: %v", msg.err)
		} else {
			m.diff = msg.diff
		}

	case stashDeletedMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			// remove from list
			idx := m.stashList.Index()
			m.stashList.RemoveItem(idx)
			if len(m.stashList.Items()) > 0 {
				ref := m.stashList.SelectedItem().(Stash).Ref
				cmds = append(cmds, getStashDiff(ref))
			} else {
				m.diff = "(no stashes)"
			}
		}
	}

	m.stashList, cmd = m.stashList.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

// --- View -----------------------------------------------------------------

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	leftPane := borderStyle.Render(m.stashList.View())

	rightContent := m.diff
	if m.loading {
		rightContent = "Loading..."
	}

	header := titleStyle.Render("[Enter] Show stash  [d] Drop stash  [q] Quit")
	rightPane := borderStyle.Render(header + "\n\n" + rightContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
}

// --- Helpers --------------------------------------------------------------

func listStashes() ([]Stash, error) {
	cmd := exec.Command("git", "stash", "list", "--pretty=format:%gd|%gs|%cr")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	var stashes []Stash
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "|", 3)
		if len(parts) == 3 {
			stashes = append(stashes, Stash{parts[0], parts[1], parts[2]})
		}
	}
	return stashes, scanner.Err()
}

// --- Main -----------------------------------------------------------------

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
