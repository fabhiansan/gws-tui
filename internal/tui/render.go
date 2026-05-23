package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fabhiansan/gws-tui/internal/api"
	"github.com/mattn/go-runewidth"
)

func (m Model) View() string {
	width, height := m.width, m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 32
	}

	if m.authRequired {
		return m.theme.Root.Width(width).Height(height).Render(m.authView(width, height))
	}

	body := m.mainView(width, height)
	if m.messagesVisible {
		messages := m.renderMessageLog(width, height)
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, messages)
	}
	if m.helpVisible {
		help := m.renderHelp(max(50, width-4), max(10, height-2))
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, help)
	}
	if m.imageViewer != nil {
		return m.renderImageViewer(width, height)
	}
	if m.modal != nil {
		modal := m.renderModal(max(40, width-14))
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
	}
	return body
}

func (m Model) mainView(width, height int) string {
	if m.feature == FeatureMail {
		return m.mailView(width, height)
	}
	if m.feature == FeatureCalendar {
		return m.calendarViewPane(width, height)
	}
	if m.feature == FeatureMeet {
		return m.meetView(width, height)
	}
	if m.feature == FeatureDocs {
		return m.singlePaneView(width, height)
	}
	leftHBorder := m.theme.Pane.GetHorizontalBorderSize()
	leftVBorder := m.theme.Pane.GetVerticalBorderSize()
	detailHBorder := m.theme.Active.GetHorizontalBorderSize()
	detailVBorder := m.theme.Active.GetVerticalBorderSize()
	actionVBorder := m.theme.Input.GetVerticalBorderSize()
	statusH := 1

	leftW := max(20, int(float64(width)*0.30)-leftHBorder)
	rightW := max(20, width-leftW-leftHBorder-detailHBorder)

	actionContentH := max(3, min(8, strings.Count(m.input.Value(), "\n")+3))
	detailContentH := max(5, height-statusH-detailVBorder-actionContentH-actionVBorder)
	leftContentH := max(5, height-statusH-leftVBorder)

	left := m.renderList(leftW, leftContentH)
	detailStyle := m.theme.Pane
	if m.focusedPane == paneDetail {
		detailStyle = m.theme.Active
	}
	detail := paneWithTitle(detailStyle, m.title(" [2]-"+m.detailTitle()+" "), m.detail.View(), rightW, detailContentH)
	action := m.renderAction(rightW, actionContentH)
	right := lipgloss.JoinVertical(lipgloss.Left, detail, action)
	row := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	status := m.renderStatus(width)
	return m.theme.Root.Width(width).Height(height).Render(lipgloss.JoinVertical(lipgloss.Left, row, status))
}

// singlePaneView is used for browse-first features where the list and the
// detail view share the same screen instead of sitting side by side.
func (m Model) singlePaneView(width, height int) string {
	hBorder := m.theme.Active.GetHorizontalBorderSize()
	vBorder := m.theme.Active.GetVerticalBorderSize()
	statusH := 1
	contentW := max(20, width-hBorder)
	contentH := max(5, height-statusH-vBorder)

	var pane string
	if m.singlePaneDetailVisible() {
		style := m.theme.Pane
		if m.focusedPane == paneDetail {
			style = m.theme.Active
		}
		pane = paneWithTitle(style, m.title(" [2]-"+m.detailTitle()+" "), m.detail.View(), contentW, contentH)
	} else {
		pane = m.renderList(contentW, contentH)
	}
	status := m.renderStatus(width)
	return m.theme.Root.Width(width).Height(height).Render(lipgloss.JoinVertical(lipgloss.Left, pane, status))
}

func (m Model) singlePaneDetailVisible() bool {
	return m.feature == FeatureDocs && m.focusedPane == paneDetail
}

func (m Model) authView(width, height int) string {
	var lines []string
	lines = append(lines, "Signing you into Google Workspace...", "")
	if m.auth.Valid() {
		lines = append(lines, "✓ Token cache found")
	} else {
		lines = append(lines, "→ Waiting for OAuth credentials")
	}
	if m.auth.ProjectID != "" {
		lines = append(lines, "Project: "+m.auth.ProjectID)
	}
	if m.auth.Error != "" {
		lines = append(lines, "Auth note: "+m.auth.Error)
	}
	lines = append(lines,
		"",
		"If your browser did not open, run:",
		"gws auth login",
		"",
		"Press r to retry · q to quit",
	)
	content := strings.Join(lines, "\n")
	boxWidth := min(74, width-4)
	box := m.theme.Active.Width(boxWidth).Render(content)
	titled := paneWithTitle(m.theme.Active, m.title(" gws · sign in "), content, boxWidth, lipgloss.Height(box))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, titled)
}

