package api

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type failingDownloadClient struct {
	*FixtureClient
}

func (c failingDownloadClient) DownloadAttachment(context.Context, Attachment, string) error {
	return errors.New("primary media download failed")
}

func TestHybridDownloadAttachmentDoesNotFallbackToFixture(t *testing.T) {
	client := &HybridClient{
		primary:  failingDownloadClient{FixtureClient: NewFixtureClient()},
		fallback: NewFixtureClient(),
	}

	err := client.DownloadAttachment(context.Background(), Attachment{ResourceName: "spaces/a/messages/b/attachments/c"}, "out.png")
	if err == nil {
		t.Fatal("expected primary download error")
	}
	if !strings.Contains(err.Error(), "primary media download failed") {
		t.Fatalf("expected primary error, got %v", err)
	}
	if strings.Contains(err.Error(), "fixture mode") {
		t.Fatalf("download should not fallback to fixture: %v", err)
	}
}
