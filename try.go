package main

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

// UI module - lightweight token-based printer with double buffering
type UI struct {
	buffer      []string
	lastBuffer  []string
	currentLine string
}

var ui = &UI{}

var tokenMap = map[string]string{
	"{text}":           "\x1b[39m",
	"{dim_text}":       "\x1b[90m",
	"{h1}":             "\x1b[1;33m",
	"{h2}":             "\x1b[1;36m",
	"{highlight}":      "\x1b[1;33m",
	"{reset}":          "\x1b[0m\x1b[39m\x1b[49m",
	"{reset_bg}":       "\x1b[49m",
	"{reset_fg}":       "\x1b[39m",
	"{clear_screen}":   "\x1b[2J",
	"{clear_line}":     "\x1b[2K",
	"{home}":           "\x1b[H",
	"{clear_below}":    "\x1b[0J",
	"{hide_cursor}":    "\x1b[?25l",
	"{show_cursor}":    "\x1b[?25h",
	"{start_selected}": "\x1b[1m",
	"{end_selected}":   "\x1b[0m",
	"{bold}":           "\x1b[1m",
}

func (u *UI) Print(text string, w io.Writer) {
	if text == "" {
		return
	}
	u.currentLine += text
}

func (u *UI) Println(text string, w io.Writer) {
	u.currentLine += text
	u.buffer = append(u.buffer, u.currentLine)
	u.currentLine = ""
}

func (u *UI) Flush(w io.Writer) {
	// Always finalize into the buffer
	if u.currentLine != "" {
		u.buffer = append(u.buffer, u.currentLine)
		u.currentLine = ""
	}

	// In non-TTY contexts, print plain text without control codes or tokens
	if f, ok := w.(*os.File); ok && !term.IsTerminal(int(f.Fd())) {
		plain := strings.Join(u.buffer, "\n")
		plain = regexp.MustCompile(`\{.*?\}`).ReplaceAllString(plain, "")
		fmt.Fprint(w, plain)
		if !strings.HasSuffix(plain, "\n") {
			fmt.Fprintln(w)
		}
		u.lastBuffer = []string{}
		u.buffer = []string{}
		u.currentLine = ""
		if flusher, ok := w.(interface{ Flush() error }); ok {
			flusher.Flush()
		}
		return
	}

	// Position cursor at home for TTY
	fmt.Fprint(w, "\x1b[H")

	maxLines := len(u.buffer)
	if len(u.lastBuffer) > maxLines {
		maxLines = len(u.lastBuffer)
	}
	reset := tokenMap["{reset}"]

	for i := 0; i < maxLines; i++ {
		currentLine := ""
		if i < len(u.buffer) {
			currentLine = u.buffer[i]
		}
		lastLine := ""
		if i < len(u.lastBuffer) {
			lastLine = u.lastBuffer[i]
		}

		if currentLine != lastLine {
			// Move to line and clear it, then write new content
			fmt.Fprintf(w, "\x1b[%d;1H\x1b[2K", i+1)
			if currentLine != "" {
				processedLine := expandTokens(currentLine)
				fmt.Fprint(w, processedLine)
				fmt.Fprint(w, reset)
			}
		}
	}

	// Store current buffer as last buffer for next comparison
	u.lastBuffer = make([]string, len(u.buffer))
	copy(u.lastBuffer, u.buffer)
	u.buffer = []string{}
	u.currentLine = ""

	if flusher, ok := w.(interface{ Flush() error }); ok {
		flusher.Flush()
	}
}

func (u *UI) ClearScreen(w io.Writer) {
	u.currentLine = ""
	u.buffer = []string{}
	u.lastBuffer = []string{}
	fmt.Fprint(w, "\x1b[2J\x1b[H")
}

func readKey() string {
	oldState, err := term.MakeRaw(int(syscall.Stdin))
	if err != nil {
		return ""
	}
	defer term.Restore(int(syscall.Stdin), oldState)

	input := make([]byte, 1)
	n, err := os.Stdin.Read(input)
	if err != nil || n == 0 {
		return ""
	}

	result := string(input[0])
	if result == "\x1b" {
		// Try to read more for escape sequences
		extra := make([]byte, 3)
		os.Stdin.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
		n, _ := os.Stdin.Read(extra)
		if n > 0 {
			result += string(extra[:n])
		}
		os.Stdin.SetReadDeadline(time.Time{})
	}

	return result
}

func getTerminalSize() (int, int) {
	cmd := exec.Command("tput", "lines")
	cmd.Stdin = os.Stdin
	out, _ := cmd.Output()
	h, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	if h <= 0 {
		h = 24
	}

	cmd = exec.Command("tput", "cols")
	cmd.Stdin = os.Stdin
	out, _ = cmd.Output()
	w, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	if w <= 0 {
		w = 80
	}

	return w, h
}

func expandTokens(str string) string {
	re := regexp.MustCompile(`\{.*?\}`)
	return re.ReplaceAllStringFunc(str, func(match string) string {
		if val, ok := tokenMap[match]; ok {
			return val
		}
		panic(fmt.Sprintf("Unknown token: %s", match))
	})
}

// TrySelector - main interactive selector
type TrySelector struct {
	searchTerm     string
	basePath       string
	inputBuffer    string
	cursorPos      int
	scrollOffset   int
	selected       map[string]interface{}
	allTries       []map[string]interface{}
	deleteStatus   string
	testRenderOnce bool
	testNoCLS      bool
	testKeys       []string
	testConfirm    string
}

const defaultTryPath = "~/src/tries"
const defaultPromotePath = "~/dev"