func (m Model) renderList(width, height int) string {
	var title string
	var lines []string
	selStart, selEnd := -1, -1
	switch m.feature {
	case FeatureChat:
		title, lines, selStart, selEnd = m.chatListRows(width)
	case FeatureMail:
		title, lines, selStart, selEnd = m.mailListRows(width)
	case FeatureCalendar:
		if m.calendarView == calViewMonth {
			return m.renderCalendarMonthPane(width, height)
		}
		title, lines, selStart, selEnd = m.calendarAgendaRows(width)
	case FeatureMeet:
		title, lines, selStart, selEnd = m.meetListRows(width)
	case FeatureTasks:
		title, lines, selStart, selEnd = m.taskListRows(width)
	case FeatureDrive:
		title, lines, selStart, selEnd = m.driveListRows(width)
	case FeatureDocs:
		title, lines, selStart, selEnd = m.docsListRows(width)
	}
	if len(lines) == 0 {
		switch {
		case m.featureLoading[m.feature]:
			lines = []string{"", m.subtle(" Loading…")}
		case m.feature == FeatureDocs && m.loading:
			label := "Loading docs..."
			if strings.TrimSpace(m.search) != "" {
				label = "Searching docs /" + m.search + "..."
			}
			lines = []string{"", " " + label}
		default:
			lines = []string{"", " No items yet."}
		}
	}
	innerW := max(1, width-m.theme.Pane.GetHorizontalPadding())
	viewportH := max(1, height)
	if selStart >= 0 && selEnd >= selStart {
		for i := selStart; i <= selEnd && i < len(lines); i++ {
			lines[i] = renderStylePreserve(m.theme.Selected.Width(innerW), truncate(lines[i], innerW))
		}
	}
	offset := computeListOffset(len(lines), viewportH, selStart, selEnd)
	visible := lines
	if offset > 0 && offset < len(lines) {
		visible = lines[offset:]
	}
	body := fitLines(visible, innerW, viewportH)
	style := m.theme.Pane
	if m.focusedPane == paneList {
		style = m.theme.Active
	}
	return paneWithTitle(style, m.title(title), body, width, height)
}

// padCell truncates value to width display cells, then right-pads it with
// spaces so column-aligned rows line up regardless of the text inside.
func padCell(value string, width int) string {
	value = truncate(value, width)
	if pad := width - lipgloss.Width(value); pad > 0 {
		value += strings.Repeat(" ", pad)
	}
	return value
}

func computeListOffset(total, viewportH, selStart, selEnd int) int {
	if total <= viewportH || selEnd < 0 {
		return 0
	}
	offset := 0
	if selEnd >= viewportH {
		offset = selEnd - viewportH + 1
	}
	if selStart >= 0 && selStart < offset {
		offset = selStart
	}
	if offset < 0 {
		offset = 0
	}
	if maxOffset := total - viewportH; offset > maxOffset {
		offset = maxOffset
	}
	return offset
}

func (m Model) renderAction(width, height int) string {
	focused := m.focusedPane == paneAction
	title := " [3]-" + m.actionTitle() + " "
	content := ""
	if focused {
		content = m.input.View()
	} else {
		content = m.subtle(m.actionPlaceholder())
	}
	if chip := m.pendingAttachmentsChip(); chip != "" {
		content = chip + "\n" + content
	}
	style := m.theme.Input
	if !focused {
		style = style.BorderForeground(lipgloss.Color(m.theme.Subtle))
	}
	return paneWithTitle(style, m.title(title), content, width, height)
}

// pendingAttachmentsChip surfaces staged chat uploads above the composer so
// users see what will be sent on Enter even after the success toast fades.
func (m Model) pendingAttachmentsChip() string {
	if m.feature != FeatureChat || len(m.pendingChatAttachments) == 0 {
		return ""
	}
	return m.subtle(fmt.Sprintf("[attach] %d image(s) pending - Enter to send, Ctrl+X to clear", len(m.pendingChatAttachments)))
}

// wideGlyphs lists the symbol runes the status bar draws that many
// terminals render two cells wide even though lipgloss measures them as
// one. statusWidth counts them as 2 so the compact-mode and truncate
// decisions below fire before the row overflows onto a second line.
var wideGlyphs = map[rune]struct{}{
	'◉': {}, '✉': {}, '◫': {}, '◎': {}, '☑': {}, '▤': {}, '●': {}, '◦': {},
}

// statusWidth is a pessimistic estimate of how many terminal columns s
// occupies: lipgloss.Width plus one extra cell per wide glyph. The status
// row budget uses this instead of lipgloss.Width so it stays in sync with
// what the terminal actually draws — lipgloss undercounts the symbol
// icons, which is what lets the row silently overflow onto a second line.
func statusWidth(s string) int {
	w := lipgloss.Width(s)
	for _, r := range ansi.Strip(s) {
		if _, ok := wideGlyphs[r]; ok {
			w++
		}
	}
	return w
}

