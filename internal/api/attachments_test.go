package api

import "testing"

func TestImageAttachmentsFromText(t *testing.T) {
	text := "see https://example.com/a/cat.png?token=1 and https://example.com/photo.jpeg). also https://example.com/page"

	attachments := ImageAttachmentsFromText(text)
	if len(attachments) != 2 {
		t.Fatalf("expected 2 image attachments, got %#v", attachments)
	}
	if attachments[0].URL != "https://example.com/a/cat.png?token=1" || attachments[0].ContentType != "image/png" {
		t.Fatalf("unexpected first attachment: %#v", attachments[0])
	}
	if attachments[1].URL != "https://example.com/photo.jpeg" || attachments[1].ContentType != "image/jpeg" {
		t.Fatalf("unexpected second attachment: %#v", attachments[1])
	}
}

func TestImageAttachmentsFromHTML(t *testing.T) {
	markup := `<html><body>
		<img alt="hero" src="https://cdn.example.com/render?id=123&amp;width=800">
		<img src="cid:logo">
		<img data-src="//cdn.example.com/lazy.webp">
		<source srcset="https://cdn.example.com/banner.jpg 1x, https://cdn.example.com/banner@2x.jpg 2x">
	</body></html>`

	attachments := ImageAttachmentsFromHTML(markup)
	if len(attachments) != 3 {
		t.Fatalf("expected 3 html image attachments, got %#v", attachments)
	}
	if attachments[0].URL != "https://cdn.example.com/render?id=123&width=800" {
		t.Fatalf("first html image URL was not decoded: %#v", attachments[0])
	}
	if attachments[0].ContentType != "image/unknown" || !attachments[0].IsImage() {
		t.Fatalf("expected extensionless HTML image to be previewable: %#v", attachments[0])
	}
	if attachments[1].URL != "https://cdn.example.com/lazy.webp" || attachments[1].ContentType != "image/webp" {
		t.Fatalf("unexpected protocol-relative lazy image: %#v", attachments[1])
	}
	if attachments[2].URL != "https://cdn.example.com/banner.jpg" || attachments[2].ContentType != "image/jpeg" {
		t.Fatalf("unexpected srcset image: %#v", attachments[2])
	}
}

func TestNormalizeAttachmentsKeepsImageWithoutPreviewSource(t *testing.T) {
	attachments := NormalizeAttachments([]Attachment{{
		ID:          "gmail-attachment",
		Name:        "chart.png",
		ContentType: "image/png",
	}})

	if len(attachments) != 1 {
		t.Fatalf("expected image attachment to be kept, got %#v", attachments)
	}
	if !attachments[0].IsImage() {
		t.Fatalf("expected normalized attachment to be an image: %#v", attachments[0])
	}
}

func TestPreviewSourceFallsBackToMediaResourceName(t *testing.T) {
	attachment := Attachment{
		ResourceName: "spaces/AAA/messages/BBB/attachments/CCC",
		Name:         "image.png",
		ContentType:  "image/png",
	}

	if got := attachment.PreviewSource(); got != attachment.ResourceName {
		t.Fatalf("PreviewSource() = %q, want %q", got, attachment.ResourceName)
	}
	if got := attachment.MediaResourceName(); got != attachment.ResourceName {
		t.Fatalf("MediaResourceName() = %q, want %q", got, attachment.ResourceName)
	}
}

func TestPreviewSourcePrefersMediaResourceOverBrowserURL(t *testing.T) {
	attachment := Attachment{
		ID:           "spaces/AAA/messages/BBB/attachments/CCC",
		ResourceName: "opaque-media-resource",
		Name:         "image.png",
		ContentType:  "image/png",
		DownloadURL:  "https://chat.google.com/api/get_attachment_url?url_type=DOWNLOAD_URL&content_type=image/png&attachment_token=token",
		ThumbnailURL: "https://chat.google.com/api/get_attachment_url?url_type=FIFE_URL&content_type=image/png&attachment_token=token",
	}

	if got := attachment.PreviewSource(); got != attachment.ResourceName {
		t.Fatalf("PreviewSource() = %q, want media resource %q", got, attachment.ResourceName)
	}
}