func NewTrySelector(searchTerm string, basePath string, initialInput string, testRenderOnce bool, testNoCLS bool, testKeys []string, testConfirm string) *TrySelector {
	if basePath == "" {
		basePath = os.ExpandEnv(defaultTryPath)
	}

	searchTerm = regexp.MustCompile(`\s+`).ReplaceAllString(searchTerm, "-")
	inputBuffer := initialInput
	if inputBuffer == "" {
		inputBuffer = searchTerm
	}

	// Ensure base path exists
	os.MkdirAll(basePath, 0755)

	return &TrySelector{
		searchTerm:     searchTerm,
		basePath:       basePath,
		inputBuffer:    inputBuffer,
		cursorPos:      0,
		scrollOffset:   0,
		selected:       nil,
		allTries:       nil,
		deleteStatus:   "",
		testRenderOnce: testRenderOnce,
		testNoCLS:      testNoCLS,
		testKeys:       testKeys,
		testConfirm:    testConfirm,
	}
}

func (ts *TrySelector) Run() map[string]interface{} {
	// Always use STDERR for UI (it stays connected to TTY)
	// This allows stdout to be captured for the shell commands
	ts.setupTerminal()

	// In test mode, render once and exit without TTY requirements
	if ts.testRenderOnce {
		tries := ts.getTries()
		ts.render(tries)
		return nil
	}

	// Check if we have a TTY; allow tests with injected keys
	if !term.IsTerminal(int(syscall.Stdin)) || !term.IsTerminal(int(syscall.Stderr)) {
		if ts.testKeys == nil || len(ts.testKeys) == 0 {
			ui.Println("Error: try requires an interactive terminal", os.Stderr)
			ui.Flush(os.Stderr)
			return nil
		}
		ts.mainLoop()
	} else {
		ts.mainLoop()
	}

	defer ts.restoreTerminal()

	return ts.selected
}

func (ts *TrySelector) setupTerminal() {
	if !ts.testNoCLS {
		ui.ClearScreen(os.Stderr)
		fmt.Fprint(os.Stderr, "\x1b[2J\x1b[H\x1b[?25l")
	}
}

func (ts *TrySelector) restoreTerminal() {
	if !ts.testNoCLS {
		fmt.Fprint(os.Stderr, "\x1b[2J\x1b[H\x1b[?25h")
	}
}

func (ts *TrySelector) loadAllTries() []map[string]interface{} {
	if ts.allTries != nil {
		return ts.allTries
	}

	entries, err := os.ReadDir(ts.basePath)
	if err != nil {
		ts.allTries = []map[string]interface{}{}
		return ts.allTries
	}

	tries := []map[string]interface{}{}
	for _, entry := range entries {
		if entry.Name() == "." || entry.Name() == ".." {
			continue
		}

		path := filepath.Join(ts.basePath, entry.Name())
		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		if !info.IsDir() {
			continue
		}

		tries = append(tries, map[string]interface{}{
			"name":     "📁 " + entry.Name(),
			"basename": entry.Name(),
			"path":     path,
			"is_new":   false,
			"ctime":    info.ModTime(),
			"mtime":    info.ModTime(),
		})
	}

	ts.allTries = tries
	return tries
}

func (ts *TrySelector) getTries() []map[string]interface{} {
	ts.loadAllTries()

	// Always score trials (for time-based sorting even without search)
	scoredTries := make([]map[string]interface{}, len(ts.allTries))
	for i, tryDir := range ts.allTries {
		basename := tryDir["basename"].(string)
		ctime := tryDir["ctime"].(time.Time)
		mtime := tryDir["mtime"].(time.Time)
		score := ts.calculateScore(basename, ts.inputBuffer, ctime, mtime)
		scoredTry := make(map[string]interface{})
		for k, v := range tryDir {
			scoredTry[k] = v
		}
		scoredTry["score"] = score
		scoredTries[i] = scoredTry
	}

	// Filter only if searching, otherwise show all
	if ts.inputBuffer == "" {
		sort.Slice(scoredTries, func(i, j int) bool {
			return scoredTries[i]["score"].(float64) > scoredTries[j]["score"].(float64)
		})
		return scoredTries
	}

	// When searching, only show matches
	filtered := []map[string]interface{}{}
	for _, t := range scoredTries {
		if t["score"].(float64) > 0 {
			filtered = append(filtered, t)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i]["score"].(float64) > filtered[j]["score"].(float64)
	})
	return filtered
}