// fetchActivity reports whether the daemon currently has a request in flight on
// the TUI's behalf and, if so, a short summary of what is being fetched. The
// status bar turns this into a single spinner-led "fetching …" segment so the
// user always knows the daemon is busy — even while a pane still shows stale or
// empty content and nothing else on screen is moving.
func (m Model) fetchActivity() (summary string, active bool) {
	seen := map[string]bool{}
	var labels []string
	add := func(label string) {
		if label == "" || seen[label] {
			return
		}
		seen[label] = true
		labels = append(labels, label)
	}

	// Cold-start fan-out and lazy first-visit loads: panes still waiting on
	// their very first fetch.
	for _, f := range featureOrder {
		if m.featureLoading[f] {
			add(m.featureLabel(f))
		}
	}
	// A chat space's messages are being (re)loaded after a switch or open.
	if m.chatLoading {
		add(m.featureLabel(FeatureChat))
	}
	// A document body is being fetched.
	if m.docLoadingID != "" {
		add(m.featureLabel(FeatureDocs))
	}
	// Generic in-flight fetch — a refresh or load-more of whatever pane the
	// user is currently looking at.
	if m.loading {
		add(m.featureLabel(m.feature))
	}

	switch len(labels) {
	case 0:
		return "", false
	case 1, 2:
		return strings.Join(labels, ", "), true
	default:
		// At cold start most panes load at once; a count stays readable and
		// keeps the status row from overflowing onto a second line.
		return fmt.Sprintf("%d sections", len(labels)), true
	}
}

func (m Model) renderStatus(width int) string {
	brand := m.theme.StatusBrand.Render(" gws ")
	tabSep := m.theme.StatusSeparator.Render("│")

	// renderTabs builds the feature tab strip. The compact form drops the
	// word label and keeps just the icon + Ctrl-number, so a narrow
	// terminal still shows every tab instead of overflowing the row.
	renderTabs := func(compact bool) string {
		tabs := make([]string, 0, len(featureOrder))
		for i, feature := range featureOrder {
			var label string
			if compact {
				label = fmt.Sprintf("%s ^%d", m.featureIcon(feature), i+1)
			} else {
				label = fmt.Sprintf("%s %s ^%d", m.featureIcon(feature), m.featureLabel(feature), i+1)
			}
			if feature == m.feature {
				tabs = append(tabs, m.theme.ActiveTab.Render(label))
			} else {
				tabs = append(tabs, m.theme.Tab.Render(label))
			}
		}
		return strings.Join(tabs, tabSep)
	}

	// finalize stretches left/right to fill the row, then hard-truncates to
	// width. An over-long row *wraps* onto a second line — that extra row
	// pushes the layout past m.height and clips the UI, so this truncate
	// guard is what keeps the status bar exactly one row tall.
	//
	// Every width calculation uses statusWidth, not lipgloss.Width, because
	// the terminal draws the symbol icons wider than lipgloss measures.
	// ansi.Truncate still counts the lipgloss way, so we aim it at
	// width-drift to shave the overflow; the final padding is also done by
	// hand, since lipgloss .Width() would re-pad by its own undercount and
	// re-introduce the overflow for any wide glyph left in the row.
	finalize := func(left, right string) string {
		gap := width - statusWidth(left) - statusWidth(right)
		filler := m.theme.Status.Render(strings.Repeat(" ", max(1, gap)))
		line := left + filler + right
		if statusWidth(line) > width {
			drift := statusWidth(line) - lipgloss.Width(line)
			line = ansi.Truncate(line, max(0, width-drift), "")
		}
		if pad := width - statusWidth(line); pad > 0 {
			line += m.theme.Status.Render(strings.Repeat(" ", pad))
		}
		return line
	}

	left := brand + renderTabs(false)

	if m.err != "" {
		errSeg := m.theme.StatusError.Render(fmt.Sprintf(" ! %s · x dismiss ", m.err))
		if width-statusWidth(left)-statusWidth(errSeg) < 1 {
			errSeg = m.theme.StatusError.Render(" ! " + truncate(m.err, max(10, width-statusWidth(left)-12)) + " · x ")
		}
		if width-statusWidth(left)-statusWidth(errSeg) < 1 {
			left = brand + renderTabs(true)
		}
		return finalize(left, errSeg)
	}

	segments := make([]string, 0, 6)

	fetchSummary, fetching := m.fetchActivity()
	if fetching {
		segments = append(segments, m.theme.StatusWarn.Render(m.spinner.View()+" fetching "+fetchSummary))
	}

	if m.toast != "" {
		segments = append(segments, m.theme.StatusAccent.Render(" "+m.toast+" "))
	}

	liveCount := 0
	for _, space := range m.spaces {
		if space.Live {
			liveCount++
		}
	}
	liveLabel := fmt.Sprintf("%s %d live", m.icon("●", "*"), liveCount)
	if liveCount > 0 {
		segments = append(segments, m.theme.StatusAccent.Render(" "+liveLabel+" "))
	} else {
		segments = append(segments, m.theme.StatusSegmentAlt.Render(liveLabel))
	}

	segments = append(segments, m.theme.StatusSegment.Render(m.paneHints()))
	segments = append(segments, m.theme.StatusSegmentAlt.Render("? help"))
	segments = append(segments, m.theme.StatusSegment.Render("q quit"))

	rightSep := m.theme.StatusSeparator.Render(" ")
	right := strings.Join(segments, rightSep)

	// When the row is too tight, shrink the right segments first, then fall
	// back to compact tabs. finalize still truncates as a last resort.
	if width-statusWidth(left)-statusWidth(right) < 1 {
		minSegments := []string{}
		// Keep the fetch indicator even in compact mode — knowing the daemon
		// is busy matters more than the pane hints it would otherwise show.
		if fetching {
			minSegments = append(minSegments, m.theme.StatusWarn.Render(m.spinner.View()+" fetching"))
		}
		if liveCount > 0 {
			minSegments = append(minSegments, m.theme.StatusAccent.Render(" "+liveLabel+" "))
		}
		minSegments = append(minSegments, m.theme.StatusSegment.Render("? q"))
		right = strings.Join(minSegments, rightSep)
	}
	if width-statusWidth(left)-statusWidth(right) < 1 {
		left = brand + renderTabs(true)
	}

	return finalize(left, right)
}

