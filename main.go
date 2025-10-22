package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Stash struct {
	Ref, Message, Created string
}

func (s Stash) Title() string       { return s.Message }
func (s Stash) Description() string { return fmt.Sprintf("%s (%s)", s.Ref, s.Created) }
func (s Stash) FilterValue() string { return s.Message }

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------
type stashDiffMsg struct {
	ref  string
	diff string
	err  error
}
type stashDeletedMsg struct {
	ref string
	err error
}
type stashAppliedMsg struct {
	ref string
	err error
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------
type model struct {
	stashList   list.Model
	viewport    viewport.Model
	width       int
	height      int
	diff        string
	loading     bool
	err         error
	confirmMode bool
	selectedRef string
}

func initialModel() model {
	stashes, err := listStashes()
	items := make([]list.Item, len(stashes))
	for i, s := range stashes {
		items[i] = s
	}
	l := list.New(items, list.NewDefaultDelegate(), 30, 10)
	l.Title = "Packrat - Git Stash Explorer"

	vp := viewport.New(80, 20)
	vp.Style = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(1)

	return model{
		stashList: l,
		viewport:  vp,
		err:       err,
	}
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------
func (m model) Init() tea.Cmd {
	if len(m.stashList.Items()) > 0 {
		ref := m.stashList.SelectedItem().(Stash).Ref
		return getStashDiff(ref)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helper Functions
// ---------------------------------------------------------------------------
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

// ---------------------------------------------------------------------------
// Tea Messages
// ---------------------------------------------------------------------------
func getStashDiff(ref string) tea.Cmd {
	return func() tea.Msg {
		// -c color.ui=always tells git to include the ANSI colors even though it's not going direct to a terminal
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

func applyStash(ref string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("git", "stash", "apply", ref)
		err := cmd.Run()
		return stashAppliedMsg{ref: ref, err: err}
	}
}

// ---------------------------------------------------------------------------
// Update (the game loop)
// ---------------------------------------------------------------------------
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		listWidth := 75
		m.stashList.SetHeight(m.height - 2)
		m.stashList.SetWidth(listWidth)
		m.viewport.Width = m.width - (listWidth + 2) - 4
		m.viewport.Height = m.height - 4

	case tea.KeyMsg:
		switch {
		case msg.String() == "ctrl+c" || msg.String() == "q":
			if m.confirmMode {
				m.confirmMode = false
			} else {
				return m, tea.Quit
			}
		case m.confirmMode:
			switch msg.String() {
			case "y", "Y":
				m.confirmMode = false
				ref := m.selectedRef
				return m, dropStash(ref)
			case "n", "N", "esc":
				m.confirmMode = false
			}
		default:
			switch msg.String() {
			case "enter":
				if sel, ok := m.stashList.SelectedItem().(Stash); ok {
					m.loading = true
					return m, getStashDiff(sel.Ref)
				}
			case "d":
				if sel, ok := m.stashList.SelectedItem().(Stash); ok {
					m.selectedRef = sel.Ref
					m.confirmMode = true
				}
			case "a":
				if sel, ok := m.stashList.SelectedItem().(Stash); ok {
					m.loading = true
					return m, applyStash(sel.Ref)
				}
			}
		}

	case stashDiffMsg:
		m.loading = false
		if msg.err != nil {
			m.diff = fmt.Sprintf("Error loading diff: %v", msg.err)
		} else {
			m.diff = msg.diff
		}
		m.viewport.SetContent(m.diff)
		m.viewport.GotoTop()

	case stashDeletedMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			// Re-fetch the list of stashes so that the indexes aren't messed up
			stashes, err := listStashes()
			items := make([]list.Item, len(stashes))
			for i, s := range stashes {
				items[i] = s
			}
			m.stashList.SetItems(items)

			if len(m.stashList.Items()) > 0 {
				ref := m.stashList.SelectedItem().(Stash).Ref
				cmds = append(cmds, getStashDiff(ref))
			} else {
				m.viewport.SetContent("(no stashes)")
			}

			if err != nil {
				m.viewport.SetContent(fmt.Sprintf("Error reloading stashes: %v", err))
			}
		}

	case stashAppliedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.viewport.SetContent("Stash applied!")
			m.viewport.GotoTop()
		}

	}

	// Scroll diff viewport if active
	if !m.confirmMode {
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)

		m.stashList, cmd = m.stashList.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------
var (
	borderStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(1)
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("36"))
	modalStyle  = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			Padding(1, 2).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("52"))
)

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	leftPane := borderStyle.Render(m.stashList.View())
	header := titleStyle.Render("[Enter] Show stash  [a] Apply stash  [d] Drop stash  [q] Quit  [↑/↓] Scroll diff")

	right := m.viewport.View()
	rightPane := borderStyle.Render(header + "\n\n" + right)
	view := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

	if m.confirmMode {
		modal := modalStyle.Render(fmt.Sprintf("Delete %s?\n\n[y] Yes   [n] No", m.selectedRef))
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
	}

	return view
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
