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
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/fabhiantomaoludyo/gws-tui/internal/api"
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