// renderModal draws the compose modal. Each field renders its own editor
// View() so the real text cursor is visible, and a footer carries the vim
// mode badge plus the keys that act on the modal.
func (m Model) renderModal(width int) string {
	if m.modal == nil {
		return ""
	}
	modalWidth := min(width, 88)
	lines, _ := m.modalContentLines()
	content := strings.Join(lines, "\n")
	// paneWithTitle re-applies the modal style; +2 covers the modal's vertical
	// padding so the content never gets clipped.
	return paneWithTitle(m.theme.Modal, m.title(" "+m.modal.title+" "), content, modalWidth, len(lines)+2)
}

// modalContentLines builds the modal body line by line and, alongside it, a
// parallel slice mapping each line to the field index it belongs to (-1 for
// chrome). The mapping lets the mouse layer hit-test which field a click hit.
func (m Model) modalContentLines() (lines []string, fieldOf []int) {
	for i := range m.modal.fields {
		field := &m.modal.fields[i]
		focused := i == m.modal.focus
		marker := "  "
		if focused {
			marker = m.accent(m.icon("▎", "|")) + " "
		}
		label := fmt.Sprintf("%-9s", field.Label)
		if focused {
			label = m.accent(label)
		}
		if field.Multiline {
			lines = append(lines, marker+label)
			fieldOf = append(fieldOf, i)
			for _, bodyLine := range strings.Split(field.view(), "\n") {
				lines = append(lines, "  "+bodyLine)
				fieldOf = append(fieldOf, i)
			}
		} else {
			lines = append(lines, marker+label+" "+field.view())
			fieldOf = append(fieldOf, i)
		}
	}
	lines = append(lines, "")
	fieldOf = append(fieldOf, -1)
	if !m.modal.savedAt.IsZero() {
		lines = append(lines, m.subtle("autosaved "+m.modal.savedAt.Format("15:04:05")))
		fieldOf = append(fieldOf, -1)
	}
	lines = append(lines, m.modalHintLine())
	fieldOf = append(fieldOf, -1)
	return lines, fieldOf
}

// modalHintLine is the modal footer: a vim mode badge plus the keys that act
// on the modal, spelled out so they stay discoverable.
func (m Model) modalHintLine() string {
	keys := "^s send · ^q cancel · Tab field"
	if m.modal.kind == modalMail {
		keys = "^s send · ^d draft · ^q cancel · Tab field"
	}
	if !m.cfg.VimMode {
		return m.subtle("esc cancel · " + keys)
	}
	badge := m.accent("-- " + m.modal.vimMode.String() + " --")
	if m.modal.vimMode == vimModeNormal {
		return badge + "  " + m.subtle("i insert · hjkl move · dd yy p edit · "+keys)
	}
	return badge + "  " + m.subtle("esc normal mode · "+keys)
}

type helpBinding struct {
	keys string
	desc string
}

type helpSection struct {
	name     string
	bindings []helpBinding
}

