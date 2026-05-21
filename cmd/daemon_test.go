package cmd

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fabhiansan/gws-tui/internal/api"
	"github.com/fabhiansan/gws-tui/internal/tui"
)

func TestConnectDaemonClientSkipsColdSnapshot(t *testing.T) {
	socketPath := filepath.Join(shortSocketDir(t), "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	snapshotRequested := make(chan struct{}, 1)
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		for {
			env, err := api.ReadFrame(conn)
			if err != nil {
				serverDone <- nil
				return
			}
			switch env.Method {
			case "ClientHello":
				if err := api.WriteFrame(conn, api.Envelope{ID: env.ID, Kind: "response"}); err != nil {
					serverDone <- err
					return
				}
			case "DaemonStatus":
				result, err := api.MarshalRaw(api.DaemonStatus{
					ProtocolVersion: api.ProtocolVersion,
					PID:             1234,
					SocketPath:      socketPath,
					SnapshotLoaded:  false,
					SnapshotHasData: false,
				})
				if err != nil {
					serverDone <- err
					return
				}
				if err := api.WriteFrame(conn, api.Envelope{ID: env.ID, Kind: "response", Result: result}); err != nil {
					serverDone <- err
					return
				}
			case "Snapshot":
				snapshotRequested <- struct{}{}
				if err := api.WriteFrame(conn, api.Envelope{ID: env.ID, Kind: "response", Error: &api.ProtocolError{Message: "snapshot should not be requested"}}); err != nil {
					serverDone <- err
					return
				}
			default:
				if err := api.WriteFrame(conn, api.Envelope{ID: env.ID, Kind: "response"}); err != nil {
					serverDone <- err
					return
				}
			}
		}
	}()

	start := time.Now()
	client, snapshot, err := connectDaemonClient(tui.Config{
		DaemonSocket:    socketPath,
		DaemonAutospawn: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("connect should not wait for cold snapshot, took %s", elapsed)
	}
	if snapshot == nil {
		t.Fatal("expected empty startup snapshot")
	}
	if snapshot.HasData() {
		t.Fatalf("cold daemon should return an empty snapshot, got %#v", snapshot)
	}
	select {
	case <-snapshotRequested:
		t.Fatal("connect requested a cold Snapshot")
	default:
	}

	_ = client.Close()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fake daemon did not close")
	}
}

func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "gwscmd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