func (ts *TrySelector) calculateScore(text string, query string, ctime time.Time, mtime time.Time) float64 {
	score := 0.0

	// Generally we are looking for default date-prefixed directories
	matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}-`, text)
	if matched {
		score += 2.0
	}

	// If there's a search query, calculate match score
	if query != "" {
		textLower := strings.ToLower(text)
		queryLower := strings.ToLower(query)
		queryChars := []rune(queryLower)

		lastPos := -1
		queryIdx := 0

		for pos, char := range textLower {
			if queryIdx >= len(queryChars) {
				break
			}
			if char != queryChars[queryIdx] {
				continue
			}

			// Base point + word boundary bonus
			score += 1.0
			if pos == 0 || !isWordChar(rune(textLower[pos-1])) {
				score += 1.0
			}

			// Proximity bonus: 1/sqrt(distance) gives nice decay
			if lastPos >= 0 {
				gap := pos - lastPos - 1
				score += 1.0 / math.Sqrt(float64(gap+1))
			}

			lastPos = pos
			queryIdx++
		}

		// Return 0 if not all query chars matched
		if queryIdx < len(queryChars) {
			return 0.0
		}

		// Prefer shorter matches (density bonus)
		if lastPos >= 0 {
			score *= float64(len(queryChars)) / float64(lastPos+1)
		}

		// Length penalty - shorter text scores higher for same match
		score *= 10.0 / (float64(len(text)) + 10.0)
	}

	// Always apply time-based scoring
	now := time.Now()

	// Creation time bonus - newer is better
	if !ctime.IsZero() {
		daysOld := now.Sub(ctime).Hours() / 24.0
		score += 2.0 / math.Sqrt(daysOld+1)
	}

	// Access time bonus - recently accessed is better
	if !mtime.IsZero() {
		hoursSinceAccess := now.Sub(mtime).Hours()
		score += 3.0 / math.Sqrt(hoursSinceAccess+1)
	}

	return score
}

func isWordChar(char rune) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_'
}

func (ts *TrySelector) mainLoop() {
	for {
		tries := ts.getTries()
		totalItems := len(tries) + 1 // +1 for "Create new" option

		// Ensure cursor is within bounds
		if ts.cursorPos < 0 {
			ts.cursorPos = 0
		}
		if ts.cursorPos >= totalItems {
			ts.cursorPos = totalItems - 1
		}

		ts.render(tries)

		key := ts.readKey()

		switch key {
		case "\r": // Enter
			if ts.cursorPos < len(tries) {
				ts.handleSelection(tries[ts.cursorPos])
			} else {
				ts.handleCreateNew()
			}
			if ts.selected != nil {
				return
			}
		case "\x1b[A", "\x10", "\x0B": // Up arrow or Ctrl-P or Ctrl-K
			if ts.cursorPos > 0 {
				ts.cursorPos--
			}
		case "\x1b[B", "\x0E", "\n": // Down arrow or Ctrl-N or Ctrl-J
			if ts.cursorPos < totalItems-1 {
				ts.cursorPos++
			}
		case "\x1b[C": // Right arrow
			// Do nothing
		case "\x1b[D": // left arrow
			// Do nothing
		case "\x7F", "\b": // Backspace
			if len(ts.inputBuffer) > 0 {
				ts.inputBuffer = ts.inputBuffer[:len(ts.inputBuffer)-1]
			}
			ts.cursorPos = 0
		case "\x04": // Ctrl-D
			if ts.cursorPos < len(tries) {
				ts.handleDelete(tries[ts.cursorPos])
			}
		case "\x12": // Ctrl-R
			if ts.cursorPos < len(tries) {
				ts.handlePromote(tries[ts.cursorPos])
				if ts.selected != nil {
					return
				}
			}
		case "\x03", "\x1b": // Ctrl-C or ESC
			ts.selected = nil
			return
		default:
			// Only accept printable characters, not escape sequences
			if len(key) == 1 {
				r := rune(key[0])
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == ' ' {
					ts.inputBuffer += key
					ts.cursorPos = 0
				}
			}
		}
	}
}

func (ts *TrySelector) readKey() string {
	if ts.testKeys != nil && len(ts.testKeys) > 0 {
		key := ts.testKeys[0]
		ts.testKeys = ts.testKeys[1:]
		return key
	}
	return readKey()
}

func (ts *TrySelector) render(tries []map[string]interface{}) {
	termWidth, termHeight := getTerminalSize()

	// Use actual terminal width for separator lines
	separator := strings.Repeat("─", termWidth-1)

	// Header
	ui.Println("{h1}📁 Try Directory Selection", os.Stderr)
	ui.Println("{dim_text}"+separator, os.Stderr)

	// Search input
	ui.Println("{highlight}Search: {reset}"+ts.inputBuffer, os.Stderr)
	ui.Println("{dim_text}"+separator, os.Stderr)

	// Calculate visible window based on actual terminal height
	maxVisible := termHeight - 8
	if maxVisible < 3 {
		maxVisible = 3
	}
	totalItems := len(tries) + 1 // +1 for "Create new"

	// Adjust scroll window
	if ts.cursorPos < ts.scrollOffset {
		ts.scrollOffset = ts.cursorPos
	} else if ts.cursorPos >= ts.scrollOffset+maxVisible {
		ts.scrollOffset = ts.cursorPos - maxVisible + 1
	}

	// Display items
	visibleEnd := ts.scrollOffset + maxVisible
	if visibleEnd > totalItems {
		visibleEnd = totalItems
	}

	for idx := ts.scrollOffset; idx < visibleEnd; idx++ {
		// Add blank line before "Create new"
		if idx == len(tries) && len(tries) > 0 && idx >= ts.scrollOffset {
			ui.Println("", os.Stderr)
		}

		// Print cursor/selection indicator
		isSelected := idx == ts.cursorPos
		if isSelected {
			ui.Print("{highlight}→ {reset_fg}", os.Stderr)
		} else {
			ui.Print("  ", os.Stderr)
		}

		// Display try directory or "Create new" option
		if idx < len(tries) {
			tryDir := tries[idx]

			// Render the folder icon (always outside selection)
			ui.Print("📁 ", os.Stderr)

			// Start selection highlighting after icon
			if isSelected {
				ui.Print("{start_selected}", os.Stderr)
			}

			basename := tryDir["basename"].(string)
			re := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})-(.+)$`)
			matches := re.FindStringSubmatch(basename)

			var displayText string
			if len(matches) == 3 {
				datePart := matches[1]
				namePart := matches[2]

				// Render the date part (faint)
				ui.Print("{dim_text}"+datePart+"{reset_fg}", os.Stderr)

				// Render the separator
				separatorMatches := ts.inputBuffer != "" && strings.Contains(ts.inputBuffer, "-")
				if separatorMatches {
					ui.Print("{highlight}-{reset_fg}", os.Stderr)
				} else {
					ui.Print("{dim_text}-{reset_fg}", os.Stderr)
				}

				// Render the name part with match highlighting
				if ts.inputBuffer != "" {
					ui.Print(ts.highlightMatchesForSelection(namePart, ts.inputBuffer, isSelected), os.Stderr)
				} else {
					ui.Print(namePart, os.Stderr)
				}

				displayText = datePart + "-" + namePart
			} else {
				// No date prefix - render folder icon then content
				if ts.inputBuffer != "" {
					ui.Print(ts.highlightMatchesForSelection(basename, ts.inputBuffer, isSelected), os.Stderr)
				} else {
					ui.Print(basename, os.Stderr)
				}
				displayText = basename
			}

			// Format score and time for display
			timeText := ts.formatRelativeTime(tryDir["mtime"].(time.Time))
			scoreText := fmt.Sprintf("%.1f", tryDir["score"].(float64))
			metaText := timeText + ", " + scoreText

			// Calculate padding
			metaWidth := len(metaText) + 1
			textWidth := len(displayText)
			paddingNeeded := termWidth - 5 - textWidth - metaWidth
			if paddingNeeded < 1 {
				paddingNeeded = 1
			}
			padding := strings.Repeat(" ", paddingNeeded)

			// Print padding and metadata
			ui.Print(padding, os.Stderr)
			if isSelected {
				ui.Print("{end_selected}", os.Stderr)
			}
			ui.Print(" {dim_text}"+metaText+"{reset_fg}", os.Stderr)
		} else {
			// This is the "Create new" option
			ui.Print("+ ", os.Stderr) // Plus sign outside selection

			if isSelected {
				ui.Print("{start_selected}", os.Stderr)
			}

			displayText := "Create new"
			if ts.inputBuffer != "" {
				displayText = "Create new: " + ts.inputBuffer
			}

			ui.Print(displayText, os.Stderr)

			// Pad to full width
			textWidth := len(displayText)
			paddingNeeded := termWidth - 5 - textWidth
			if paddingNeeded < 1 {
				paddingNeeded = 1
			}
			ui.Print(strings.Repeat(" ", paddingNeeded), os.Stderr)
		}

		// End selection and reset all formatting
		ui.Println("", os.Stderr)
	}

	// Scroll indicator if needed
	if totalItems > maxVisible {
		ui.Println("{dim_text}"+separator, os.Stderr)
		ui.Println(fmt.Sprintf("{dim_text}[%d-%d/%d]", ts.scrollOffset+1, visibleEnd, totalItems), os.Stderr)
	}

	// Instructions at bottom
	ui.Println("{dim_text}"+separator, os.Stderr)

	// Show delete status if present, otherwise show instructions
	if ts.deleteStatus != "" {
		ui.Println("{highlight}"+ts.deleteStatus+"{reset}", os.Stderr)
		ts.deleteStatus = ""
	} else {
		ui.Println("{dim_text}↑↓/Ctrl-P,N,J,K: Navigate  Enter: Select  Ctrl-D: Delete  Ctrl-R: Promote  ESC: Cancel{reset}", os.Stderr)
	}

	// Flush the double buffer
	ui.Flush(os.Stderr)
}

