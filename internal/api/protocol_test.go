package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestProtocolFrameRoundTrip(t *testing.T) {
	params, err := MarshalRaw(ChatMessagesParams{SpaceName: "spaces/engineering", PageToken: "older"})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	want := Envelope{ID: 42, Kind: "request", Method: "ChatMessages", Params: params}
	if err := WriteFrame(&buf, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Kind != want.Kind || got.Method != want.Method {
		t.Fatalf("unexpected envelope: %#v", got)
	}
	var decoded ChatMessagesParams
	if err := json.Unmarshal(got.Params, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SpaceName != "spaces/engineering" || decoded.PageToken != "older" {
		t.Fatalf("unexpected params: %#v", decoded)
	}
}

func TestWorkspaceSnapshotLockPreventsConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	lock, err := LockWorkspaceSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := TryLockWorkspaceSnapshot(path); !errors.Is(err, ErrSnapshotLockBusy) {
		t.Fatalf("expected busy lock, got %v", err)
	}
}
