package tui

import (
	"errors"
	"os/exec"
	"runtime"
)

func openURL(url string) error {
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
