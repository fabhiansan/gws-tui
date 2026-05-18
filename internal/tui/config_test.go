package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistedStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := persistedState{
		LastFeature: "mail",
		LastSpace:   "spaces/engineering",
		Selections:  map[string]int{"chat": 2, "mail": 1},
	}
	if err := savePersistedState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded := loadPersistedState(path)
	if loaded.LastFeature != state.LastFeature || loaded.LastSpace != state.LastSpace || loaded.Selections["chat"] != 2 {
		t.Fatalf("unexpected state: %#v", loaded)
	}
}

func TestLoadConfigTOMLSubset(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "runtime"))
	configPath := filepath.Join(root, "gws", "tui.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("initial_feature = \"meet\"\nno_icons = true\nnotify_sound = false\ninline_images = false\ndaemon = true\ndaemon_autospawn = false\ndaemon_socket = \"$XDG_RUNTIME_DIR/gws/custom.sock\"\ncache_path = \"~/custom-gws-cache.json\"\nimage_cache_dir = \"~/custom-gws-images\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InitialFeature != "meet" || !cfg.NoIcons || cfg.NotifySound || cfg.InlineImages {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if filepath.Base(cfg.CachePath) != "custom-gws-cache.json" {
		t.Fatalf("unexpected cache path: %q", cfg.CachePath)
	}
	if filepath.Base(cfg.ImageCacheDir) != "custom-gws-images" {
		t.Fatalf("unexpected image cache dir: %q", cfg.ImageCacheDir)
	}
	if !cfg.Daemon || cfg.DaemonAutospawn {
		t.Fatalf("unexpected daemon flags: daemon=%v autospawn=%v", cfg.Daemon, cfg.DaemonAutospawn)
	}
	if filepath.Base(cfg.DaemonSocket) != "custom.sock" {
		t.Fatalf("unexpected daemon socket: %q", cfg.DaemonSocket)
	}
}
