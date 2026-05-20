package tui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/fabhiansan/gws-tui/internal/api"
	"golang.org/x/image/draw"
)

const (
	maxInlineImageBytes = int64(10 << 20)
	inlineImageRows     = 8

	// Approximate pixel dimensions of one terminal cell. Used to derive a
	// downscale budget so the bitmap we hand to Kitty stays close to what's
	// actually visible on screen — a 4000×3000 PNG displayed in 8 rows is
	// otherwise transmitted at full resolution on every redraw and tanks
	// scroll perf.
	kittyCellPxW = 10
	kittyCellPxH = 20
)

type inlineImageRender struct {
	file        string
	source      string
	columns     int
	rows        int
	size        int64
	modTime     time.Time
	full        string
	placeholder string
}

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
			if m.imageFiles[source] != local {
				m.imageFiles[source] = local
				m.imageVersion++
			}
			continue
		}
		if attachment.MediaResourceName() == "" && !isRemoteImageSource(source) {
			continue
		}
		cachePath := attachment.CachePath(m.cfg.ImageCacheDir)
		if fileExists(cachePath) {
			changed := false
			if m.imageFiles[source] != cachePath {
				m.imageFiles[source] = cachePath
				changed = true
			}
			if m.imageErrors[source] != "" {
				delete(m.imageErrors, source)
				changed = true
			}
			if changed {
				m.imageVersion++
			}
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
		m.imageVersion++
		if m.cfg.Daemon {
			// Daemon owns the download; we just wait for image.cached.
			continue
		}
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

// attachmentLineRange records which lines of the attachment block belong to
// which attachment, so the chat-history Enter handler can look up the
// attachment under the vim cursor without re-walking message attachments.
// `start` is relative to the first attachment line returned alongside.
type attachmentLineRange struct {
	start      int
	rows       int
	attachment api.Attachment
}

func (m *Model) renderAttachments(attachments []api.Attachment) []string {
	lines, _ := m.renderAttachmentsTracked(attachments)
	return lines
}

func (m *Model) renderAttachmentsTracked(attachments []api.Attachment) ([]string, []attachmentLineRange) {
	attachments = api.NormalizeAttachments(attachments)
	if len(attachments) == 0 {
		return nil, nil
	}

	width := max(16, m.detail.Width-4)
	lines := make([]string, 0, len(attachments)*2)
	var ranges []attachmentLineRange
	for _, attachment := range attachments {
		source := attachment.PreviewSource()
		label := attachment.DisplayName()
		if attachment.IsImage() {
			// Line offset within the attLines result (after detailContent
			// joins+splits, internal "\n" inside the kitty preview entry
			// expand into separate detailLines entries — so we have to
			// count real lines, not slice indices).
			start := countDisplayLines(lines)
			lines = append(lines, m.subtle("  [image] "+truncate(label, width-10)))
			if preview, ok := m.renderInlineImage(attachment); ok {
				lines = append(lines, preview)
				rows := 1 + strings.Count(preview, "\n") + 1
				ranges = append(ranges, attachmentLineRange{
					start:      start,
					rows:       rows,
					attachment: attachment,
				})
				continue
			}
			lines = append(lines, m.subtle("  "+truncate(imageFallback(source, m.imageLoading[source], m.imageErrors[source]), width)))
			ranges = append(ranges, attachmentLineRange{
				start:      start,
				rows:       2,
				attachment: attachment,
			})
			continue
		}

		fallback := "[attachment] " + label
		if source != "" {
			fallback += "  " + source
		}
		start := countDisplayLines(lines)
		lines = append(lines, m.subtle("  "+truncate(fallback, width)))
		ranges = append(ranges, attachmentLineRange{
			start:      start,
			rows:       1,
			attachment: attachment,
		})
	}
	return lines, ranges
}

// countDisplayLines reports how many lines a slice of strings will occupy
// once joined with "\n" and split again — the form detailContent feeds into
// decorateDetail. A multi-line slice entry (the kitty image preview is one
// such entry, holding inlineImageRows newline-separated placeholder rows)
// expands into multiple detailLines entries, so a plain len(slice) lookup
// loses the real cursor line.
func countDisplayLines(lines []string) int {
	n := 0
	for _, l := range lines {
		n += strings.Count(l, "\n") + 1
	}
	return n
}

func (m *Model) renderInlineImage(attachment api.Attachment) (string, bool) {
	if !m.cfg.InlineImages || !isKittyTerminal() {
		return "", false
	}
	source := attachment.PreviewSource()
	local, ok := m.previewImagePath(source)
	if !ok {
		return "", false
	}
	cols := min(48, max(16, m.detail.Width-6))
	rendered, ok := m.cachedKittyImage(local, source, cols, inlineImageRows)
	if !ok {
		return "", false
	}
	return rendered, true
}

// cachedKittyImage returns the cached kitty escape sequence for an image, or
// an empty string when the frame hasn't been precomputed yet. It never blocks
// on decode/encode — those run via precomputeImageFrameCmd outside View.
func (m *Model) cachedKittyImage(file, source string, columns, rows int) (string, bool) {
	stat, err := os.Stat(file)
	if err != nil {
		return "", false
	}
	key := inlineImageRenderKey(file, source, columns, rows)
	if cached, ok := m.imageRenders[key]; ok &&
		cached.size == stat.Size() &&
		cached.modTime.Equal(stat.ModTime()) {
		// Always re-emit the APC transmission. Kitty treats a retransmit
		// under the same image ID as a no-op for display purposes, but
		// it's required because bubbletea's viewport content is the only
		// source of truth for the next frame — if we returned the
		// placeholder alone, a resize, redraw, or kitty cache eviction
		// would leave the cells empty.
		return cached.full, true
	}
	return "", false
}

// imageFrameReadyMsg carries a precomputed kitty frame ready to be cached
// on the model. Emitted by precomputeImageFrameCmd.
type imageFrameReadyMsg struct {
	key         string
	file        string
	source      string
	columns     int
	rows        int
	size        int64
	modTime     time.Time
	full        string
	placeholder string
	err         error
}

// precomputeImageFrameCmd does the expensive decode+kitty-encode work in a
// goroutine so View() can stay responsive. The result is fed back to Update
// via imageFrameReadyMsg.
func precomputeImageFrameCmd(file, source string, columns, rows int) tea.Cmd {
	return func() tea.Msg {
		stat, err := os.Stat(file)
		if err != nil {
			return imageFrameReadyMsg{file: file, source: source, columns: columns, rows: rows, err: err}
		}
		full, placeholder, err := kittyImageFrame(file, source, columns, rows)
		if err != nil {
			return imageFrameReadyMsg{file: file, source: source, columns: columns, rows: rows, err: err}
		}
		return imageFrameReadyMsg{
			key:         inlineImageRenderKey(file, source, columns, rows),
			file:        file,
			source:      source,
			columns:     columns,
			rows:        rows,
			size:        stat.Size(),
			modTime:     stat.ModTime(),
			full:        full,
			placeholder: placeholder,
		}
	}
}

// precomputeFrameCmdsForAttachments enumerates image attachments and queues
// precompute commands for any whose frames are not yet cached or in-flight.
func (m *Model) precomputeFrameCmdsForAttachments(attachments []api.Attachment) []tea.Cmd {
	if !m.cfg.InlineImages || !isKittyTerminal() || m.detail.Width <= 0 {
		return nil
	}
	cols := min(48, max(16, m.detail.Width-6))
	rows := inlineImageRows
	var cmds []tea.Cmd
	for _, attachment := range api.NormalizeAttachments(attachments) {
		if !attachment.IsImage() {
			continue
		}
		source := attachment.PreviewSource()
		if source == "" {
			continue
		}
		file, ok := m.previewImagePath(source)
		if !ok {
			continue
		}
		key := inlineImageRenderKey(file, source, cols, rows)
		if _, ok := m.imageRenders[key]; ok {
			continue
		}
		if m.imageFramePend[key] {
			continue
		}
		m.imageFramePend[key] = true
		cmds = append(cmds, precomputeImageFrameCmd(file, source, cols, rows))
	}
	return cmds
}

func (m *Model) precomputeFrameCmdsForChat(messages []api.ChatMessage) []tea.Cmd {
	var attachments []api.Attachment
	for _, message := range messages {
		attachments = append(attachments, message.Attachments...)
	}
	return m.precomputeFrameCmdsForAttachments(attachments)
}

func (m *Model) precomputeFrameCmdsForMail(threads []api.MailThread) []tea.Cmd {
	var attachments []api.Attachment
	for _, thread := range threads {
		attachments = append(attachments, thread.Attachments...)
	}
	return m.precomputeFrameCmdsForAttachments(attachments)
}

// precomputeFrameCmdsForCurrentDetail picks up the attachments currently
// visible in the detail pane and queues precompute for them. Called after
// events that introduce new image paths (download finished, frame ready,
// space switched).
func (m *Model) precomputeFrameCmdsForCurrentDetail() []tea.Cmd {
	switch m.feature {
	case FeatureChat:
		return m.precomputeFrameCmdsForChat(m.chatMessages)
	case FeatureMail:
		thread := m.selectedMail()
		if thread.ID == "" {
			return nil
		}
		return m.precomputeFrameCmdsForMail([]api.MailThread{thread})
	default:
		return nil
	}
}

func inlineImageRenderKey(file, source string, columns, rows int) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d", source, file, columns, rows)
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
	cached := api.Attachment{URL: source}.CachePath(m.cfg.ImageCacheDir)
	if fileExists(cached) {
		return cached, true
	}
	return "", false
}

