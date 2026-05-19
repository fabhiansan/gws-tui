package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/fabhiansan/gws-tui/internal/api"
)

func TestRenderAttachmentsFallback(t *testing.T) {
	model := Model{
		cfg:    Config{InlineImages: true},
		detail: viewport.New(80, 20),
	}

	lines := model.renderAttachments([]api.Attachment{{
		Name: "photo.png",
		URL:  "https://example.com/photo.png",
	}})

	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "[image] photo.png") || !strings.Contains(got, "preview unavailable") {
		t.Fatalf("unexpected fallback render: %q", got)
	}
}

func TestRenderAttachmentsKittyPreview(t *testing.T) {
	t.Setenv("TERM", "xterm-kitty")
	dir := t.TempDir()
	file := filepath.Join(dir, "photo.png")
	writeTestPNG(t, file, 8, 8)
	source := "https://example.com/photo.png"
	model := Model{
		cfg:        Config{InlineImages: true},
		detail:     viewport.New(80, 20),
		imageFiles: map[string]string{source: file},
	}
	warmImageFrame(t, &model, file, source, min(48, max(16, model.detail.Width-6)), inlineImageRows)

	lines := model.renderAttachments([]api.Attachment{{
		Name: "photo.png",
		URL:  source,
	}})

	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "\x1b_G") {
		t.Fatalf("expected kitty APC escape, got %q", got)
	}
	// probe15 layout: raw RGB (f=24), zlib (o=z), transmit+put (a=T), virtual
	// placement (U=1), don't move cursor (C=1), no placement_id and no
	// underline color escape on the placeholder cells.
	for _, want := range []string{"f=24", "o=z", "a=T", "U=1", "C=1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected kitty APC to include %q, got %q", want, got)
		}
	}
	if !strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("expected foreground color encoding image id, got %q", got)
	}
	if strings.Contains(got, "\x1b[58;2;") {
		t.Fatalf("placeholder grid should no longer emit an underline color, got %q", got)
	}
	if !strings.Contains(got, string(kitty.Placeholder)) {
		t.Fatalf("expected kitty unicode placeholder grid, got %q", got)
	}
	// Each placeholder must be followed by three diacritics (row, col, msb).
	r := []rune(got)
	for i, ch := range r {
		if ch != kitty.Placeholder {
			continue
		}
		if i+3 >= len(r) {
			t.Fatalf("placeholder at end of output without diacritic triple")
		}
		for j := 1; j <= 3; j++ {
			if !isDiacritic(r[i+j]) {
				t.Fatalf("expected diacritic at offset %d after placeholder, got %U", j, r[i+j])
			}
		}
	}
}

func TestRenderAttachmentsKittyPreviewReemitsAPC(t *testing.T) {
	t.Setenv("TERM", "xterm-kitty")
	dir := t.TempDir()
	file := filepath.Join(dir, "photo.png")
	writeTestPNG(t, file, 8, 8)
	source := "https://example.com/photo.png"
	model := Model{
		cfg:        Config{InlineImages: true},
		detail:     viewport.New(80, 20),
		imageFiles: map[string]string{source: file},
	}
	warmImageFrame(t, &model, file, source, min(48, max(16, model.detail.Width-6)), inlineImageRows)
	attachment := []api.Attachment{{
		Name: "photo.png",
		URL:  source,
	}}

	first := strings.Join(model.renderAttachments(attachment), "\n")
	if !strings.Contains(first, "\x1b_G") {
		t.Fatalf("first render should transmit kitty image, got %q", first)
	}
	if model.imagePlacement != 1 {
		t.Fatalf("first render should register one image placement, got %d", model.imagePlacement)
	}

	// The viewport content for every frame must carry the APC payload.
	// Returning only the placeholder grid on cache hits causes the image
	// to vanish whenever bubbletea reuses the cached frame, resizes, or
	// kitty evicts its image store.
	second := strings.Join(model.renderAttachments(attachment), "\n")
	if !strings.Contains(second, "\x1b_G") {
		t.Fatalf("second render must keep emitting the kitty APC payload, got %q", second)
	}
	if !strings.Contains(second, string(kitty.Placeholder)) {
		t.Fatalf("second render should keep kitty placeholders, got %q", second)
	}
	if model.imagePlacement != 1 {
		t.Fatalf("cached render should not register a new placement, placements=%d", model.imagePlacement)
	}
}

func writeTestPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 16), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// warmImageFrame synchronously runs the precompute pipeline so render tests
// can assert against the cached frame without going through tea.Cmd dispatch.
func warmImageFrame(t *testing.T, model *Model, file, source string, cols, rows int) {
	t.Helper()
	msg, ok := precomputeImageFrameCmd(file, source, cols, rows)().(imageFrameReadyMsg)
	if !ok {
		t.Fatal("precomputeImageFrameCmd did not produce imageFrameReadyMsg")
	}
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if model.imageRenders == nil {
		model.imageRenders = map[string]inlineImageRender{}
	}
	model.imageRenders[msg.key] = inlineImageRender{
		file:        msg.file,
		source:      msg.source,
		columns:     msg.columns,
		rows:        msg.rows,
		size:        msg.size,
		modTime:     msg.modTime,
		full:        msg.full,
		placeholder: msg.placeholder,
	}
	model.imagePlacement++
}

func isDiacritic(r rune) bool {
	// Cheap check: kitty's diacritic table is all combining marks above U+0300.
	// Unicode classifies them as Mn / Me, but here we just need a sanity check.
	return r >= 0x0300
}

func TestCurrentDetailSchedulesImageDownload(t *testing.T) {
	source := "spaces/AAA/messages/BBB/attachments/CCC"
	model := Model{
		cfg: Config{
			InlineImages:  true,
			ImageCacheDir: t.TempDir(),
		},
		feature: FeatureChat,
		detail:  viewport.New(80, 20),
		chatMessages: []api.ChatMessage{{
			ID:    "msg-1",
			Space: "spaces/AAA",
			Attachments: []api.Attachment{{
				ResourceName: source,
				Name:         "image.png",
				ContentType:  "image/png",
			}},
		}},
		imageFiles:   map[string]string{},
		imageLoading: map[string]bool{},
		imageErrors:  map[string]string{},
	}

	cmds := model.imageDownloadCmdsForCurrentDetail()
	if len(cmds) != 1 {
		t.Fatalf("expected one image download command, got %d", len(cmds))
	}
	if !model.imageLoading[source] {
		t.Fatalf("expected image source %q to be marked loading", source)
	}
}

func TestImageCacheHitInvalidatesDetailRender(t *testing.T) {
	dir := t.TempDir()
	attachment := api.Attachment{
		Name:        "photo.png",
		URL:         "https://example.com/photo.png",
		ContentType: "image/png",
	}
	cachePath := attachment.CachePath(dir)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestPNG(t, cachePath, 4, 4)

	model := Model{
		cfg: Config{
			InlineImages:  true,
			ImageCacheDir: dir,
		},
		imageFiles:   map[string]string{},
		imageLoading: map[string]bool{},
		imageErrors:  map[string]string{},
	}

	cmds := model.imageDownloadCmdsForAttachments([]api.Attachment{attachment})
	if len(cmds) != 0 {
		t.Fatalf("cache hit should not schedule downloads, got %d", len(cmds))
	}
	if model.imageFiles[attachment.URL] != cachePath {
		t.Fatalf("cache hit should register image file: got %q want %q", model.imageFiles[attachment.URL], cachePath)
	}
	if model.imageVersion != 1 {
		t.Fatalf("cache hit should invalidate image render once, got version %d", model.imageVersion)
	}

	model.imageDownloadCmdsForAttachments([]api.Attachment{attachment})
	if model.imageVersion != 1 {
		t.Fatalf("unchanged cache hit should not keep invalidating, got version %d", model.imageVersion)
	}
}

