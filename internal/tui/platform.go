package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

var openURL = defaultOpenURL

func defaultOpenURL(url string) error {
	if url == "" {
		return errors.New("empty URL")
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return errors.New("open URL is not supported on this platform")
	}
}

func copyText(value string) error {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("pbcopy")
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		if _, err := stdin.Write([]byte(value)); err != nil {
			return err
		}
		if err := stdin.Close(); err != nil {
			return err
		}
		return cmd.Wait()
	case "linux":
		for _, name := range []string{"wl-copy", "xclip"} {
			if _, err := exec.LookPath(name); err == nil {
				cmd := exec.Command(name)
				stdin, err := cmd.StdinPipe()
				if err != nil {
					return err
				}
				if err := cmd.Start(); err != nil {
					return err
				}
				if _, err := stdin.Write([]byte(value)); err != nil {
					return err
				}
				if err := stdin.Close(); err != nil {
					return err
				}
				return cmd.Wait()
			}
		}
		return errors.New("no clipboard command found")
	case "windows":
		cmd := exec.Command("clip")
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		if _, err := stdin.Write([]byte(value)); err != nil {
			return err
		}
		if err := stdin.Close(); err != nil {
			return err
		}
		return cmd.Wait()
	default:
		return errors.New("clipboard is not supported on this platform")
	}
}

// pasteImageTo reads an image from the system clipboard and writes it to dst.
// Returns the MIME type written on success (always "image/png" today).
// Returns an error if the clipboard does not contain an image or no supported
// clipboard tool is available.
func pasteImageTo(dst string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		if path, err := exec.LookPath("pngpaste"); err == nil {
			cmd := exec.Command(path, dst)
			if err := cmd.Run(); err != nil {
				return "", fmt.Errorf("pngpaste: %w", err)
			}
			if info, statErr := os.Stat(dst); statErr != nil || info.Size() == 0 {
				return "", errors.New("clipboard has no image")
			}
			return "image/png", nil
		}
		// osascript fallback. Uses «class PNGf» so the system converts other
		// image flavors (TIFF, etc.) into PNG before writing.
		script := fmt.Sprintf(`try
	set the_data to (the clipboard as «class PNGf»)
	set fp to open for access POSIX file %q with write permission
	set eof of fp to 0
	write the_data to fp
	close access fp
on error
	try
		close access fp
	end try
	error number -128
end try`, dst)
		if err := exec.Command("osascript", "-e", script).Run(); err != nil {
			_ = os.Remove(dst)
			return "", errors.New("clipboard has no image")
		}
		if info, statErr := os.Stat(dst); statErr != nil || info.Size() == 0 {
			return "", errors.New("clipboard has no image")
		}
		return "image/png", nil
	case "linux":
		for _, name := range []string{"wl-paste", "xclip"} {
			path, err := exec.LookPath(name)
			if err != nil {
				continue
			}
			var cmd *exec.Cmd
			if strings.HasSuffix(path, "xclip") {
				cmd = exec.Command(path, "-selection", "clipboard", "-t", "image/png", "-o")
			} else {
				cmd = exec.Command(path, "--type", "image/png")
			}
			out, err := cmd.Output()
			if err != nil || len(out) == 0 {
				continue
			}
			if err := os.WriteFile(dst, out, 0o600); err != nil {
				return "", err
			}
			return "image/png", nil
		}
		return "", errors.New("no clipboard image tool found (install wl-paste or xclip)")
	default:
		return "", errors.New("clipboard image is not supported on this platform")
	}
}

func pasteText() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("pbpaste").Output()
		if err != nil {
			return "", err
		}
		return string(out), nil
	case "linux":
		for _, name := range []string{"wl-paste", "xclip"} {
			path, err := exec.LookPath(name)
			if err != nil {
				continue
			}
			var cmd *exec.Cmd
			if strings.HasSuffix(path, "xclip") {
				cmd = exec.Command(path, "-selection", "clipboard", "-o")
			} else {
				cmd = exec.Command(path)
			}
			out, err := cmd.Output()
			if err != nil {
				return "", err
			}
			return string(out), nil
		}
		return "", errors.New("no clipboard command found")
	case "windows":
		out, err := exec.Command("powershell", "-NoProfile", "-Command", "Get-Clipboard").Output()
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(out), "\r\n"), nil
	default:
		return "", errors.New("clipboard is not supported on this platform")
	}
}