func (m Model) renderHelp(width, height int) string {
	sections := []helpSection{
		{"Global", []helpBinding{
			{"?", "toggle this help"},
			{"q · Ctrl+C", "quit"},
			{"Tab · S-Tab", "cycle features"},
			{"Ctrl+N · Ctrl+P", "next / prev feature"},
			{"Ctrl+L", "toggle feature drawer"},
			{"R", "refresh"},
			{"r", "resize panes auto"},
			{":", "message log"},
			{"/", "search"},
			{"1 · 2 · 3", "focus list · detail · action"},
			{"esc", "dismiss modal · back to list"},
		}},
		{"Pane & list", []helpBinding{
			{"h / l", "focus pane left / right"},
			{"j / k", "move cursor / selection"},
			{"g / G", "top / bottom"},
			{"Enter", "open item / detail"},
			{"Ctrl+D / Ctrl+U", "page down / up"},
			{"/", "search"},
			{"Esc", "exit visual · back to list"},
		}},
	}

	featBindings := m.featureBindings()
	featName := strings.Title(string(m.feature))
	if len(featBindings) > 0 {
		sections = append(sections, helpSection{featName, featBindings})
	}

	if m.cfg.VimMode {
		sections = append(sections,
			helpSection{"Vim — composer", []helpBinding{
				{"i / I / a / A", "insert here / line start / after / end"},
				{"o / O", "open line below / above"},
				{"h j k l", "move char / line"},
				{"w / b", "word forward / back"},
				{"0 / $", "line start / end"},
				{"gg / G", "top / bottom of input"},
				{"x / X", "delete char forward / back"},
				{"dd / cc", "delete line / change line"},
				{"dw de db", "delete word forward / to end / back"},
				{"cw ce / yw ye", "change / yank word"},
				{"d$ D / d0", "delete to line end / start"},
				{"yy / p / P", "yank / paste after / before"},
				{"Enter", "send message"},
			}},
			helpSection{"Vim — yank & paste", []helpBinding{
				{"y (list)", "yank selected item to clipboard"},
				{"p (list)", "paste clipboard into composer"},
				{"a (list)", "append to composer in insert mode"},
			}},
		)
	}

	keyW := 0
	for _, sec := range sections {
		for _, b := range sec.bindings {
			if w := lipgloss.Width(b.keys); w > keyW {
				keyW = w
			}
		}
	}

	renderSection := func(sec helpSection) []string {
		out := make([]string, 0, len(sec.bindings)+1)
		out = append(out, m.accent(sec.name))
		for _, b := range sec.bindings {
			pad := strings.Repeat(" ", max(1, keyW-lipgloss.Width(b.keys)+2))
			out = append(out, "  "+b.keys+pad+m.subtle(b.desc))
		}
		return out
	}

	var allLines []string
	for _, sec := range sections {
		allLines = append(allLines, renderSection(sec)...)
		allLines = append(allLines, "")
	}
	if len(allLines) > 0 && allLines[len(allLines)-1] == "" {
		allLines = allLines[:len(allLines)-1]
	}

	title := m.title(" gws · keybindings ")

	chrome := m.theme.Modal.GetVerticalFrameSize() + 2
	contentHeight := max(1, height-chrome)
	totalLines := len(allLines)
	maxScroll := max(0, totalLines-contentHeight)
	m.helpScroll = clamp(m.helpScroll, maxScroll)

	visibleStart := m.helpScroll
	visibleEnd := min(totalLines, visibleStart+contentHeight)
	visibleLines := allLines[visibleStart:visibleEnd]

	body := strings.Join(visibleLines, "\n")

	numerator := visibleEnd
	denominator := totalLines
	scrollPct := 0
	if totalLines > contentHeight {
		scrollPct = min(99, visibleStart*100/max(1, totalLines-contentHeight))
	}
	scrollHint := ""
	if totalLines > contentHeight {
		scrollHint = fmt.Sprintf("▲%d%%▼", scrollPct)
	}

	var footer string
	if m.cfg.VimMode {
		footer = m.subtle(fmt.Sprintf("j/k scroll  gg/G top/bottom  ?/Esc close  %d/%d %s", numerator, denominator, scrollHint))
	} else {
		footer = m.subtle(fmt.Sprintf("↑/↓ scroll  PgUp/PgDn page  Home/End  ?/Esc close  %d/%d %s", numerator, denominator, scrollHint))
	}

	content := strings.Join([]string{body, "", footer}, "\n")

	modalWidth := m.messageLogModalWidth(width)
	box := m.theme.Modal.Width(modalWidth).Render(content)
	return paneWithTitle(m.theme.Modal, title, content, modalWidth, lipgloss.Height(box))
}

func (m Model) featureBindings() []helpBinding {
	switch m.feature {
	case FeatureChat:
		return []helpBinding{
			{"s", "toggle live subscription"},
			{"Ctrl+V", "attach image from clipboard"},
			{"Ctrl+X", "clear pending attachments"},
		}
	case FeatureMail:
		return []helpBinding{
			{"H", "focus folder sidebar"},
			{"s", "toggle star"},
			{"c", "compose"},
			{"R", "reply"},
			{"A", "reply all"},
			{"f", "forward"},
			{"l", "toggle label"},
			{"u", "mark read / unread"},
			{"e", "archive"},
			{"#", "trash"},
		}
	case FeatureCalendar:
		return []helpBinding{
			{"v", "toggle month / agenda view"},
			{"c", "new event"},
			{"Enter", "open focused event detail"},
			{"E", "edit opened/selected event"},
			{"y / n / M", "RSVP yes / no / maybe"},
			{"d", "delete event"},
			{">", "move event to next calendar"},
			{"] · [", "next / prev calendar"},
			{"D", "go to a specific date"},
			{"t", "jump to today"},
			{"h/l (grid)", "previous / next day"},
			{"j/k (grid)", "next / previous week"},
			{"}/{ (grid)", "next / previous month"},
		}
	case FeatureMeet:
		return []helpBinding{
			{"n", "new space"},
			{"J", "join (open link)"},
			{"C", "copy link"},
			{"E", "end conference"},
		}
	case FeatureTasks:
		return []helpBinding{
			{"Space", "complete / uncomplete"},
			{"d", "delete task"},
			{"] · [", "next / prev task list"},
			{"m", "load more"},
		}
	default:
		return nil
	}
}

