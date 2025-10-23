package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
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

type FileChange struct {
	Path     string
	Status   string // e.g., "M" (modified), "A" (added), "D" (deleted), etc.
	IsStaged bool
}

func (f FileChange) Title() string {
	statusIndicator := "  "
	if f.IsStaged {
		statusIndicator = "● " // Staged
	} else {
		statusIndicator = "○ " // Unstaged
	}
	return fmt.Sprintf("%s%s %s", statusIndicator, f.Status, f.Path)
}
func (f FileChange) Description() string {
	if f.IsStaged {
		return "staged"
	}
	return "unstaged"
}
func (f FileChange) FilterValue() string { return f.Path }

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
type changedFilesMsg struct {
	files []FileChange
	err   error
}
type fileDiffMsg struct {
	path string
	diff string
	err  error
}
type stashCreatedMsg struct {
	output string
	err    error
}
type workingDirectoryRestoredMsg struct {
	output string
	err    error
}

// ---------------------------------------------------------------------------
// States
// ---------------------------------------------------------------------------

type Mode int

const (
	ModeExplore Mode = iota
	ModeBuild
)

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
	ModalStashMessage
	ModalRestoreConfirm
)

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------
type model struct {
	// Common fields
	width       int
	height      int
	loading     bool
	err         error
	activeModal ModalType // The type of modal currently displayed (ModalNone if no modal)
	appState    AppState  // The state of the app at any given moment
	mode        Mode      // Current mode: Explore or Build

	// Explore Mode fields
	stashList   list.Model
	viewport    viewport.Model
	diff        string
	selectedRef string

	// Build Mode fields
	fileList      list.Model
	selectedFiles map[string]FileChange // map of path -> FileChange for selected files
	expandedFiles map[string]bool       // map of path -> expanded state
	fileDiffs     map[string]string     // map of path -> diff content
	buildViewport viewport.Model        // viewport for the build mode right pane
	stashInput    textinput.Model       // text input for stash message
}

func initialModel() model {
	stashes, err := listStashes()
	items := make([]list.Item, len(stashes))
	for i, s := range stashes {
		items[i] = s
	}
	l := list.New(items, list.NewDefaultDelegate(), 30, 10)
	l.Title = "Packrat - Explore Mode"

	vp := viewport.New(80, 20)
	vp.Style = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(1)

	// Build mode list
	fileList := list.New([]list.Item{}, list.NewDefaultDelegate(), 30, 10)
	fileList.Title = "Packrat - Build Mode"

	// Build mode viewport
	buildVp := viewport.New(80, 20)
	buildVp.Style = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(1)

	// Text input for stash message
	ti := textinput.New()
	ti.Placeholder = "Enter stash message..."
	ti.Focus()
	ti.CharLimit = 200
	ti.Width = 50

	return model{
		stashList:     l,
		viewport:      vp,
		appState:      StateExplore,
		mode:          ModeExplore,
		err:           err,
		fileList:      fileList,
		selectedFiles: make(map[string]FileChange),
		expandedFiles: make(map[string]bool),
		fileDiffs:     make(map[string]string),
		buildViewport: buildVp,
		stashInput:    ti,
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

func listChangedFiles() ([]FileChange, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	var files []FileChange
	scanner := bufio.NewScanner(&out)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 4 {
			continue
		}

		// Git status --porcelain format: XY filename
		// X = staged status, Y = unstaged status
		stagedStatus := line[0:1]
		unstagedStatus := line[1:2]
		path := strings.TrimSpace(line[3:])

		// Add staged file if it has staged changes
		if stagedStatus != " " && stagedStatus != "?" {
			files = append(files, FileChange{
				Path:     path,
				Status:   stagedStatus,
				IsStaged: true,
			})
		}

		// Add unstaged file if it has unstaged changes
		if unstagedStatus != " " {
			files = append(files, FileChange{
				Path:     path,
				Status:   unstagedStatus,
				IsStaged: false,
			})
		}
	}
	return files, scanner.Err()
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

func getChangedFiles() tea.Cmd {
	return func() tea.Msg {
		files, err := listChangedFiles()
		return changedFilesMsg{files: files, err: err}
	}
}

func getFileDiff(file FileChange) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		if file.IsStaged {
			cmd = exec.Command("git", "-c", "color.ui=always", "diff", "--cached", "--", file.Path)
		} else {
			cmd = exec.Command("git", "-c", "color.ui=always", "diff", "--", file.Path)
		}
		out, err := cmd.CombinedOutput()
		return fileDiffMsg{path: file.Path, diff: string(out), err: err}
	}
}

func createStash(files []FileChange, message string) tea.Cmd {
	return func() tea.Msg {
		// Build the git stash push command with file paths
		args := []string{"stash", "push", "--include-untracked", "-m", message, "--"}
		for _, f := range files {
			args = append(args, f.Path)
		}

		cmd := exec.Command("git", args...)
		out, err := cmd.CombinedOutput()
		return stashCreatedMsg{output: string(out), err: err}
	}
}

