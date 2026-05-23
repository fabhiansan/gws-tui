package tui

import (
	"regexp"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var detailURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)
var detailURLContinuationPattern = regexp.MustCompile(`^[^\s<>"']+`)

type detailURLOpenErrMsg struct{ err string }

func (m Model) openDetailURLAtCursor() (Model, tea.Cmd, bool) {
	url, ok := detailURLAtCursorWrapped(m.detailLines, m.detailCursor, m.detailCol, m.detailTextWidth())
	if !ok {
		return m, nil, false
	}
	m.toast = "opening browser"
	return m, func() tea.Msg {
		if err := openURL(url); err != nil {
			return detailURLOpenErrMsg{err: err.Error()}
		}
		return nil
	}, true
}

func detailURLAtCursor(lines []string, lineIndex, col int) (string, bool) {
	return detailURLAtCursorWrapped(lines, lineIndex, col, 0)
}

func detailURLAtCursorWrapped(lines []string, lineIndex, col, width int) (string, bool) {
	if lineIndex < 0 || lineIndex >= len(lines) {
		return "", false
	}

	urls := detailURLCandidates(lines, lineIndex, width)
	if len(urls) == 0 {
		return "", false
	}
	for _, candidate := range urls {
		if candidate.contains(lineIndex, col) {
			return candidate.value, true
		}
	}
	if len(urls) == 1 && urls[0].startLine == lineIndex {
		return urls[0].value, true
	}
	return "", false
}

type detailURLCandidate struct {
	value     string
	startLine int
	ranges    []detailURLRange
}

type detailURLRange struct {
	line     int
	startCol int
	endCol   int
}

func (c detailURLCandidate) contains(line, col int) bool {
	for _, r := range c.ranges {
		if r.line == line && col >= r.startCol && col < r.endCol {
			return true
		}
	}
	return false
}

func detailURLCandidates(lines []string, targetLine, width int) []detailURLCandidate {
	var candidates []detailURLCandidate
	for startLine := 0; startLine <= targetLine; startLine++ {
		line := lines[startLine]
		for _, match := range detailURLPattern.FindAllStringIndex(line, -1) {
			candidate, ok := buildDetailURLCandidate(lines, startLine, match[0], match[1], width)
			if !ok || !candidate.coversLine(targetLine) {
				continue
			}
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func (c detailURLCandidate) coversLine(line int) bool {
	for _, r := range c.ranges {
		if r.line == line {
			return true
		}
	}
	return false
}

func buildDetailURLCandidate(lines []string, lineIndex, start, end, width int) (detailURLCandidate, bool) {
	line := lines[lineIndex]
	var b strings.Builder
	ranges := []detailURLRange{{
		line:     lineIndex,
		startCol: utf8.RuneCountInString(line[:start]),
		endCol:   utf8.RuneCountInString(line[:end]),
	}}
	b.WriteString(line[start:end])

	for current := lineIndex; width > 0 && end == len(line) && current+1 < len(lines); current++ {
		next := lines[current+1]
		if !detailLineSoftWrapsInto(line, next, width) {
			break
		}
		nextEnd, ok := detailURLContinuationEnd(next)
		if !ok {
			break
		}
		b.WriteString(next[:nextEnd])
		ranges = append(ranges, detailURLRange{
			line:     current + 1,
			startCol: 0,
			endCol:   utf8.RuneCountInString(next[:nextEnd]),
		})
		line = next
		end = nextEnd
	}

	value := b.String()
	trimmedEnd := trimDetailURLEnd(value, 0, len(value))
	if trimmedEnd <= 0 {
		return detailURLCandidate{}, false
	}
	if trimmedEnd < len(value) {
		trimmedRunes := utf8.RuneCountInString(value[trimmedEnd:])
		ranges = trimDetailURLRanges(ranges, trimmedRunes)
	}
	if len(ranges) == 0 {
		return detailURLCandidate{}, false
	}

	return detailURLCandidate{
		value:     value[:trimmedEnd],
		startLine: lineIndex,
		ranges:    ranges,
	}, true
}

func detailURLContinuationEnd(line string) (int, bool) {
	if line == "" || strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
		return 0, false
	}
	match := detailURLContinuationPattern.FindStringIndex(line)
	if match == nil || match[0] != 0 || match[1] == 0 {
		return 0, false
	}
	return match[1], true
}

// detailLineSoftWrapsInto reports whether `next` is a soft-wrap continuation
// of `line` rather than a separate source line. ansi.Wrap (see wrapAnsi)
// pushes a word onto a new row only when the current row plus that word
// exceeds the wrap width, so the two rows belong to the same logical line
// exactly when joining the first word of `next` back onto `line` would
// overflow. This must not assume soft-wrapped lines are near-full width:
// ansi.Wrap breaks early at "." and "-" inside long URLs, leaving short
// rows that are still continuations.
func detailLineSoftWrapsInto(line, next string, width int) bool {
	if width <= 0 {
		return false
	}
	lineWidth := lipgloss.Width(line)
	// A row filled to the wrap target was clearly broken by width alone.
	if lineWidth >= width {
		return true
	}
	wordWidth := lipgloss.Width(detailFirstWrapWord(next))
	if wordWidth == 0 {
		return false
	}
	return lineWidth+wordWidth > width
}

// detailFirstWrapWord returns the leading run of `next` up to and including
// the first ansi.Wrap breakpoint — the "word" ansi.Wrap would try to fit
// onto the previous row before deciding whether to wrap.
func detailFirstWrapWord(next string) string {
	for i, r := range next {
		if strings.ContainsRune(wrapBreakpoints, r) {
			return next[:i+utf8.RuneLen(r)]
		}
	}
	return next
}

func trimDetailURLRanges(ranges []detailURLRange, runes int) []detailURLRange {
	for runes > 0 && len(ranges) > 0 {
		last := len(ranges) - 1
		available := ranges[last].endCol - ranges[last].startCol
		if runes < available {
			ranges[last].endCol -= runes
			break
		}
		runes -= available
		ranges = ranges[:last]
	}
	return ranges
}

func trimDetailURLEnd(line string, start, end int) int {
	for end > start {
		r, size := utf8.DecodeLastRuneInString(line[start:end])
		if !strings.ContainsRune(".,;:!?", r) && !isUnbalancedClosingURLRune(line[start:end], r) {
			break
		}
		end -= size
	}
	return end
}

func isUnbalancedClosingURLRune(value string, r rune) bool {
	switch r {
	case ')':
		return strings.Count(value, ")") > strings.Count(value, "(")
	case ']':
		return strings.Count(value, "]") > strings.Count(value, "[")
	case '}':
		return strings.Count(value, "}") > strings.Count(value, "{")
	default:
		return false
	}
}