func (ts *TrySelector) formatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return "?"
	}

	seconds := time.Since(t).Seconds()
	minutes := seconds / 60
	hours := minutes / 60
	days := hours / 24

	if seconds < 10 {
		return "just now"
	} else if minutes < 60 {
		return fmt.Sprintf("%dm ago", int(minutes))
	} else if hours < 24 {
		return fmt.Sprintf("%dh ago", int(hours))
	} else if days < 30 {
		return fmt.Sprintf("%dd ago", int(days))
	} else if days < 365 {
		return fmt.Sprintf("%dmo ago", int(days/30))
	}
	return fmt.Sprintf("%dy ago", int(days/365))
}

func (ts *TrySelector) highlightMatches(text string, query string) string {
	if query == "" {
		return text
	}

	result := ""
	textLower := strings.ToLower(text)
	queryLower := strings.ToLower(query)
	queryChars := []rune(queryLower)
	queryIndex := 0

	for i, char := range text {
		if queryIndex < len(queryChars) && rune(textLower[i]) == queryChars[queryIndex] {
			result += "{highlight}" + string(char) + "{text}"
			queryIndex++
		} else {
			result += string(char)
		}
	}

	return result
}

func (ts *TrySelector) highlightMatchesForSelection(text string, query string, isSelected bool) string {
	if query == "" {
		return text
	}

	result := ""
	textLower := strings.ToLower(text)
	queryLower := strings.ToLower(query)
	queryChars := []rune(queryLower)
	queryIndex := 0

	for i, char := range text {
		if queryIndex < len(queryChars) && rune(textLower[i]) == queryChars[queryIndex] {
			result += "{highlight}" + string(char) + "{text}"
			queryIndex++
		} else {
			result += string(char)
		}
	}

	return result
}

func (ts *TrySelector) handleSelection(tryDir map[string]interface{}) {
	ts.selected = map[string]interface{}{
		"type": "cd",
		"path": tryDir["path"].(string),
	}
}

func (ts *TrySelector) handlePromote(tryDir map[string]interface{}) {
	path := tryDir["path"].(string)
	basename := tryDir["basename"].(string)
	defaultDst := defaultPromoteDestination(basename)

	ui.ClearScreen(os.Stderr)
	ui.Println("{h2}Promote Directory", os.Stderr)
	ui.Println("", os.Stderr)
	ui.Println("Copy this try into a dev folder with rsync:", os.Stderr)
	ui.Println(fmt.Sprintf("  {dim_text}from %s{reset}", path), os.Stderr)
	ui.Println(fmt.Sprintf("  {dim_text}to   %s{reset}", defaultDst), os.Stderr)
	ui.Println("", os.Stderr)
	ui.Println("Enter destination path, or leave blank to use the default:", os.Stderr)
	ui.Println("> ", os.Stderr)
	ui.Flush(os.Stderr)
	fmt.Fprint(os.Stderr, "\x1b[?25h")

	reader := bufio.NewReader(os.Stdin)
	dst, _ := reader.ReadString('\n')
	dst = strings.TrimSpace(dst)
	if dst == "" {
		dst = defaultDst
	} else {
		dst = expandPath(dst)
		if abs, err := filepath.Abs(dst); err == nil {
			dst = abs
		}
	}

	ts.selected = map[string]interface{}{
		"type": "promote",
		"path": dst,
		"src":  path,
	}
}

