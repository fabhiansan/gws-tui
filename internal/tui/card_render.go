package tui

import (
	"html"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fabhiansan/gws-tui/internal/api"
)

// renderCards builds a styled box for each ChatCard in the message and returns
// the joined lines, ready to be appended to chatDetail's output. width is the
// usable text width for the message column; the card draws a rounded border
// inside that, so the actual widget area is width - border - padding.
func (m Model) renderCards(cards []api.ChatCard, width int) []string {
	if len(cards) == 0 {
		return nil
	}
	out := make([]string, 0, len(cards)*8)
	for _, card := range cards {
		body := m.renderCardBody(card, cardInnerWidth(width))
		if body == "" {
			continue
		}
		boxed := renderStylePreserve(m.cardStyle().Width(cardBoxWidth(width)), body)
		for _, line := range strings.Split(boxed, "\n") {
			out = append(out, "  "+line)
		}
	}
	return out
}

// cardStyle is a Pane-derived box with a different border tint so cards read
// as a distinct affordance from message text. We keep using rounded corners
// to match the rest of the TUI.
func (m Model) cardStyle() lipgloss.Style {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 2)
	if !m.cfg.NoColor {
		style = style.
			BorderForeground(lipgloss.Color(m.theme.Border)).
			BorderBackground(lipgloss.Color(m.theme.Surface)).
			Foreground(lipgloss.Color(m.theme.Fg)).
			Background(lipgloss.Color(m.theme.Surface))
	}
	return style
}

func cardInnerWidth(width int) int {
	// 2 padding + 2 padding + 1 border each side = 6; leave a small safety
	// margin so wrapped lines don't kiss the right border.
	w := width - 8
	if w < 10 {
		return 10
	}
	return w
}

func cardBoxWidth(width int) int {
	w := width - 4
	if w < 16 {
		return 16
	}
	return w
}

func (m Model) renderCardBody(card api.ChatCard, innerW int) string {
	var lines []string
	lines = append(lines, m.renderCardHeader(card.Header, innerW)...)
	if card.Header != nil && len(card.Widgets) > 0 {
		lines = append(lines, "")
	}
	lastSection := ""
	for i, w := range card.Widgets {
		if w.SectionHeader != "" && w.SectionHeader != lastSection {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, m.subtle(truncate(stripCardHTML(w.SectionHeader), innerW)))
			lastSection = w.SectionHeader
		}
		widgetLines := m.renderCardWidget(w, innerW)
		if len(widgetLines) == 0 {
			continue
		}
		if i > 0 && needsSpacer(card.Widgets[i-1], w) {
			lines = append(lines, "")
		}
		lines = append(lines, widgetLines...)
	}
	return strings.Join(lines, "\n")
}

// needsSpacer decides whether to insert a blank line between two adjacent
// widgets. Buttons and dividers are visually self-contained; consecutive
// decoratedTexts read better with a gap so each label/value pair is its own
// "row" like Google Chat shows them.
func needsSpacer(prev, next api.CardWidget) bool {
	if prev.Kind == api.CardWidgetDivider || next.Kind == api.CardWidgetDivider {
		return false
	}
	if prev.Kind == api.CardWidgetButtonList || next.Kind == api.CardWidgetButtonList {
		return true
	}
	if prev.Kind == api.CardWidgetDecoratedText && next.Kind == api.CardWidgetDecoratedText {
		return true
	}
	return false
}

func (m Model) renderCardHeader(h *api.CardHeader, width int) []string {
	if h == nil {
		return nil
	}
	var lines []string
	if h.Title != "" {
		title := lipgloss.NewStyle().Bold(true).Render(stripCardHTML(h.Title))
		lines = append(lines, wrapAnsi(title, width)...)
	}
	if h.Subtitle != "" {
		for _, sub := range wrapAnsi(stripCardHTML(h.Subtitle), width) {
			lines = append(lines, m.subtle(sub))
		}
	}
	return lines
}

func (m Model) renderCardWidget(w api.CardWidget, width int) []string {
	switch w.Kind {
	case api.CardWidgetDecoratedText:
		return m.renderDecoratedText(w.DecoratedText, width)
	case api.CardWidgetTextParagraph:
		if w.TextParagraph == nil {
			return nil
		}
		return wrapAnsi(stripCardHTML(w.TextParagraph.Text), width)
	case api.CardWidgetButtonList:
		return m.renderButtonList(w.ButtonList, width)
	case api.CardWidgetImage:
		return m.renderCardImage(w.Image, width)
	case api.CardWidgetDivider:
		return []string{m.subtle(strings.Repeat("─", width))}
	case api.CardWidgetColumns:
		return m.renderColumns(w.Columns, width)
	case api.CardWidgetGrid:
		return m.renderGrid(w.Grid, width)
	case api.CardWidgetUnknown:
		kind := w.UnknownType
		if kind == "" {
			kind = "widget"
		}
		return []string{m.subtle(truncate("[unsupported "+kind+"]", width))}
	}
	return nil
}

