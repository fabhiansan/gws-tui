package tui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/fabhiantomaoludyo/gws-tui/internal/api"
)

const (
	maxInlineImageBytes = int64(10 << 20)
	inlineImageRows     = 8
)

func (m *Model) imageDownloadCmdsForWorkspace() []tea.Cmd {
	var cmds []tea.Cmd
	cmds = append(cmds, m.imageDownloadCmdsForChat(m.chatMessages)...)
	cmds = append(cmds, m.imageDownloadCmdsForMail(m.mailThreads)...)
	return cmds
}

func (m *Model) imageDownloadCmdsForChat(messages []api.ChatMessage) []tea.Cmd {
	var attachments []api.Attachment
	for _, message := range messages {
		attachments = append(attachments, message.Attachments...)
	}
	return m.imageDownloadCmdsForAttachments(attachments)
}

func (m *Model) imageDownloadCmdsForMail(threads []api.MailThread) []tea.Cmd {
	var attachments []api.Attachment
	for _, thread := range threads {
		attachments = append(attachments, thread.Attachments...)
	}
	return m.imageDownloadCmdsForAttachments(attachments)
}

func (m *Model) imageDownloadCmdsForCurrentDetail() []tea.Cmd {
	switch m.feature {
	case FeatureChat:
		return m.imageDownloadCmdsForChat(m.chatMessages)
	case FeatureMail:
		thread := m.selectedMail()
		if thread.ID == "" {
			return nil
		}
		return m.imageDownloadCmdsForMail([]api.MailThread{thread})
	default:
		return nil
	}
}

func (m *Model) imageDownloadCmdsForAttachments(attachments []api.Attachment) []tea.Cmd {
	if !m.cfg.InlineImages || m.cfg.ImageCacheDir == "" {
		return nil
	}
	if m.imageFiles == nil {
		m.imageFiles = map[string]string{}
	}
	if m.imageLoading == nil {
		m.imageLoading = map[string]bool{}
	}
	if m.imageErrors == nil {
		m.imageErrors = map[string]string{}
	}

	var cmds []tea.Cmd
	for _, attachment := range attachments {
		if !attachment.IsImage() {
			continue
		}
		source := attachment.PreviewSource()
		if source == "" {
			continue
		}
		if local, ok := localImagePath(source); ok {
			m.imageFiles[source] = local
			continue
		}
		if attachment.MediaResourceName() == "" && !isRemoteImageSource(source) {
			continue
		}
		cachePath := imageCachePath(m.cfg.ImageCacheDir, attachment)
		if fileExists(cachePath) {
			m.imageFiles[source] = cachePath
			delete(m.imageErrors, source)
			continue
		}
		if m.imageErrors[source] != "" {
			continue
		}
		if m.imageLoading[source] {
			continue
		}
		m.imageLoading[source] = true
		delete(m.imageErrors, source)
		cmds = append(cmds, downloadImageCmd(m.ctx, m.client, attachment, cachePath))
	}
	return cmds
}

func downloadImageCmd(ctx context.Context, client api.WorkspaceClient, attachment api.Attachment, cachePath string) tea.Cmd {
	return func() tea.Msg {
		source := attachment.PreviewSource()
		if source == "" {
			source = attachment.MediaResourceName()
		}
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		err := downloadImage(c, client, attachment, cachePath)
		if err != nil {
			return imageCachedMsg{source: source, err: err}
		}
		return imageCachedMsg{source: source, path: cachePath}
	}
}

func (m Model) renderAttachments(attachments []api.Attachment) []string {
	attachments = api.NormalizeAttachments(attachments)
	if len(attachments) == 0 {
		return nil
	}

	width := max(16, m.detail.Width-4)
	lines := make([]string, 0, len(attachments)*2)
	for _, attachment := range attachments {
		source := attachment.PreviewSource()
		label := attachment.DisplayName()
		if attachment.IsImage() {
			lines = append(lines, m.subtle("  [image] "+truncate(label, width-10)))
			if preview, ok := m.renderInlineImage(attachment); ok {
				lines = append(lines, preview)
				continue
			}
			lines = append(lines, m.subtle("  "+truncate(imageFallback(source, m.imageLoading[source], m.imageErrors[source]), width)))
			continue
		}

		fallback := "[attachment] " + label
		if source != "" {
			fallback += "  " + source
		}
		lines = append(lines, m.subtle("  "+truncate(fallback, width)))
	}
	return lines
}

func (m Model) renderInlineImage(attachment api.Attachment) (string, bool) {
	if !m.cfg.InlineImages || !isKittyTerminal() {
		return "", false
	}
	source := attachment.PreviewSource()
	local, ok := m.previewImagePath(source)
	if !ok {
		return "", false
	}
	cols := min(48, max(16, m.detail.Width-6))
	rendered, err := kittyImage(local, source, cols, inlineImageRows)
	if err != nil {
		return "", false
	}
	return rendered, true
}

func (m Model) previewImagePath(source string) (string, bool) {
	if source == "" {
		return "", false
	}
	if local, ok := localImagePath(source); ok {
		return local, true
	}
	if cached, ok := m.imageFiles[source]; ok && fileExists(cached) {
		return cached, true
	}
	if m.cfg.ImageCacheDir == "" || !isRemoteImageSource(source) {
		return "", false
	}
	cached := imageCachePath(m.cfg.ImageCacheDir, api.Attachment{URL: source})
	if fileExists(cached) {
		return cached, true
	}
	return "", false
}