func (m *Model) updateHelpScroll(key string) {
	visible := m.height - m.theme.Modal.GetVerticalFrameSize() - 2
	if visible < 1 {
		visible = 1
	}
	page := max(1, visible/2)

	if m.helpPending == "g" {
		m.helpPending = ""
		if key == "g" {
			m.helpScroll = 0
		}
		return
	}

	switch key {
	case "j", "down":
		m.helpScroll++
	case "k", "up":
		m.helpScroll--
	case "ctrl+d", "pgdown":
		m.helpScroll += page
	case "ctrl+u", "pgup":
		m.helpScroll -= page
	case "ctrl+f":
		m.helpScroll += visible
	case "ctrl+b":
		m.helpScroll -= visible
	case "g":
		if m.cfg.VimMode {
			m.helpPending = "g"
			return
		}
		m.helpScroll = 0
	case "G", "end":
		m.helpScroll = 999999
	case "home":
		m.helpScroll = 0
	}
}

func balancedHeight(sections [][]string, columns, spacer int) int {
	if columns < 1 {
		columns = 1
	}
	heights := make([]int, columns)
	for _, sec := range sections {
		idx := 0
		for i := 1; i < columns; i++ {
			if heights[i] < heights[idx] {
				idx = i
			}
		}
		if heights[idx] > 0 {
			heights[idx] += spacer
		}
		heights[idx] += len(sec)
	}
	maxH := 0
	for _, h := range heights {
		if h > maxH {
			maxH = h
		}
	}
	return maxH
}

func distributeSections(sections [][]string, columns, spacer int) [][][]string {
	if columns < 1 {
		columns = 1
	}
	cols := make([][][]string, columns)
	heights := make([]int, columns)
	for _, sec := range sections {
		idx := 0
		for i := 1; i < columns; i++ {
			if heights[i] < heights[idx] {
				idx = i
			}
		}
		if heights[idx] > 0 {
			heights[idx] += spacer
		}
		heights[idx] += len(sec)
		cols[idx] = append(cols[idx], sec)
	}
	return cols
}

func (m Model) detailTitle() string {
	var base string
	switch m.feature {
	case FeatureChat:
		if m.spaceFilterActive {
			base = "Spaces filter"
		} else {
			base = m.spaceLabel(m.selectedSpace())
		}
	case FeatureMail:
		base = fallback(m.selectedMail().Subject, "Mail")
	case FeatureCalendar:
		if m.calendarView == calViewMonth && m.focusedPane != paneDetail {
			base = m.calendarCursorOrToday().Format("Mon, 02 Jan 2006")
		} else {
			base = fallback(m.selectedEvent().Summary, "Calendar")
		}
	case FeatureMeet:
		base = fallback(lastSegment(m.selectedMeet().Name), "Meet")
	case FeatureTasks:
		base = fallback(m.selectedTask().Title, "Tasks")
	case FeatureDrive:
		base = fallback(m.selectedDriveFile().Name, "Drive")
	case FeatureDocs:
		base = fallback(m.selectedDocFile().Name, "Docs")
	default:
		base = "gws"
	}
	if m.cfg.VimMode && m.focusedPane == paneDetail && m.detailVisual {
		if m.detailVisualLine {
			base += "  -- VISUAL LINE --"
		} else {
			base += "  -- VISUAL --"
		}
	}
	return base
}

func (m *Model) detailContent() string {
	switch m.feature {
	case FeatureChat:
		return m.chatDetail()
	case FeatureMail:
		return m.mailDetail()
	case FeatureCalendar:
		return m.calendarDetail()
	case FeatureMeet:
		return m.meetDetail()
	case FeatureTasks:
		return m.taskDetail()
	case FeatureDrive:
		return m.driveDetail()
	case FeatureDocs:
		return m.docsDetail()
	default:
		return ""
	}
}

// appendAttachmentLines renders msg.Attachments and records each attachment's
// line range so the vim cursor can resolve "which attachment is under me"
// when Enter is pressed in the detail pane. The indent argument shifts the
// rendered lines right so attachments under a reply bubble stay visually
// attached to it.
func (m *Model) appendAttachmentLines(lines []string, attachments []api.Attachment, indent int) []string {
	attLines, ranges := m.renderAttachmentsTracked(attachments)
	if indent > 0 {
		pad := strings.Repeat(" ", indent)
		for i := range attLines {
			attLines[i] = pad + attLines[i]
		}
	}
	base := countDisplayLines(lines)
	for _, r := range ranges {
		for i := 0; i < r.rows; i++ {
			m.mapDetailAttachmentLine(base+r.start+i, r.attachment)
		}
	}
	return append(lines, attLines...)
}