func (ts *TrySelector) handleCreateNew() {
	datePrefix := time.Now().Format("2006-01-02")

	// If user already typed a name, use it directly
	if ts.inputBuffer != "" {
		finalName := datePrefix + "-" + regexp.MustCompile(`\s+`).ReplaceAllString(ts.inputBuffer, "-")
		fullPath := filepath.Join(ts.basePath, finalName)
		ts.selected = map[string]interface{}{
			"type": "mkdir",
			"path": fullPath,
		}
	} else {
		// No name typed, prompt for one
		ui.ClearScreen(os.Stderr)
		ui.Println("{h2}Enter new try name", os.Stderr)
		ui.Println("", os.Stderr)
		ui.Println("> {dim_text}"+datePrefix+"-{reset}", os.Stderr)
		ui.Flush(os.Stderr)
		fmt.Fprint(os.Stderr, "\x1b[?25h")

		// Read user input
		reader := bufio.NewReader(os.Stdin)
		entry, _ := reader.ReadString('\n')
		entry = strings.TrimSpace(entry)

		if entry == "" {
			ts.selected = map[string]interface{}{
				"type": "cancel",
				"path": nil,
			}
			return
		}

		finalName := datePrefix + "-" + regexp.MustCompile(`\s+`).ReplaceAllString(entry, "-")
		fullPath := filepath.Join(ts.basePath, finalName)

		ts.selected = map[string]interface{}{
			"type": "mkdir",
			"path": fullPath,
		}
	}
}

func (ts *TrySelector) handleDelete(tryDir map[string]interface{}) {
	// Show delete confirmation dialog
	path := tryDir["path"].(string)
	basename := tryDir["basename"].(string)

	// Get size and file count
	cmd := exec.Command("du", "-sh", path)
	sizeOut, _ := cmd.Output()
	size := strings.Fields(string(sizeOut))[0]

	cmd = exec.Command("sh", "-c", fmt.Sprintf("find %s -type f | wc -l", path))
	filesOut, _ := cmd.Output()
	files := strings.TrimSpace(string(filesOut))

	ui.ClearScreen(os.Stderr)
	ui.Println("{h2}Delete Directory", os.Stderr)
	ui.Println("", os.Stderr)
	ui.Println(fmt.Sprintf("Are you sure you want to delete: {highlight}%s{reset}", basename), os.Stderr)
	ui.Println(fmt.Sprintf("  {dim_text}in %s{reset}", path), os.Stderr)
	ui.Println(fmt.Sprintf("  {dim_text}files: %s files{reset}", files), os.Stderr)
	ui.Println(fmt.Sprintf("  {dim_text}size: %s{reset}", size), os.Stderr)
	ui.Println("", os.Stderr)
	ui.Println("{highlight}Type {text}YES{highlight} to confirm: ", os.Stderr)
	ui.Flush(os.Stderr)
	fmt.Fprint(os.Stderr, "\x1b[?25h")

	// Confirmation input
	var confirmation string
	if ts.testConfirm != "" || !term.IsTerminal(int(syscall.Stderr)) {
		if ts.testConfirm != "" {
			confirmation = ts.testConfirm
		} else {
			reader := bufio.NewReader(os.Stdin)
			confirmation, _ = reader.ReadString('\n')
			confirmation = strings.TrimSpace(confirmation)
		}
	} else {
		reader := bufio.NewReader(os.Stdin)
		confirmation, _ = reader.ReadString('\n')
		confirmation = strings.TrimSpace(confirmation)
	}

	if confirmation == "YES" {
		err := os.RemoveAll(path)
		if err != nil {
			ts.deleteStatus = fmt.Sprintf("Error: %s", err.Error())
		} else {
			ts.deleteStatus = fmt.Sprintf("Deleted: %s", basename)
			ts.allTries = nil // Clear cache
		}
	} else {
		ts.deleteStatus = "Delete cancelled"
	}

	// Hide cursor again for main UI
	fmt.Fprint(os.Stderr, "\x1b[?25l")
}

// Command handlers
func cmdClone(args []string, triesPath string) []map[string]string {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: git URI required for clone command")
		fmt.Fprintln(os.Stderr, "Usage: try clone <git-uri> [name]")
		os.Exit(1)
	}

	gitURI := args[0]
	customName := ""
	if len(args) > 1 {
		customName = args[1]
	}

	dirName := generateCloneDirectoryName(gitURI, customName)
	if dirName == "" {
		fmt.Fprintf(os.Stderr, "Error: Unable to parse git URI: %s\n", gitURI)
		os.Exit(1)
	}

	fullPath := filepath.Join(triesPath, dirName)
	return []map[string]string{
		{"type": "target", "path": fullPath},
		{"type": "mkdir"},
		{"type": "echo", "msg": "Using {highlight}git clone{reset_fg} to create this trial from " + gitURI + "."},
		{"type": "git-clone", "uri": gitURI},
		{"type": "touch"},
		{"type": "cd"},
	}
}

func cmdInit(args []string, triesPath string) {
	executable, _ := os.Executable()

	if len(args) > 0 && strings.HasPrefix(args[0], "/") {
		triesPath = args[0]
	}

	pathArg := ""
	if triesPath != "" {
		pathArg = fmt.Sprintf(` --path "%s"`, triesPath)
	}

	bashScript := fmt.Sprintf(`try() {
  script_path='%s'
  case "$1" in
    clone|worktree|init)
      cmd=$(%s%s "$@" 2>/dev/tty)
      ;;
    *)
      cmd=$(%s cd%s "$@" 2>/dev/tty)
      ;;
  esac
  rc=$?
  if [ $rc -eq 0 ]; then
    case "$cmd" in
      *" && "*) eval "$cmd" ;;
      *) printf %%s "$cmd" ;;
    esac
  else
    printf %%s "$cmd"
  fi
}`, executable, executable, pathArg, executable, pathArg)

	fishScript := fmt.Sprintf(`function try
  set -l script_path "%s"
  switch $argv[1]
    case clone worktree init
      set -l cmd (%s%s $argv 2>/dev/tty | string collect)
    case '*'
      set -l cmd (%s cd%s $argv 2>/dev/tty | string collect)
  end
  set -l rc $status
  if test $rc -eq 0
    if string match -r ' && ' -- $cmd
      eval $cmd
    else
      printf %%s $cmd
    end
  else
    printf %%s $cmd
  end
end`, executable, executable, pathArg, executable, pathArg)

	if isFishShell() {
		fmt.Println(fishScript)
	} else {
		fmt.Println(bashScript)
	}
	os.Exit(0)
}