func (m Model) renderDecoratedText(d *api.DecoratedTextWidget, width int) []string {
	if d == nil {
		return nil
	}
	icon := m.cardIcon(d.StartIcon)
	if icon == "" {
		icon = m.cardIcon(d.Icon)
	}
	// Layout: " ICON  TopLabel\n        Text\n        BottomLabel". The
	// indent keeps a two-line entry visually grouped — both wrapped lines
	// sit under the same icon column.
	indent := "  "
	if icon != "" {
		indent = strings.Repeat(" ", lipgloss.Width(icon)+2)
	}
	textWidth := width - lipgloss.Width(indent)
	if textWidth < 4 {
		textWidth = 4
	}
	var lines []string
	first := true
	emit := func(content string, subtle bool) {
		wrapped := wrapAnsi(content, textWidth)
		for _, line := range wrapped {
			prefix := indent
			if first && icon != "" {
				prefix = icon + " "
				first = false
			}
			if subtle {
				line = m.subtle(line)
			}
			lines = append(lines, prefix+line)
		}
	}
	if d.TopLabel != "" {
		emit(stripCardHTML(d.TopLabel), true)
	}
	if d.Text != "" {
		emit(stripCardHTML(d.Text), false)
	}
	if d.BottomLabel != "" {
		emit(stripCardHTML(d.BottomLabel), true)
	}
	if d.URL != "" {
		emit(d.URL, true)
	}
	if first && icon != "" {
		// No text at all — keep the icon visible on its own line so we
		// don't silently drop the widget.
		lines = append(lines, icon)
	}
	return lines
}

func (m Model) renderButtonList(b *api.ButtonListWidget, width int) []string {
	if b == nil || len(b.Buttons) == 0 {
		return nil
	}
	var lines []string
	for _, btn := range b.Buttons {
		label := stripCardHTML(btn.Text)
		if label == "" {
			label = btn.AltText
		}
		if label == "" {
			continue
		}
		boxed := "[ " + label + " ]"
		if !m.cfg.NoColor {
			boxed = lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Accent)).Bold(true).Render(boxed)
		}
		lines = append(lines, boxed)
		if btn.URL != "" {
			lines = append(lines, m.subtle(truncate(btn.URL, width)))
		}
	}
	return lines
}

func (m Model) renderCardImage(img *api.ImageWidget, width int) []string {
	if img == nil {
		return nil
	}
	label := img.AltText
	if label == "" {
		label = "image"
	}
	lines := []string{m.subtle(truncate("["+label+"]", width))}
	if img.URL != "" {
		lines = append(lines, m.subtle(truncate(img.URL, width)))
	}
	return lines
}

func (m Model) renderColumns(c *api.ColumnsWidget, width int) []string {
	if c == nil {
		return nil
	}
	var lines []string
	for i, col := range c.Columns {
		if i > 0 {
			lines = append(lines, m.subtle(strings.Repeat("·", width)))
		}
		for _, w := range col {
			lines = append(lines, m.renderCardWidget(w, width)...)
		}
	}
	return lines
}

func (m Model) renderGrid(g *api.GridWidget, width int) []string {
	if g == nil {
		return nil
	}
	var lines []string
	if g.Title != "" {
		lines = append(lines, m.subtle(truncate(stripCardHTML(g.Title), width)))
	}
	for _, item := range g.Items {
		bullet := m.icon("•", "*") + " "
		title := stripCardHTML(item.Title)
		lines = append(lines, truncate(bullet+title, width))
		if item.Subtitle != "" {
			lines = append(lines, m.subtle(truncate("  "+stripCardHTML(item.Subtitle), width)))
		}
	}
	return lines
}

// cardIcon resolves a CardIcon to a single-character glyph. knownIcon values
// come from a fixed Google Chat enum; we map the common ones to unicode and
// fall back to a generic bullet so unknown icons still anchor the row.
func (m Model) cardIcon(ic *api.CardIcon) string {
	if ic == nil {
		return ""
	}
	if ic.KnownIcon != "" {
		if glyph, ok := knownIconGlyph(ic.KnownIcon); ok {
			return m.icon(glyph.unicode, glyph.ascii)
		}
		return m.icon("•", "*")
	}
	if ic.IconURL != "" || ic.AltText != "" {
		return m.icon("•", "*")
	}
	return ""
}

