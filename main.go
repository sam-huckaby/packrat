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
	ref    string
	output string
	err    error
}

// ---------------------------------------------------------------------------
// States
// ---------------------------------------------------------------------------

type AppState int

const (
	StateExplore AppState = iota
	StateDelete
	StateInspect
	StateCleanUp
)

var stateName = map[AppState]string{
	StateExplore: "explore",
	StateDelete:  "delete",
	StateInspect: "inspect",
	StateCleanUp: "cleanup",
}

// ---------------------------------------------------------------------------
// Modal Types
// ---------------------------------------------------------------------------

type ModalType int

const (
	ModalNone ModalType = iota
	ModalDeleteConfirm
	ModalApplyConfirm
)

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
	activeModal ModalType // The type of modal currently displayed (ModalNone if no modal)
	appState    AppState  // The state of the app at any given moment
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
		appState:  StateExplore,
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
		out, err := cmd.CombinedOutput()
		return stashAppliedMsg{ref: ref, output: string(out), err: err}
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

		// Calculate dimensions accounting for borders and padding
		// borderStyle adds: 2 for border (left+right or top+bottom) + 2 for padding = 4 total per dimension
		const borderChrome = 4

		// Left pane (list) takes up about 75 columns
		listPaneWidth := 75
		listContentWidth := listPaneWidth - borderChrome

		// Right pane (viewport) takes the remaining width
		rightPaneWidth := m.width - listPaneWidth
		viewportContentWidth := rightPaneWidth - borderChrome

		// Height calculations - both panes should have the same total height
		// Content inside the border should be: m.height - borderChrome
		totalContentHeight := m.height - borderChrome

		// For the viewport, we need to account for the header (1 line) + spacing (2 lines) = 3 lines
		const headerAndSpacing = 2
		viewportHeight := totalContentHeight - headerAndSpacing
		if viewportHeight < 0 {
			viewportHeight = 0
		}

		// Set the actual component sizes (these are the content sizes, borders will be added in View)
		m.stashList.SetWidth(listContentWidth)
		m.stashList.SetHeight(totalContentHeight)
		m.viewport.Width = viewportContentWidth
		m.viewport.Height = viewportHeight

	case tea.KeyMsg:
		switch {
		case msg.String() == "ctrl+c" || msg.String() == "q":
			if m.activeModal != ModalNone {
				m.activeModal = ModalNone
			} else {
				return m, tea.Quit
			}
		case m.activeModal == ModalDeleteConfirm:
			switch msg.String() {
			case "y", "Y":
				m.activeModal = ModalNone
				ref := m.selectedRef
				return m, dropStash(ref)
			case "n", "N", "esc":
				m.activeModal = ModalNone
			}
		case m.activeModal == ModalApplyConfirm:
			switch msg.String() {
			case "y", "Y":
				m.activeModal = ModalNone
				m.loading = true
				ref := m.selectedRef
				return m, applyStash(ref)
			case "n", "N", "esc":
				m.activeModal = ModalNone
			}
		default:
			switch msg.String() {
			case "enter": // View a stash's contents
				if sel, ok := m.stashList.SelectedItem().(Stash); ok {
					m.loading = true
					return m, getStashDiff(sel.Ref)
				}
			case "d": // Delete a stash
				if sel, ok := m.stashList.SelectedItem().(Stash); ok {
					m.selectedRef = sel.Ref
					m.activeModal = ModalDeleteConfirm
				}
			case "a": // Apply a stash
				if sel, ok := m.stashList.SelectedItem().(Stash); ok {
					m.selectedRef = sel.Ref
					m.activeModal = ModalApplyConfirm
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
			m.viewport.SetContent(fmt.Sprintf("Error applying stash:\n\n%s", msg.output))
		} else {
			m.viewport.SetContent(fmt.Sprintf("Stash applied successfully!\n\n%s", msg.output))
		}
		m.viewport.GotoTop()

	}

	// Scroll diff viewport if active
	if m.activeModal == ModalNone {
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

func (m model) renderModal() string {
	switch m.activeModal {
	case ModalDeleteConfirm:
		return modalStyle.Render(fmt.Sprintf("Delete %s?\n\n[y] Yes   [n] No", m.selectedRef))
	case ModalApplyConfirm:
		return modalStyle.Render(fmt.Sprintf("Apply %s?\n\n[y] Yes   [n] No", m.selectedRef))
	default:
		return ""
	}
}

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	if m.activeModal != ModalNone {
		modal := m.renderModal()
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
	}

	// Render left pane with border
	leftPane := borderStyle.Render(m.stashList.View())

	// Render header and viewport for right pane
	header := titleStyle.Render("[Enter] Show stash  [a] Apply stash  [d] Drop stash  [q] Quit  [↑/↓] Scroll diff")
	viewportContent := m.viewport.View()

	// Combine header and viewport, then wrap in border
	rightContent := header + "\n\n" + viewportContent
	rightPane := borderStyle.Render(rightContent)

	// Join panes side by side - they should naturally be the same height since we sized them equally
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
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
