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
	InitialFeature     string
	NoIcons            bool
	NoColor            bool
	NotifyDesktop      bool
	NotifySound        bool
	NotifySoundFile    string
	InlineImages       bool
	VimMode            bool
	ConfigPath         string
	StatePath          string
	CachePath          string
	ImageCacheDir      string
	DraftDir           string
	LogPath            string
	AllowFixtureNotice bool
}

func LoadConfig() (Config, error) {
	configBase, cacheBase, err := gwsDirs()
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		InitialFeature:     "chat",
		NotifyDesktop:      true,
		NotifySound:        true,
		NotifySoundFile:    "/System/Library/Sounds/Glass.aiff",
		InlineImages:       true,
		VimMode:            true,
		ConfigPath:         filepath.Join(configBase, "tui.toml"),
		StatePath:          filepath.Join(configBase, "tui-state.json"),
		CachePath:          filepath.Join(cacheBase, "tui-cache.json"),
		ImageCacheDir:      filepath.Join(cacheBase, "images"),
		DraftDir:           filepath.Join(cacheBase, "drafts"),
		LogPath:            filepath.Join(cacheBase, "tui.log"),
		AllowFixtureNotice: true,
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
		case "no_icons":
			cfg.NoIcons = parseBool(value)
		case "no_color":
			cfg.NoColor = parseBool(value)
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
			cfg.StatePath = expandHome(value)
		case "cache_path":
			cfg.CachePath = expandHome(value)
		case "image_cache_dir":
			cfg.ImageCacheDir = expandHome(value)
		case "draft_dir":
			cfg.DraftDir = expandHome(value)
		case "log_path":
			cfg.LogPath = expandHome(value)
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

func parseBool(value string) bool {
	parsed, err := strconv.ParseBool(strings.ToLower(value))
	if err != nil {
		return false
	}
	return parsed
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