func kittyImage(file, source string, columns, rows int) (string, error) {
	full, _, err := kittyImageFrame(file, source, columns, rows)
	return full, err
}

func kittyImageFrame(file, source string, columns, rows int) (string, string, error) {
	imageID := kittyImageID(source, columns, rows)
	img, err := decodeImage(file)
	if err != nil {
		return "", "", err
	}
	img = downscaleForCells(img, columns, rows)
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
		return "", "", err
	}
	placeholder := kittyPlaceholderGrid(imageID, columns, rows)
	buf.WriteString(placeholder)
	return buf.String(), placeholder, nil
}

// downscaleForCells shrinks src so it fits inside the Kitty cell grid at
// roughly one bitmap pixel per terminal subcell. Aspect ratio is preserved
// (Kitty stretches into the requested Columns/Rows regardless, so we keep
// the source aspect to avoid double-distortion). If the source is already
// at or below the budget we return it unchanged.
func downscaleForCells(src image.Image, columns, rows int) image.Image {
	if columns <= 0 || rows <= 0 {
		return src
	}
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return src
	}
	targetW := columns * kittyCellPxW
	targetH := rows * kittyCellPxH
	scaleW := float64(targetW) / float64(srcW)
	scaleH := float64(targetH) / float64(srcH)
	scale := scaleW
	if scaleH < scale {
		scale = scaleH
	}
	if scale >= 1.0 {
		return src
	}
	dstW := int(float64(srcW) * scale)
	dstH := int(float64(srcH) * scale)
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)
	return dst
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

func fileExists(file string) bool {
	stat, err := os.Stat(file)
	return err == nil && stat.Mode().IsRegular()
}