func kittyImage(file, source string, columns, rows int) (string, error) {
	imageID := kittyImageID(source, columns, rows)
	img, err := decodeImage(file)
	if err != nil {
		return "", err
	}
	bounds := img.Bounds()
	var buf bytes.Buffer
	// Kitty 0.46 silently refuses Unicode-placeholder display for f=100
	// (PNG) and for t=f/t=t (file/tempfile) under U=1. The only reliable
	// path is what `kitten icat --unicode-placeholder` itself uses: decode
	// to raw RGB, zlib-compress, and transmit inline via a=T with explicit
	// image pixel dimensions. C=1 keeps the cursor in place so the
	// placeholder grid that follows lands on the same row.
	err = kitty.EncodeGraphics(&buf, img, &kitty.Options{
		Action:           kitty.TransmitAndPut,
		Transmission:     kitty.Direct,
		Format:           kitty.RGB,
		Compression:      kitty.Zlib,
		ID:               imageID,
		ImageWidth:       bounds.Dx(),
		ImageHeight:      bounds.Dy(),
		Columns:          columns,
		Rows:             rows,
		VirtualPlacement: true,
		DoNotMoveCursor:  true,
		Quite:            2,
		Chunk:            true,
	})
	if err != nil {
		return "", err
	}
	buf.WriteString(kittyPlaceholderGrid(imageID, columns, rows))
	return buf.String(), nil
}

func decodeImage(file string) (image.Image, error) {
	in, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	img, _, err := image.Decode(in)
	return img, err
}

func kittyImageID(source string, columns, rows int) int {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", source, columns, rows)))
	return nonzero24BitID(sum[0], sum[1], sum[2])
}

func nonzero24BitID(a, b, c byte) int {
	id := int(a)<<16 | int(b)<<8 | int(c)
	if id == 0 {
		return 1
	}
	return id
}

// kittyPlaceholderGrid emits one Unicode placeholder per cell, tagged with
// the image id via the foreground color and a (row, col, image-id MSB)
// diacritic triple. Three diacritics are required even when the MSB is zero
// — kitty only treats the cell as a placeholder when all three are present.
func kittyPlaceholderGrid(imageID, columns, rows int) string {
	var b strings.Builder
	r, g, bl := colorParts(imageID)
	msb := kitty.Diacritic((imageID >> 24) & 0xff)
	for row := 0; row < rows; row++ {
		if row > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm", r, g, bl)
		for col := 0; col < columns; col++ {
			b.WriteRune(kitty.Placeholder)
			b.WriteRune(kitty.Diacritic(row))
			b.WriteRune(kitty.Diacritic(col))
			b.WriteRune(msb)
		}
		b.WriteString("\x1b[39m")
	}
	return b.String()
}

func colorParts(id int) (r, g, b int) {
	return (id >> 16) & 0xff, (id >> 8) & 0xff, id & 0xff
}

func downloadImage(ctx context.Context, client api.WorkspaceClient, attachment api.Attachment, cachePath string) error {
	if attachment.MediaResourceName() != "" && client != nil {
		return client.DownloadAttachment(ctx, attachment, cachePath)
	}
	source := attachment.PreviewSource()
	if !isRemoteImageSource(source) {
		return fmt.Errorf("unsupported image source: %s", source)
	}
	httpClient := http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "gws-tui")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("image request failed: %s", resp.Status)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if contentType != "" && !strings.HasPrefix(contentType, "image/") {
		return fmt.Errorf("response is not an image: %s", contentType)
	}
	if contentType == "" && !(api.Attachment{URL: source}).IsImage() {
		return errors.New("image content type missing")
	}

	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(cachePath), ".image-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	written, copyErr := io.Copy(tmp, io.LimitReader(resp.Body, maxInlineImageBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maxInlineImageBytes {
		return fmt.Errorf("image exceeds %d bytes", maxInlineImageBytes)
	}
	if err := os.Rename(tmpPath, cachePath); err != nil {
		return err
	}
	return nil
}

func imageCachePath(dir string, attachment api.Attachment) string {
	source := attachment.PreviewSource()
	if source == "" {
		source = attachment.MediaResourceName()
	}
	sum := sha256.Sum256([]byte(source))
	ext := imageExt(attachment)
	if ext == "" {
		ext = ".png"
	}
	return filepath.Join(dir, hex.EncodeToString(sum[:])+ext)
}

func localImagePath(source string) (string, bool) {
	if source == "" {
		return "", false
	}
	if parsed, err := url.Parse(source); err == nil && parsed.Scheme == "file" {
		file := parsed.Path
		return file, fileExists(file)
	}
	if strings.HasPrefix(source, "~/") {
		source = expandHome(source)
	}
	if filepath.IsAbs(source) || strings.HasPrefix(source, ".") {
		return source, fileExists(source)
	}
	return "", false
}

func isRemoteImageSource(source string) bool {
	parsed, err := url.Parse(source)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func isKittyTerminal() bool {
	return os.Getenv("TERM") == "xterm-kitty" || os.Getenv("KITTY_WINDOW_ID") != ""
}

func imageFallback(source string, loading bool, downloadErr string) string {
	switch {
	case source == "":
		return "preview unavailable"
	case loading:
		return "loading preview: " + source
	case downloadErr != "":
		return "preview failed: " + downloadErr
	default:
		return "preview unavailable: " + source
	}
}

func imageExt(attachment api.Attachment) string {
	if ext := extFromContentType(attachment.ContentType); ext != "" {
		return ext
	}
	source := attachment.PreviewSource()
	if parsed, err := url.Parse(source); err == nil && parsed.Scheme != "" {
		if ext := extFromContentType(parsed.Query().Get("content_type")); ext != "" {
			return ext
		}
		return path.Ext(parsed.Path)
	}
	return filepath.Ext(source)
}

func extFromContentType(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/svg+xml":
		return ".svg"
	default:
		return ""
	}
}

func fileExists(file string) bool {
	stat, err := os.Stat(file)
	return err == nil && stat.Mode().IsRegular()
}
