package notify

import (
	"os/exec"
	"runtime"
)

type Options struct {
	Desktop   bool
	Sound     bool
	SoundFile string
}

func Send(title, message string, opts Options) {
	if opts.Desktop {
		desktop(title, message)
	}
	if opts.Sound {
		sound(opts.SoundFile)
	}
}

func desktop(title, message string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("osascript", "-e", `display notification `+quote(message)+` with title `+quote(title)).Start()
	case "linux":
		_ = exec.Command("notify-send", title, message).Start()
	}
}

func sound(file string) {
	switch runtime.GOOS {
	case "darwin":
		if file != "" {
			_ = exec.Command("afplay", file).Start()
		}
	case "linux":
		_ = exec.Command("paplay", "/usr/share/sounds/freedesktop/stereo/message.oga").Start()
	default:
		print("\a")
	}
}

func quote(value string) string {
	escaped := ""
	for _, r := range value {
		if r == '"' || r == '\\' {
			escaped += "\\"
		}
		escaped += string(r)
	}
	return `"` + escaped + `"`
}