func (m *Model) mapDetailAttachmentLine(line int, attachment api.Attachment) {
	if m.detailAttachmentAt == nil {
		m.detailAttachmentAt = map[int]api.Attachment{}
	}
	m.detailAttachmentAt[line] = attachment
	if attachment.IsImage() {
		if m.detailImageAt == nil {
			m.detailImageAt = map[int]api.Attachment{}
		}
		m.detailImageAt[line] = attachment
	}
}

func (m Model) paneHints() string {
	switch m.focusedPane {
	case paneDetail:
		if m.cfg.VimMode {
			if m.detailVisual {
				return "motions extend  y yank  Esc cancel"
			}
			return "h/j/k/l cursor  w/b/e word  v visual  V line"
		}
		return "j/k scroll  g/G top/bot  i compose"
	case paneAction:
		if m.feature == FeatureChat && m.createSpaceMode {
			return "Enter create  Esc cancel"
		}
		if m.feature == FeatureChat && m.editMessageName != "" {
			return "Enter save  Esc cancel"
		}
		return "Enter send  Esc cancel"
	default:
		if m.spaceFilterActive && m.feature == FeatureChat {
			return "type filter  ↑/↓ move  Enter open  Esc clear"
		}
		return "j/k move  Enter open  i compose  / search"
	}
}

func (m Model) actionTitle() string {
	base := ""
	switch m.feature {
	case FeatureChat:
		if m.createSpaceMode {
			base = "new chat space · Enter create"
			break
		}
		if m.editMessageName != "" {
			base = "edit message · Enter save"
			break
		}
		base = "message · Enter send · Shift+Enter newline"
	case FeatureMail:
		base = "quick reply · c compose · R reply · A reply-all · l label · u read/unread"
	case FeatureCalendar:
		base = "quick add · Enter send · v agenda/month · D go to date"
	case FeatureMeet:
		base = "create space · Enter create"
	case FeatureTasks:
		base = "tasks · Space complete · d delete · [/] switch list · m more"
	case FeatureDrive:
		base = "drive · / search · m more"
	case FeatureDocs:
		base = "docs · / search · m more"
	default:
		base = "action"
	}
	if m.cfg.VimMode && m.focusedPane == paneAction {
		return "-- " + m.vimComposer.String() + " -- · " + base
	}
	return base
}

func (m Model) actionPlaceholder() string {
	switch m.feature {
	case FeatureChat:
		if m.createSpaceMode {
			return "Type a space name, optionally followed by | user emails, Enter to create."
		}
		if m.editMessageName != "" {
			return "Edit the message text, Enter to save."
		}
		return "Press i, type a message, Enter to send."
	case FeatureMail:
		return "Press c to compose, R to reply, u to toggle read state."
	case FeatureCalendar:
		return "Press Enter on an event for detail, i for quick-add, or c for full event."
	case FeatureMeet:
		return "Press n to create a new Meet space."
	case FeatureTasks:
		return "Use Space to complete tasks, d to delete, [ and ] to switch lists."
	case FeatureDrive:
		return "Use / to search Drive, m to load more files."
	case FeatureDocs:
		return "Use / to search Docs, m to load more documents."
	default:
		return ""
	}
}

func (m Model) title(value string) string {
	return m.theme.Title.Render(value)
}

func paneWithTitle(style lipgloss.Style, title, content string, width, height int) string {
	box := renderStylePreserve(style.Width(width).Height(height), content)
	if title == "" {
		return box
	}
	nl := strings.Index(box, "\n")
	if nl == -1 {
		return box
	}
	topLine := box[:nl]
	rest := box[nl:]

	plainTop := ansi.Strip(topLine)
	plainRunes := []rune(plainTop)
	plainWidth := lipgloss.Width(plainTop)
	titleWidth := lipgloss.Width(title)
	if len(plainRunes) < 4 || plainWidth < titleWidth+4 {
		return box
	}

	leftCorner := string(plainRunes[0])
	rightCorner := string(plainRunes[len(plainRunes)-1])
	borderChar := string(plainRunes[1])
	innerWidth := len(plainRunes) - 2

	titlePos := (innerWidth - titleWidth) / 2
	if titlePos < 1 {
		titlePos = 1
	}
	rightPad := innerWidth - titlePos - titleWidth
	if rightPad < 1 {
		rightPad = 1
		titlePos = innerWidth - titleWidth - rightPad
		if titlePos < 1 {
			return box
		}
	}

	borderStyle := lipgloss.NewStyle().
		Foreground(style.GetBorderTopForeground()).
		Background(style.GetBorderTopBackground())
	leftPart := borderStyle.Render(leftCorner + strings.Repeat(borderChar, titlePos))
	rightPart := borderStyle.Render(strings.Repeat(borderChar, rightPad) + rightCorner)

	return leftPart + title + rightPart + rest
}