func restoreWorkingDirectory() tea.Cmd {
	return func() tea.Msg {
		var output bytes.Buffer

		// First, restore all modified tracked files
		restoreCmd := exec.Command("git", "restore", ".")
		restoreOut, restoreErr := restoreCmd.CombinedOutput()
		output.Write(restoreOut)

		if restoreErr != nil {
			return workingDirectoryRestoredMsg{output: output.String(), err: restoreErr}
		}

		// Then, clean untracked files and directories
		cleanCmd := exec.Command("git", "clean", "-f", "-d")
		cleanOut, cleanErr := cleanCmd.CombinedOutput()
		output.Write(cleanOut)

		return workingDirectoryRestoredMsg{output: output.String(), err: cleanErr}
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

		// For the viewport, we need to account for the spacing (2 lines)
		const headerAndSpacing = 2
		viewportHeight := totalContentHeight - headerAndSpacing
		if viewportHeight < 0 {
			viewportHeight = 0
		}

		// Set the actual component sizes for both modes
		m.stashList.SetWidth(listContentWidth)
		m.stashList.SetHeight(totalContentHeight)
		m.viewport.Width = viewportContentWidth
		m.viewport.Height = viewportHeight

		m.fileList.SetWidth(listContentWidth)
		m.fileList.SetHeight(totalContentHeight)
		m.buildViewport.Width = viewportContentWidth
		m.buildViewport.Height = viewportHeight

	case tea.KeyMsg:
		switch {
		case msg.String() == "ctrl+c" || msg.String() == "q":
			if m.activeModal != ModalNone {
				m.activeModal = ModalNone
			} else {
				return m, tea.Quit
			}
		case msg.String() == "tab": // Got this idea from Opencode.ai, you should try Opencode yourself btw
			// Toggle between modes
			if m.mode == ModeExplore {
				m.mode = ModeBuild
				return m, getChangedFiles()
			} else {
				m.mode = ModeExplore
				// Clear build mode selections
				m.selectedFiles = make(map[string]FileChange)
				m.expandedFiles = make(map[string]bool)
				m.fileDiffs = make(map[string]string)
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
		case m.activeModal == ModalStashMessage:
			switch msg.String() {
			case "enter":
				message := m.stashInput.Value()
				if message != "" {
					m.activeModal = ModalNone
					m.loading = true
					// Convert selectedFiles map to slice
					files := make([]FileChange, 0, len(m.selectedFiles))
					for _, f := range m.selectedFiles {
						files = append(files, f)
					}
					m.stashInput.SetValue("") // Clear input
					return m, createStash(files, message)
				}
			case "esc":
				m.activeModal = ModalNone
				m.stashInput.SetValue("") // Clear input
			default:
				var cmd tea.Cmd
				m.stashInput, cmd = m.stashInput.Update(msg)
				return m, cmd
			}
		case m.activeModal == ModalRestoreConfirm:
			switch msg.String() {
			case "y", "Y":
				m.activeModal = ModalNone
				m.loading = true
				return m, restoreWorkingDirectory()
			case "n", "N", "esc":
				m.activeModal = ModalNone
			}
		default:
			if m.mode == ModeExplore {
				// Explore Mode key handlers
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
			} else if m.mode == ModeBuild {
				// Build Mode key handlers
				switch msg.String() {
				case "enter", " ": // Select/deselect a file or toggle expansion
					if sel, ok := m.fileList.SelectedItem().(FileChange); ok {
						key := sel.Path
						if _, exists := m.selectedFiles[key]; exists {
							// File already selected - treat space as toggle expansion
							if msg.String() == " " {
								m.expandedFiles[key] = !m.expandedFiles[key]
								m.buildViewport.SetContent(m.buildCollapsibleDiffsView())
								m.buildViewport.GotoTop()
							} else if msg.String() == "enter" {
								// Enter deselects
								delete(m.selectedFiles, key)
								delete(m.expandedFiles, key)
								delete(m.fileDiffs, key)
								m.buildViewport.SetContent(m.buildCollapsibleDiffsView())
								m.buildViewport.GotoTop()
							}
						} else {
							// File not selected - select it and fetch diff
							m.selectedFiles[key] = sel
							m.expandedFiles[key] = false // Start collapsed
							return m, getFileDiff(sel)
						}
					}
				case "s", "S": // Save stash (open modal)
					if len(m.selectedFiles) > 0 {
						m.stashInput.Focus()
						m.activeModal = ModalStashMessage
					}
				case "r", "R": // Restore working directory
					m.activeModal = ModalRestoreConfirm
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

	case changedFilesMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			items := make([]list.Item, len(msg.files))
			for i, f := range msg.files {
				items[i] = f
			}
			m.fileList.SetItems(items)
		}

	case fileDiffMsg:
		if msg.err != nil {
			m.fileDiffs[msg.path] = fmt.Sprintf("Error loading diff: %v", msg.err)
		} else {
			m.fileDiffs[msg.path] = msg.diff
		}
		m.buildViewport.SetContent(m.buildCollapsibleDiffsView())
		m.buildViewport.GotoTop()

	case stashCreatedMsg:
		m.loading = false
		if msg.err != nil {
			m.buildViewport.SetContent(fmt.Sprintf("Error creating stash:\n\n%s", msg.output))
		} else {
			// Success! Clear selections and return to Explore Mode
			m.selectedFiles = make(map[string]FileChange)
			m.expandedFiles = make(map[string]bool)
			m.fileDiffs = make(map[string]string)
			m.mode = ModeExplore

			// Refresh stash list
			stashes, err := listStashes()
			if err == nil {
				items := make([]list.Item, len(stashes))
				for i, s := range stashes {
					items[i] = s
				}
				m.stashList.SetItems(items)

				// Load the first stash's diff
				if len(stashes) > 0 {
					return m, getStashDiff(stashes[0].Ref)
				}
			}
		}

	case workingDirectoryRestoredMsg:
		m.loading = false
		if msg.err != nil {
			m.buildViewport.SetContent(fmt.Sprintf("Error restoring working directory:\n\n%s", msg.output))
		} else {
			// Success! Clear selections and refresh file list
			m.selectedFiles = make(map[string]FileChange)
			m.expandedFiles = make(map[string]bool)
			m.fileDiffs = make(map[string]string)

			// Show success message
			m.buildViewport.SetContent(fmt.Sprintf("Working directory restored successfully!\n\n%s", msg.output))
			m.buildViewport.GotoTop()

			// Refresh the file list (should be empty now)
			return m, getChangedFiles()
		}

	}

	// Update viewports and lists if no modal active
	if m.activeModal == ModalNone {
		if m.mode == ModeExplore {
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)

			m.stashList, cmd = m.stashList.Update(msg)
			cmds = append(cmds, cmd)
		} else if m.mode == ModeBuild {
			m.buildViewport, cmd = m.buildViewport.Update(msg)
			cmds = append(cmds, cmd)

			m.fileList, cmd = m.fileList.Update(msg)
			cmds = append(cmds, cmd)
		}
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

func (m model) buildCollapsibleDiffsView() string {
	if len(m.selectedFiles) == 0 {
		return "No files selected.\n\nSelect files from the list to see their diffs here.\n[Enter] Select file  [Space] Expand/collapse diff  [s] Create stash"
	}

	var content strings.Builder
	content.WriteString(fmt.Sprintf("Selected files: %d\n\n", len(m.selectedFiles)))

	// Sort files for consistent display
	var sortedPaths []string
	for path := range m.selectedFiles {
		sortedPaths = append(sortedPaths, path)
	}

	for _, path := range sortedPaths {
		file := m.selectedFiles[path]
		expanded := m.expandedFiles[path]

		// Show collapse/expand indicator
		indicator := "▶"
		if expanded {
			indicator = "▼"
		}

		statusStr := "unstaged"
		if file.IsStaged {
			statusStr = "staged"
		}

		content.WriteString(fmt.Sprintf("%s %s (%s)\n", indicator, path, statusStr))

		if expanded {
			diff, exists := m.fileDiffs[path]
			if exists {
				content.WriteString(diff)
			} else {
				content.WriteString("  Loading diff...\n")
			}
			content.WriteString("\n")
		}
	}

	return content.String()
}

func (m model) renderModal() string {
	switch m.activeModal {
	case ModalDeleteConfirm:
		return modalStyle.Render(fmt.Sprintf("Delete %s?\n\n[y] Yes   [n] No", m.selectedRef))
	case ModalApplyConfirm:
		return modalStyle.Render(fmt.Sprintf("Apply %s?\n\n[y] Yes   [n] No", m.selectedRef))
	case ModalStashMessage:
		content := fmt.Sprintf("Create Stash\n\n%s\n\n[Enter] Save   [Esc] Cancel", m.stashInput.View())
		return modalStyle.Render(content)
	case ModalRestoreConfirm:
		warning := "⚠️  WARNING ⚠️\n\n"
		warning += "This will restore your working directory to a clean state.\n"
		warning += "All staged and unstaged changes will be LOST!\n"
		warning += "Untracked files will be DELETED!\n\n"
		warning += "Are you sure?\n\n"
		warning += "[y] Yes   [n] No"
		return modalStyle.Render(warning)
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

	if m.mode == ModeExplore {
		// Explore Mode view
		leftPane := borderStyle.Render(m.stashList.View())

		header := titleStyle.Render("[Enter] Show stash  [a] Apply  [d] Drop  [Tab] Build Mode  [q] Quit  [↑/↓] Scroll")
		viewportContent := m.viewport.View()

		rightContent := header + "\n\n" + viewportContent
		rightPane := borderStyle.Render(rightContent)

		return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
	} else {
		// Build Mode view
		leftPane := borderStyle.Render(m.fileList.View())

		selectedCount := len(m.selectedFiles)
		helpText := fmt.Sprintf("[Enter] Select  [Space] Expand/Collapse  [s] Save (%d)  [r] Restore  [Tab] Explore  [q] Quit", selectedCount)
		header := titleStyle.Render(helpText)
		viewportContent := m.buildViewport.View()

		rightContent := header + "\n\n" + viewportContent
		rightPane := borderStyle.Render(rightContent)

		return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
	}
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