func cmdCD(args []string, triesPath string, andType string, andExit bool, andKeys []string, andConfirm string) []map[string]string {
	if len(args) > 0 && args[0] == "clone" {
		return cmdClone(args[1:], triesPath)
	}

	// Support: try . [name] and try ./path [name]
	if len(args) > 0 && strings.HasPrefix(args[0], ".") {
		pathArg := args[0]
		custom := strings.Join(args[1:], " ")
		repoDir, _ := filepath.Abs(pathArg)
		base := custom
		if base == "" {
			base = filepath.Base(repoDir)
		}
		base = regexp.MustCompile(`\s+`).ReplaceAllString(base, "-")
		datePrefix := time.Now().Format("2006-01-02")
		base = resolveUniqueNameWithVersioning(triesPath, datePrefix, base)
		dirName := datePrefix + "-" + base
		fullPath := filepath.Join(triesPath, dirName)

		tasks := []map[string]string{
			{"type": "target", "path": fullPath},
			{"type": "mkdir"},
		}

		// Only add worktree when a .git directory exists at that path
		if _, err := os.Stat(filepath.Join(repoDir, ".git")); err == nil {
			tasks = append(tasks, map[string]string{
				"type": "echo",
				"msg":  "Using {highlight}git worktree{reset_fg} to create this trial from " + repoDir + ".",
			})
			tasks = append(tasks, map[string]string{
				"type": "git-worktree",
				"repo": repoDir,
			})
		}

		tasks = append(tasks,
			map[string]string{"type": "touch"},
			map[string]string{"type": "cd"},
		)
		return tasks
	}

	searchTerm := strings.Join(args, " ")

	// Git URL shorthand → clone workflow
	if len(args) > 0 && isGitURI(args[0]) {
		parts := strings.Fields(searchTerm)
		gitURI := parts[0]
		customName := ""
		if len(parts) > 1 {
			customName = strings.Join(parts[1:], " ")
		}
		dirName := generateCloneDirectoryName(gitURI, customName)
		if dirName == "" {
			fmt.Fprintf(os.Stderr, "Error: Unable to parse git URI: %s\n", gitURI)
			os.Exit(1)
		}
		fullPath := filepath.Join(triesPath, dirName)
		return []map[string]string{
			{"type": "target", "path": fullPath},
			{"type": "mkdir"},
			{"type": "echo", "msg": "Using {highlight}git clone{reset_fg} to create this trial from " + gitURI + "."},
			{"type": "git-clone", "uri": gitURI},
			{"type": "touch"},
			{"type": "cd"},
		}
	}

	// Regular interactive selector
	selector := NewTrySelector(
		searchTerm,
		triesPath,
		andType,
		andExit,
		andExit || (andKeys != nil && len(andKeys) > 0),
		andKeys,
		andConfirm,
	)

	if andExit {
		selector.Run()
		os.Exit(0)
	}

	result := selector.Run()
	if result == nil {
		return nil
	}

	if result["type"] == "promote" {
		return []map[string]string{
			{"type": "target", "path": result["path"].(string)},
			{"type": "mkdir"},
			{"type": "rsync", "src": result["src"].(string)},
			{"type": "cd"},
		}
	}

	tasks := []map[string]string{
		{"type": "target", "path": result["path"].(string)},
	}

	if result["type"] == "mkdir" {
		tasks = append(tasks, map[string]string{"type": "mkdir"})
	}

	tasks = append(tasks,
		map[string]string{"type": "touch"},
		map[string]string{"type": "cd"},
	)

	return tasks
}

func parseGitURI(uri string) map[string]string {
	// Remove .git suffix if present
	uri = strings.TrimSuffix(uri, ".git")

	result := make(map[string]string)

	// Handle different git URI formats
	re := regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+)`)
	if matches := re.FindStringSubmatch(uri); matches != nil {
		result["user"] = matches[1]
		result["repo"] = matches[2]
		result["host"] = "github.com"
		return result
	}

	re = regexp.MustCompile(`^git@github\.com:([^/]+)/([^/]+)`)
	if matches := re.FindStringSubmatch(uri); matches != nil {
		result["user"] = matches[1]
		result["repo"] = matches[2]
		result["host"] = "github.com"
		return result
	}

	re = regexp.MustCompile(`^https?://([^/]+)/([^/]+)/([^/]+)`)
	if matches := re.FindStringSubmatch(uri); matches != nil {
		result["host"] = matches[1]
		result["user"] = matches[2]
		result["repo"] = matches[3]
		return result
	}

	re = regexp.MustCompile(`^git@([^:]+):([^/]+)/([^/]+)`)
	if matches := re.FindStringSubmatch(uri); matches != nil {
		result["host"] = matches[1]
		result["user"] = matches[2]
		result["repo"] = matches[3]
		return result
	}

	return nil
}

func generateCloneDirectoryName(gitURI string, customName string) string {
	if customName != "" {
		return customName
	}

	parsed := parseGitURI(gitURI)
	if parsed == nil {
		return ""
	}

	datePrefix := time.Now().Format("2006-01-02")
	return datePrefix + "-" + parsed["user"] + "-" + parsed["repo"]
}

func isGitURI(arg string) bool {
	if arg == "" {
		return false
	}
	return strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") ||
		strings.HasPrefix(arg, "git@") || strings.Contains(arg, "github.com") ||
		strings.Contains(arg, "gitlab.com") || strings.HasSuffix(arg, ".git")
}