func TestDownscaleForCellsShrinksLargeImages(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4000, 3000))
	scaled := downscaleForCells(src, 48, inlineImageRows)
	bounds := scaled.Bounds()
	maxW := 48 * kittyCellPxW
	maxH := inlineImageRows * kittyCellPxH
	if bounds.Dx() > maxW || bounds.Dy() > maxH {
		t.Fatalf("downscale failed: got %dx%d, budget %dx%d", bounds.Dx(), bounds.Dy(), maxW, maxH)
	}
	// Aspect ratio of the source (4:3) should be preserved within a pixel.
	srcRatio := 4000.0 / 3000.0
	dstRatio := float64(bounds.Dx()) / float64(bounds.Dy())
	if dstRatio < srcRatio-0.02 || dstRatio > srcRatio+0.02 {
		t.Fatalf("downscale changed aspect ratio: src=%.3f dst=%.3f", srcRatio, dstRatio)
	}
}

func TestDownscaleForCellsSkipsSmallImages(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 32, 32))
	scaled := downscaleForCells(src, 48, inlineImageRows)
	if scaled != image.Image(src) {
		t.Fatalf("expected source to be returned unchanged when within budget")
	}
}

func TestRenderAttachmentsTrackedReturnsImageRange(t *testing.T) {
	t.Setenv("TERM", "xterm-kitty")
	dir := t.TempDir()
	file := filepath.Join(dir, "photo.png")
	writeTestPNG(t, file, 8, 8)
	source := "https://example.com/photo.png"
	model := Model{
		cfg:        Config{InlineImages: true},
		detail:     viewport.New(80, 20),
		imageFiles: map[string]string{source: file},
	}
	warmImageFrame(t, &model, file, source, min(48, max(16, model.detail.Width-6)), inlineImageRows)

	lines, ranges := model.renderAttachmentsTracked([]api.Attachment{{
		Name: "photo.png",
		URL:  source,
	}})

	if len(lines) != 2 {
		t.Fatalf("expected 2 slice entries (label + preview), got %d", len(lines))
	}
	if len(ranges) != 1 {
		t.Fatalf("expected one tracked range, got %d", len(ranges))
	}
	r := ranges[0]
	if r.start != 0 || r.rows != 1+inlineImageRows {
		t.Fatalf("unexpected range: start=%d rows=%d, want start=0 rows=%d", r.start, r.rows, 1+inlineImageRows)
	}
	if r.attachment.URL != source {
		t.Fatalf("range attachment did not round-trip: %q", r.attachment.URL)
	}
}

func TestRenderAttachmentsTrackedMapsFallback(t *testing.T) {
	model := Model{
		cfg:    Config{InlineImages: true},
		detail: viewport.New(80, 20),
	}
	lines, ranges := model.renderAttachmentsTracked([]api.Attachment{{
		Name: "photo.png",
		URL:  "https://example.com/photo.png",
	}})
	if len(lines) != 2 {
		t.Fatalf("expected label + fallback line, got %d: %#v", len(lines), lines)
	}
	if len(ranges) != 1 || ranges[0].rows != 2 {
		t.Fatalf("expected fallback range of 2 rows, got %#v", ranges)
	}
}

