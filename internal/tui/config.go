package tui

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	InitialFeature         string
	Theme                  string
	NoIcons                bool
	NoColor                bool
	Daemon                 bool
	DaemonSocket           string
	DaemonAutospawn        bool
	DaemonLog              string
	DaemonPIDFile          string
	DaemonAutoSubscribe    bool
	DaemonAutoSubscribeMax int
	NotifyDesktop          bool
	NotifySound            bool
	NotifySoundFile        string
	InlineImages           bool
	VimMode                bool
	ConfigPath             string
	StatePath              string
	CachePath              string
	ImageCacheDir          string
	DraftDir               string
	LogPath                string
}

func LoadConfig() (Config, error) {
	configBase, cacheBase, err := gwsDirs()
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		InitialFeature:         "chat",
		Theme:                  "catppuccin",
		DaemonSocket:           defaultDaemonSocket(cacheBase),
		DaemonAutospawn:        true,
		DaemonLog:              filepath.Join(cacheBase, "daemon.log"),
		DaemonPIDFile:          defaultDaemonPIDFile(cacheBase),
		DaemonAutoSubscribe:    true,
		DaemonAutoSubscribeMax: 20,
		NotifyDesktop:          true,
		NotifySound:            true,
		NotifySoundFile:        "/System/Library/Sounds/Glass.aiff",
		InlineImages:           true,
		VimMode:                true,
		ConfigPath:             filepath.Join(configBase, "tui.toml"),
		StatePath:              filepath.Join(configBase, "tui-state.json"),
		CachePath:              filepath.Join(cacheBase, "tui-cache.json"),
		ImageCacheDir:          filepath.Join(cacheBase, "images"),
		DraftDir:               filepath.Join(cacheBase, "drafts"),
		LogPath:                filepath.Join(cacheBase, "tui.log"),
	}

	file, err := os.Open(cfg.ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch key {
		case "initial_feature", "feature":
			cfg.InitialFeature = normalizeFeature(value)
		case "theme":
			cfg.Theme = strings.ToLower(strings.TrimSpace(value))
		case "no_icons":
			cfg.NoIcons = parseBool(value)
		case "no_color":
			cfg.NoColor = parseBool(value)
		case "daemon":
			cfg.Daemon = parseBool(value)
		case "daemon_socket":
			cfg.DaemonSocket = expandPath(value)
		case "daemon_autospawn":
			cfg.DaemonAutospawn = parseBool(value)
		case "daemon_log":
			cfg.DaemonLog = expandPath(value)
		case "daemon_pid_file":
			cfg.DaemonPIDFile = expandPath(value)
		case "daemon_auto_subscribe":
			cfg.DaemonAutoSubscribe = parseBool(value)
		case "daemon_auto_subscribe_max":
			if n, err := strconv.Atoi(value); err == nil && n >= 0 {
				cfg.DaemonAutoSubscribeMax = n
			}
		case "notify_desktop":
			cfg.NotifyDesktop = parseBool(value)
		case "notify_sound":
			cfg.NotifySound = parseBool(value)
		case "notify_sound_file":
			cfg.NotifySoundFile = value
		case "inline_images":
			cfg.InlineImages = parseBool(value)
		case "vim_mode", "vim":
			cfg.VimMode = parseBool(value)
		case "state_path":
			cfg.StatePath = expandPath(value)
		case "cache_path":
			cfg.CachePath = expandPath(value)
		case "image_cache_dir":
			cfg.ImageCacheDir = expandPath(value)
		case "draft_dir":
			cfg.DraftDir = expandPath(value)
		case "log_path":
			cfg.LogPath = expandPath(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func gwsDirs() (configBase, cacheBase string, err error) {
	if explicit := os.Getenv("GOOGLE_WORKSPACE_CLI_CONFIG_DIR"); explicit != "" {
		configBase = expandHome(explicit)
	} else if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		configBase = filepath.Join(xdg, "gws")
	} else {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return "", "", homeErr
		}
		configBase = filepath.Join(home, ".config", "gws")
	}

	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		cacheBase = filepath.Join(xdg, "gws")
	} else {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return "", "", homeErr
		}
		cacheBase = filepath.Join(home, ".cache", "gws")
	}
	return configBase, cacheBase, nil
}

func defaultDaemonSocket(cacheBase string) string {
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, "gws", "daemon.sock")
	}
	return filepath.Join(cacheBase, "daemon.sock")
}

func defaultDaemonPIDFile(cacheBase string) string {
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, "gws", "daemon.pid")
	}
	return filepath.Join(cacheBase, "daemon.pid")
}

func parseBool(value string) bool {
	parsed, err := strconv.ParseBool(strings.ToLower(value))
	if err != nil {
		return false
	}
	return parsed
}

func expandPath(value string) string {
	return expandHome(os.Expand(value, func(key string) string {
		if out, ok := os.LookupEnv(key); ok {
			return out
		}
		return "$" + key
	}))
}

func expandHome(value string) string {
	if value == "" || !strings.HasPrefix(value, "~/") {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return value
	}
	return filepath.Join(home, strings.TrimPrefix(value, "~/"))
}

type persistedState struct {
	LastFeature string         `json:"last_feature"`
	LastSpace   string         `json:"last_space,omitempty"`
	Selections  map[string]int `json:"selections,omitempty"`
}

func loadPersistedState(path string) persistedState {
	payload, err := os.ReadFile(path)
	if err != nil {
		return persistedState{Selections: map[string]int{}}
	}
	var state persistedState
	if json.Unmarshal(payload, &state) != nil {
		return persistedState{Selections: map[string]int{}}
	}
	if state.Selections == nil {
		state.Selections = map[string]int{}
	}
	return state
}

func savePersistedState(path string, state persistedState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(payload, '\n'), 0o600)
}