func defaultPromoteDestination(basename string) string {
	basePath := os.Getenv("TRY_PROMOTE_PATH")
	if basePath == "" {
		basePath = defaultPromotePath
	}
	basePath = expandPath(basePath)
	if abs, err := filepath.Abs(basePath); err == nil {
		basePath = abs
	}

	re := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-(.+)$`)
	matches := re.FindStringSubmatch(basename)
	if len(matches) == 2 {
		basename = matches[1]
	}

	return filepath.Join(basePath, basename)
}

func expandPath(path string) string {
	path = os.ExpandEnv(path)
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func extractOptionWithValue(args []string, optName string) (string, []string) {
	for i := len(args) - 1; i >= 0; i-- {
		if args[i] == optName {
			if i+1 < len(args) {
				value := args[i+1]
				// Remove both the option and value from args
				newArgs := append(args[:i], args[i+2:]...)
				return value, newArgs
			}
		} else if strings.HasPrefix(args[i], optName+"=") {
			parts := strings.SplitN(args[i], "=", 2)
			value := parts[1]
			newArgs := append(args[:i], args[i+1:]...)
			return value, newArgs
		}
	}
	return "", args
}

func parseTestKeys(spec string) []string {
	if spec == "" {
		return nil
	}

	tokens := strings.Split(spec, ",")
	keys := []string{}

	for _, token := range tokens {
		token = strings.TrimSpace(token)
		upper := strings.ToUpper(token)

		switch upper {
		case "UP":
			keys = append(keys, "\x1b[A")
		case "DOWN":
			keys = append(keys, "\x1b[B")
		case "LEFT":
			keys = append(keys, "\x1b[D")
		case "RIGHT":
			keys = append(keys, "\x1b[C")
		case "ENTER":
			keys = append(keys, "\r")
		case "ESC":
			keys = append(keys, "\x1b")
		case "BACKSPACE":
			keys = append(keys, "\x7F")
		case "CTRL-D", "CTRLD":
			keys = append(keys, "\x04")
		case "CTRL-P", "CTRLP":
			keys = append(keys, "\x10")
		case "CTRL-N", "CTRLN":
			keys = append(keys, "\x0E")
		case "CTRL-J", "CTRLJ":
			keys = append(keys, "\n")
		case "CTRL-K", "CTRLK":
			keys = append(keys, "\x0B")
		case "CTRL-R", "CTRLR":
			keys = append(keys, "\x12")
		default:
			if strings.HasPrefix(token, "TYPE=") {
				typeStr := strings.TrimPrefix(token, "TYPE=")
				for _, ch := range typeStr {
					keys = append(keys, string(ch))
				}
			} else if len(token) == 1 {
				keys = append(keys, token)
			}
		}
	}

	return keys
}

func joinCommands(parts []string) string {
	return strings.Join(parts, " \\\n  && ")
}

func emitScript(parts []string) {
	fmt.Println(joinCommands(parts))
}

func emitTasksScript(tasks []map[string]string) {
	var target map[string]string
	for _, t := range tasks {
		if t["type"] == "target" {
			target = t
			break
		}
	}

	if target == nil {
		panic("emit_tasks_script requires a target path")
	}

	fullPath := target["path"]
	parts := []string{}

	for _, t := range tasks {
		switch t["type"] {
		case "echo":
			msg := t["msg"]
			expanded := expandTokens(msg)
			parts = append(parts, fmt.Sprintf("echo '%s'", quoteForShell(expanded)))
		case "mkdir":
			parts = append(parts, fmt.Sprintf("mkdir -p '%s'", quoteForShell(fullPath)))
		case "git-clone":
			parts = append(parts, fmt.Sprintf("git clone '%s' '%s'", quoteForShell(t["uri"]), quoteForShell(fullPath)))
		case "git-worktree":
			repo := t["repo"]
			if repo != "" {
				parts = append(parts, fmt.Sprintf("/usr/bin/env sh -c 'if git -C '%s' rev-parse --is-inside-work-tree >/dev/null 2>&1; then repo=$(git -C '%s' rev-parse --show-toplevel); git -C \"$repo\" worktree add --detach '%s' >/dev/null 2>&1 || true; fi; exit 0'", quoteForShell(repo), quoteForShell(repo), quoteForShell(fullPath)))
			} else {
				parts = append(parts, fmt.Sprintf("/usr/bin/env sh -c 'if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then repo=$(git rev-parse --show-toplevel); git -C \"$repo\" worktree add --detach '%s' >/dev/null 2>&1 || true; fi; exit 0'", quoteForShell(fullPath)))
			}
		case "rsync":
			src := strings.TrimRight(t["src"], "/") + "/"
			dst := strings.TrimRight(fullPath, "/") + "/"
			parts = append(parts, fmt.Sprintf("rsync -a '%s' '%s'", quoteForShell(src), quoteForShell(dst)))
		case "touch":
			parts = append(parts, fmt.Sprintf("touch '%s'", quoteForShell(fullPath)))
		case "cd":
			parts = append(parts, fmt.Sprintf("cd '%s'", quoteForShell(fullPath)))
		}
	}

	emitScript(parts)
}

func quoteForShell(s string) string {
	return strings.ReplaceAll(s, "'", `'"'"'`)
}