func renderStylePreserve(style lipgloss.Style, value string) string {
	rendered := style.Render(value)
	start := styleStartSequence(style)
	if start == "" {
		return rendered
	}
	rendered = reapplyStyleAfterReset(rendered, start, "\x1b[0m")
	rendered = reapplyStyleAfterReset(rendered, start, "\x1b[m")
	return rendered + "\x1b[0m"
}

func styleStartSequence(style lipgloss.Style) string {
	const sentinel = "\x00"
	rendered := style.Inline(true).Render(sentinel)
	start, _, ok := strings.Cut(rendered, sentinel)
	if !ok {
		return ""
	}
	return start
}

func reapplyStyleAfterReset(value, start, reset string) string {
	return strings.ReplaceAll(value, reset, reset+start)
}

func (m Model) accent(value string) string {
	if m.cfg.NoColor {
		return value
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Accent)).Render(value)
}

func (m Model) live(value string) string {
	if m.cfg.NoColor {
		return value
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Live)).Render(value)
}

func (m Model) warn(value string) string {
	if m.cfg.NoColor {
		return value
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Warn)).Render(value)
}

func (m Model) danger(value string) string {
	if m.cfg.NoColor {
		return value
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Error)).Render(value)
}

func (m Model) icon(unicode, ascii string) string {
	if m.cfg.NoIcons {
		return ascii
	}
	return unicode
}

func (m Model) subtle(value string) string {
	return m.theme.SubtleText.Render(displayText(value))
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(displayText(value), width, "…")
}

func fitLines(lines []string, width, height int) string {
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, line := range lines {
		lines[i] = truncate(line, width)
	}
	return strings.Join(lines, "\n")
}

// narrowCond and wideCond measure string width with East Asian "ambiguous"
// glyphs (·, arrows, …) counted as one and two cells respectively. The
// EastAsianWidth field is pinned explicitly so the result does not depend on
// the host locale that runewidth.NewCondition would otherwise inherit.
var (
	narrowCond = newAmbiguousCond(false)
	wideCond   = newAmbiguousCond(true)
)

func newAmbiguousCond(eastAsian bool) *runewidth.Condition {
	c := runewidth.NewCondition()
	c.EastAsianWidth = eastAsian
	return c
}

// ambiguousDrift is how many extra columns s occupies on a terminal that
// renders East Asian "ambiguous" glyphs two cells wide. lipgloss and
// ansi.Wrap count those glyphs as one cell, so wrapping has to budget for
// this drift or the wrapped line still overflows the viewport and clips.
func ambiguousDrift(s string) int {
	plain := ansi.Strip(s)
	return wideCond.StringWidth(plain) - narrowCond.StringWidth(plain)
}

// wrapDetailLines folds each line to width display cells so long detail
// content (mail bodies, action hints, URLs) wraps onto extra rows instead of
// being clipped at the viewport's right edge. Blank lines are kept as-is so
// the vertical spacing of the detail panes is preserved.
func wrapDetailLines(lines []string, width int) []string {
	if width <= 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			out = append(out, line)
			continue
		}
		// Shrink the wrap target by the ambiguous-width drift: on a
		// terminal that draws ·/arrows two cells wide a line ansi.Wrap
		// thinks fits would still spill past the viewport edge and clip.
		target := max(1, width-ambiguousDrift(line))
		wrapped := wrapAnsi(line, target)
		if len(wrapped) == 0 {
			out = append(out, line)
			continue
		}
		out = append(out, wrapped...)
	}
	return out
}

func centerText(value string, width int) string {
	value = displayText(value)
	pad := max(0, (width-lipgloss.Width(value))/2)
	return strings.Repeat(" ", pad) + value
}

func rightAlign(value string, width int) string {
	value = displayText(value)
	pad := width - lipgloss.Width(value)
	if pad <= 0 {
		return value
	}
	return strings.Repeat(" ", pad) + value
}

func displayText(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	changed := false
	for _, r := range value {
		if isInvisibleFormatControl(r) {
			changed = true
			continue
		}
		b.WriteRune(r)
	}
	if !changed {
		return value
	}
	return b.String()
}

func isInvisibleFormatControl(r rune) bool {
	return unicode.In(r, unicode.Cf)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func relative(t time.Time) string {
	diff := time.Since(t)
	if diff < time.Hour {
		return fmt.Sprintf("%dm", max(1, int(diff.Minutes())))
	}
	if diff < 24*time.Hour {
		return fmt.Sprintf("%dh", int(diff.Hours()))
	}
	if diff < 7*24*time.Hour {
		return fmt.Sprintf("%dd", int(diff.Hours()/24))
	}
	return t.Format("02 Jan")
}

func formatOptionalTime(t time.Time, layout string) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(layout)
}

func fallback(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func lastSegment(value string) string {
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "/")
	return parts[len(parts)-1]
}