func TestAppendAttachmentLinesMapsSecondImagePastMultilinePreview(t *testing.T) {
	t.Setenv("TERM", "xterm-kitty")
	dir := t.TempDir()
	file1 := filepath.Join(dir, "first.png")
	file2 := filepath.Join(dir, "second.png")
	writeTestPNG(t, file1, 8, 8)
	writeTestPNG(t, file2, 8, 8)
	src1 := "https://example.com/first.png"
	src2 := "https://example.com/second.png"

	model := Model{
		cfg:           Config{InlineImages: true},
		detail:        viewport.New(80, 20),
		imageFiles:    map[string]string{src1: file1, src2: file2},
		detailImageAt: map[int]api.Attachment{},
	}
	cols := min(48, max(16, model.detail.Width-6))
	warmImageFrame(t, &model, file1, src1, cols, inlineImageRows)
	warmImageFrame(t, &model, file2, src2, cols, inlineImageRows)

	att1 := api.Attachment{Name: "first.png", URL: src1}
	att2 := api.Attachment{Name: "second.png", URL: src2}

	lines := []string{"Alice    14:30", "  hello"}
	lines = model.appendAttachmentLines(lines, []api.Attachment{att1}, 0)
	lines = append(lines, "", "Bob    14:31", "  reply")
	lines = model.appendAttachmentLines(lines, []api.Attachment{att2}, 0)

	plain := strings.Split(strings.Join(lines, "\n"), "\n")
	for line, att := range model.detailImageAt {
		if line < 0 || line >= len(plain) {
			t.Fatalf("detailImageAt index %d out of bounds (have %d lines)", line, len(plain))
		}
		switch att.URL {
		case src1:
			if !strings.Contains(plain[line], "first.png") && !strings.Contains(plain[line], string(kitty.Placeholder)) {
				t.Fatalf("line %d mapped to first.png but content is %q", line, plain[line])
			}
		case src2:
			if !strings.Contains(plain[line], "second.png") && !strings.Contains(plain[line], string(kitty.Placeholder)) {
				t.Fatalf("line %d mapped to second.png but content is %q", line, plain[line])
			}
		default:
			t.Fatalf("unexpected attachment URL: %q", att.URL)
		}
	}
}

func TestEnterOnImageLineOpensViewer(t *testing.T) {
	t.Setenv("TERM", "xterm-kitty")
	dir := t.TempDir()
	file := filepath.Join(dir, "photo.png")
	writeTestPNG(t, file, 8, 8)
	source := "https://example.com/photo.png"
	att := api.Attachment{Name: "photo.png", URL: source}

	model := Model{
		cfg:           Config{InlineImages: true},
		detail:        viewport.New(80, 20),
		focusedPane:   paneDetail,
		imageFiles:    map[string]string{source: file},
		detailImageAt: map[int]api.Attachment{5: att},
		detailCursor:  5,
		detailLines:   make([]string, 10),
	}
	model.detailLineCount = len(model.detailLines)

	updated, _ := model.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.imageViewer == nil {
		t.Fatalf("expected viewer to open, toast=%q", updated.toast)
	}
	if updated.imageViewer.path != file {
		t.Fatalf("viewer path mismatch: %q", updated.imageViewer.path)
	}

	closed, _ := updated.updateKey(tea.KeyMsg{Type: tea.KeyEsc})
	if closed.imageViewer != nil {
		t.Fatalf("expected esc to close viewer")
	}
}

func TestEnterAwayFromImageFallsBack(t *testing.T) {
	model := Model{
		cfg:           Config{InlineImages: true},
		detail:        viewport.New(80, 20),
		focusedPane:   paneDetail,
		feature:       FeatureChat,
		detailImageAt: map[int]api.Attachment{},
		detailCursor:  3,
		detailLines:   make([]string, 10),
	}
	model.detailLineCount = len(model.detailLines)

	updated, _ := model.updateKey(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.imageViewer != nil {
		t.Fatalf("viewer should not open when cursor is not on an image line")
	}
	if updated.toast == "" {
		t.Fatalf("expected fallback openHint toast")
	}
}

func TestImageDownloadSkipsFailedSourceUntilRefresh(t *testing.T) {
	source := "spaces/AAA/messages/BBB/attachments/CCC"
	model := Model{
		cfg: Config{
			InlineImages:  true,
			ImageCacheDir: t.TempDir(),
		},
		imageFiles:   map[string]string{},
		imageLoading: map[string]bool{},
		imageErrors:  map[string]string{source: "download failed"},
	}

	cmds := model.imageDownloadCmdsForAttachments([]api.Attachment{{
		ResourceName: source,
		Name:         "image.png",
		ContentType:  "image/png",
	}})
	if len(cmds) != 0 {
		t.Fatalf("expected failed image source to be skipped, got %d commands", len(cmds))
	}
}