func uniqueDirName(triesPath string, dirName string) string {
	candidate := dirName
	i := 2
	for {
		if _, err := os.Stat(filepath.Join(triesPath, candidate)); os.IsNotExist(err) {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", dirName, i)
		i++
	}
}

func resolveUniqueNameWithVersioning(triesPath string, datePrefix string, base string) string {
	initial := datePrefix + "-" + base
	if _, err := os.Stat(filepath.Join(triesPath, initial)); os.IsNotExist(err) {
		return base
	}

	re := regexp.MustCompile(`^(.*?)(\d+)$`)
	matches := re.FindStringSubmatch(base)
	if len(matches) == 3 {
		stem := matches[1]
		n, _ := strconv.Atoi(matches[2])
		candidateNum := n + 1
		for {
			candidateBase := fmt.Sprintf("%s%d", stem, candidateNum)
			candidateFull := filepath.Join(triesPath, datePrefix+"-"+candidateBase)
			if _, err := os.Stat(candidateFull); os.IsNotExist(err) {
				return candidateBase
			}
			candidateNum++
		}
	}

	// No numeric suffix; use -2 style uniqueness on full name
	fullName := datePrefix + "-" + base
	unique := uniqueDirName(triesPath, fullName)
	return strings.TrimPrefix(unique, datePrefix+"-")
}

func isFishShell() bool {
	shell := os.Getenv("SHELL")
	return strings.Contains(shell, "fish")
}

func printGlobalHelp() {
	text := `{h1}try something!{reset}

Your experiments deserve a home. 🏠

Lightweight experiments for people with ADHD

this tool is not meant to be used directly,
but added to your ~/.zshrc or ~/.bashrc:

  {highlight}eval "$(try init ~/src/tries)"{reset}

for fish shell, add to ~/.config/fish/config.fish:

  {highlight}eval (try init ~/src/tries | string collect){reset}

{h2}Usage:{text}

  init [--path PATH]  # Initialize shell function for aliasing
  cd [QUERY] [name?]  # Interactive selector; Git URL shorthand supported
  clone <git-uri> [name]  # Clone git repo into date-prefixed directory
  worktree dir [name]  # Create date-prefixed dir; add worktree from CWD if git repo
  worktree <repo-path> [name]  # Same as above, but source repo is <repo-path>

{h2}Clone Examples:{text}

  try clone https://github.com/tobi/try.git
  # Creates: 2025-08-27-tobi-try

  try clone https://github.com/tobi/try.git my-fork
  # Creates: my-fork

  try https://github.com/tobi/try.git
  # Shorthand for clone (same as first example)

{h2}Worktree Examples:{text}

  try worktree dir
  # From current git repo, creates: 2025-08-27-repo-name and adds detached worktree

  try worktree ~/src/github.com/tobi/try my-branch
  # From given repo path, creates: 2025-08-27-my-branch and adds detached worktree

{h2}Defaults:{reset}
  Default path: {dim_text}~/src/tries{reset} (override with --path on commands)
  Promote path: {dim_text}~/dev{reset} (override with TRY_PROMOTE_PATH)
  Current default: {dim_text}%s{reset}
`

	expanded := expandTokens(fmt.Sprintf(text, os.ExpandEnv(defaultTryPath)))
	fmt.Print(expanded)
}

func main() {
	// Global help: show for --help/-h anywhere
	for _, arg := range os.Args[1:] {
		if arg == "--help" || arg == "-h" {
			printGlobalHelp()
			os.Exit(0)
		}
	}

	// Extract options
	args := os.Args[1:]
	var triesPath string
	var andType string
	var andExit bool
	var andKeysRaw string
	var andConfirm string

	// Extract --path
	if path, newArgs := extractOptionWithValue(args, "--path"); path != "" {
		triesPath = path
		args = newArgs
	}

	// Extract --and-type
	if typ, newArgs := extractOptionWithValue(args, "--and-type"); typ != "" {
		andType = typ
		args = newArgs
	}

	// Extract --and-exit
	for i, arg := range args {
		if arg == "--and-exit" {
			andExit = true
			args = append(args[:i], args[i+1:]...)
			break
		}
	}

	// Extract --and-keys
	if keys, newArgs := extractOptionWithValue(args, "--and-keys"); keys != "" {
		andKeysRaw = keys
		args = newArgs
	}

	// Extract --and-confirm
	if confirm, newArgs := extractOptionWithValue(args, "--and-confirm"); confirm != "" {
		andConfirm = confirm
		args = newArgs
	}

	if triesPath == "" {
		triesPath = os.ExpandEnv(defaultTryPath)
	}
	triesPath, _ = filepath.Abs(triesPath)

	andKeys := parseTestKeys(andKeysRaw)

	if len(args) == 0 {
		printGlobalHelp()
		os.Exit(2)
	}

	command := args[0]
	args = args[1:]

	switch command {
	case "clone":
		tasks := cmdClone(args, triesPath)
		emitTasksScript(tasks)
		os.Exit(0)
	case "init":
		cmdInit(args, triesPath)
	case "worktree":
		if len(args) == 0 || args[0] == "dir" {
			// try worktree dir [name] (or no subcommand -> current directory)
			custom := strings.Join(args[1:], " ")
			base := custom
			if base == "" {
				wd, _ := os.Getwd()
				base = filepath.Base(wd)
			}
			base = regexp.MustCompile(`\s+`).ReplaceAllString(base, "-")
			datePrefix := time.Now().Format("2006-01-02")
			base = resolveUniqueNameWithVersioning(triesPath, datePrefix, base)
			dirName := datePrefix + "-" + base
			fullPath := filepath.Join(triesPath, dirName)

			tasks := []map[string]string{
				{"type": "target", "path": fullPath},
				{"type": "mkdir"},
			}

			if _, err := os.Stat(filepath.Join(os.Getenv("PWD"), ".git")); err == nil {
				tasks = append(tasks, map[string]string{
					"type": "echo",
					"msg":  "Using {highlight}git worktree{reset_fg} to create this trial from " + os.Getenv("PWD") + ".",
				})
				tasks = append(tasks, map[string]string{"type": "git-worktree"})
			}

			tasks = append(tasks,
				map[string]string{"type": "touch"},
				map[string]string{"type": "cd"},
			)
			emitTasksScript(tasks)
			os.Exit(0)
		} else {
			// try worktree <repo-path> [name]
			repoDir := args[0]
			custom := strings.Join(args[1:], " ")
			base := custom
			if base == "" {
				absRepo, _ := filepath.Abs(repoDir)
				base = filepath.Base(absRepo)
			}
			base = regexp.MustCompile(`\s+`).ReplaceAllString(base, "-")
			datePrefix := time.Now().Format("2006-01-02")
			base = resolveUniqueNameWithVersioning(triesPath, datePrefix, base)
			dirName := datePrefix + "-" + base
			fullPath := filepath.Join(triesPath, dirName)

			tasks := []map[string]string{
				{"type": "target", "path": fullPath},
				{"type": "mkdir"},
				{"type": "echo", "msg": "Using {highlight}git worktree{reset_fg} to create this trial from " + repoDir + "."},
				{"type": "git-worktree", "repo": repoDir},
				{"type": "touch"},
				{"type": "cd"},
			}
			emitTasksScript(tasks)
			os.Exit(0)
		}
	case "cd":
		tasks := cmdCD(args, triesPath, andType, andExit, andKeys, andConfirm)
		if tasks != nil {
			emitTasksScript(tasks)
		}
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printGlobalHelp()
		os.Exit(2)
	}
}
