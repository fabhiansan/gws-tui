package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var imageURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

func (a Attachment) DisplayName() string {
	if a.Name != "" {
		return a.Name
	}
	for _, raw := range []string{a.LocalPath, a.URL, a.DownloadURL, a.ThumbnailURL} {
		if name := sourceBase(raw); name != "" {
			return name
		}
	}
	if a.ContentType != "" {
		return a.ContentType
	}
	return "image"
}

func (a Attachment) PreviewSource() string {
	switch {
	case a.LocalPath != "":
		return a.LocalPath
	case a.ResourceName != "":
		return a.ResourceName
	case strings.Contains(a.ID, "/"):
		return a.ID
	case a.DownloadURL != "":
		return a.DownloadURL
	case a.ThumbnailURL != "":
		return a.ThumbnailURL
	default:
		return a.URL
	}
}

func (a Attachment) MediaResourceName() string {
	if a.ResourceName != "" {
		return a.ResourceName
	}
	if strings.Contains(a.ID, "/") {
		return a.ID
	}
	return ""
}

// CachePath returns the deterministic on-disk cache path for an attachment's
// image, shared by the daemon (writer) and TUI (reader) so both agree on
// where a cached file lives without coordination beyond the content itself.
func (a Attachment) CachePath(dir string) string {
	if dir == "" {
		return ""
	}
	a = normalizeAttachment(a)
	source := a.PreviewSource()
	if source == "" {
		source = a.MediaResourceName()
	}
	if source == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(source))
	ext := extFromContentType(a.ContentType)
	if ext == "" {
		ext = sourceExt(source)
	}
	if ext == "" {
		ext = ".png"
	}
	return filepath.Join(dir, hex.EncodeToString(sum[:])+ext)
}

func (a Attachment) IsImage() bool {
	if strings.HasPrefix(strings.ToLower(a.ContentType), "image/") {
		return true
	}
	for _, source := range []string{a.Name, a.LocalPath, a.URL, a.DownloadURL, a.ThumbnailURL} {
		if isImageSource(source) {
			return true
		}
	}
	return false
}

func ImageAttachmentsFromText(text string) []Attachment {
	matches := imageURLPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]bool{}
	attachments := make([]Attachment, 0, len(matches))
	for _, match := range matches {
		raw := trimURLPunctuation(match)
		if seen[raw] || !isHTTPURL(raw) || !isImageSource(raw) {
			continue
		}
		seen[raw] = true
		attachments = append(attachments, normalizeAttachment(Attachment{
			Name:        sourceBase(raw),
			ContentType: contentTypeFromSource(raw),
			URL:         raw,
		}))
	}
	return attachments
}

func NormalizeAttachments(attachments []Attachment) []Attachment {
	out := make([]Attachment, 0, len(attachments))
	seen := map[string]bool{}
	for _, attachment := range attachments {
		attachment = normalizeAttachment(attachment)
		if !attachment.IsImage() && attachment.PreviewSource() == "" {
			continue
		}
		key := attachment.ID + "|" + attachment.PreviewSource() + "|" + attachment.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, attachment)
	}
	return out
}

func MergeAttachments(groups ...[]Attachment) []Attachment {
	var merged []Attachment
	for _, group := range groups {
		merged = append(merged, group...)
	}
	return NormalizeAttachments(merged)
}

func normalizeAttachment(attachment Attachment) Attachment {
	if attachment.Name == "" {
		attachment.Name = sourceBase(attachment.PreviewSource())
	}
	if attachment.ContentType == "" {
		attachment.ContentType = contentTypeFromSource(attachment.PreviewSource())
	}
	return attachment
}

func isHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func isImageSource(raw string) bool {
	ext := strings.ToLower(sourceExt(raw))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg":
		return true
	default:
		return false
	}
}

func contentTypeFromSource(raw string) string {
	if parsed, err := url.Parse(raw); err == nil {
		if contentType := parsed.Query().Get("content_type"); strings.HasPrefix(strings.ToLower(contentType), "image/") {
			return strings.ToLower(contentType)
		}
	}
	switch strings.ToLower(sourceExt(raw)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	default:
		return ""
	}
}

func sourceExt(raw string) string {
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" {
		if ext := extFromContentType(parsed.Query().Get("content_type")); ext != "" {
			return ext
		}
		return path.Ext(parsed.Path)
	}
	return filepath.Ext(raw)
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

func sourceBase(raw string) string {
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" {
		name := path.Base(parsed.Path)
		if name == "." || name == "/" {
			return ""
		}
		return name
	}
	return filepath.Base(raw)
}

func trimURLPunctuation(raw string) string {
	return strings.TrimRight(raw, ".,;:!?)]}'\"")
}