type iconGlyph struct{ unicode, ascii string }

// knownIconGlyph maps Google Chat known icons to terminal-safe glyphs. The
// list covers the icons we've actually seen in the wild plus the obvious
// ones from the public enum; anything unrecognized falls back to "•".
func knownIconGlyph(name string) (iconGlyph, bool) {
	switch strings.ToUpper(name) {
	case "PERSON":
		return iconGlyph{"♔", "@"}, true // crowned head — most fonts render this well
	case "MULTIPLE_PEOPLE":
		return iconGlyph{"♕", "@@"}, true
	case "TICKET":
		return iconGlyph{"⚑", "#"}, true // flag — closer than a star
	case "STAR":
		return iconGlyph{"★", "*"}, true
	case "BOOKMARK":
		return iconGlyph{"⚑", "B"}, true
	case "EMAIL", "INVITE":
		return iconGlyph{"✉", "@"}, true
	case "PHONE":
		return iconGlyph{"☎", "T"}, true
	case "VIDEO_CAMERA", "VIDEO_PLAY":
		return iconGlyph{"▶", ">"}, true
	case "CLOCK":
		return iconGlyph{"⧗", "o"}, true
	case "CALENDAR", "EVENT_AVAILABLE":
		return iconGlyph{"▦", "#"}, true
	case "DESCRIPTION":
		return iconGlyph{"▤", "="}, true
	case "MAP_PIN", "MAP":
		return iconGlyph{"⌖", "X"}, true
	case "DOLLAR":
		return iconGlyph{"$", "$"}, true
	case "TRAIN":
		return iconGlyph{"⎒", "T"}, true
	case "AIRPLANE", "FLIGHT_ARRIVAL", "FLIGHT_DEPARTURE":
		return iconGlyph{"✈", "P"}, true
	case "HOTEL":
		return iconGlyph{"⌂", "H"}, true
	case "RESTAURANT":
		return iconGlyph{"☕", "R"}, true
	case "SHOPPING_CART":
		return iconGlyph{"⎄", "$"}, true
	case "CAR":
		return iconGlyph{"⤑", "C"}, true
	case "MEMBERSHIP":
		return iconGlyph{"♥", "v"}, true
	case "OFFER":
		return iconGlyph{"⚑", "%"}, true
	case "CONFIRMATION_NUMBER_ICON":
		return iconGlyph{"#", "#"}, true
	case "BOOKMARK_FILLED":
		return iconGlyph{"⚑", "B"}, true
	case "OPEN_IN_NEW":
		return iconGlyph{"↗", ">"}, true
	}
	return iconGlyph{}, false
}

// wrapBreakpoints is the set of characters ansi.Wrap may break a long word
// after. The wrapped-URL rejoin logic in detail_url.go must use the exact
// same set to reason about where soft wraps happen, so it lives here as a
// shared constant.
const wrapBreakpoints = " -.,;:"

// wrapAnsi wraps a (possibly ANSI-styled) string into lines of at most
// width display cells. lipgloss's ansi.Wrap inserts "-" continuation
// markers; that's appropriate for inline text but not for our card body,
// where we want clean breaks at spaces. The helper falls back to ansi.Wrap
// which already handles ANSI bytes correctly.
func wrapAnsi(value string, width int) []string {
	if value == "" {
		return nil
	}
	value = displayText(value)
	if width <= 0 {
		return []string{value}
	}
	wrapped := ansi.Wrap(value, width, wrapBreakpoints)
	return strings.Split(wrapped, "\n")
}

// stripCardHTML converts Google Chat's basic HTML into plain text. Cards use
// <b>, <i>, <u>, <br>, <a href=...>, <font color=...> and HTML entities. We
// drop the styling tags (terminals can do bold inline if we want, but it
// risks mid-word styling glitches inside wrapped lines) and just keep the
// text content. <a> keeps the visible text; <br> turns into a newline.
func stripCardHTML(value string) string {
	if value == "" {
		return value
	}
	value = strings.ReplaceAll(value, "<br>", "\n")
	value = strings.ReplaceAll(value, "<br/>", "\n")
	value = strings.ReplaceAll(value, "<br />", "\n")
	value = cardHTMLLinkRe.ReplaceAllString(value, "$1")
	value = cardHTMLTagRe.ReplaceAllString(value, "")
	return html.UnescapeString(value)
}

var (
	cardHTMLLinkRe = regexp.MustCompile(`(?i)<a\s+[^>]*>(.*?)</a>`)
	cardHTMLTagRe  = regexp.MustCompile(`<[^>]+>`)
)
