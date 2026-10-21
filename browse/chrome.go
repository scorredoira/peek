package browse

import (
	"fmt"
	"os/exec"
	"runtime"
)

// FindChromePath is where this machine keeps Chrome.
func FindChromePath() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", nil
	case "linux":
		path := findInPath("google-chrome", "google-chrome-stable", "chromium", "chromium-browser")
		if path == "" {
			return "", fmt.Errorf("Chrome not found. Install Google Chrome or Chromium")
		}
		return path, nil
	case "windows":
		path := findInPath(
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		)
		if path == "" {
			path = findInPath("chrome")
		}
		if path == "" {
			return "", fmt.Errorf("Chrome not found. Install Google Chrome")
		}
		return path, nil
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func findInPath(names ...string) string {
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err == nil {
			return path
		}
	}
	return ""
}
