package httpapi

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// The reports folder in the file manager of the machine the testbench runs on. A web page
// cannot open a folder itself, so the dashboard asks the testbench to. The window opens on
// that machine's desktop, which is why only a browser on the same machine may ask.

// wslDistro names the WSL distribution the testbench runs in, or is empty outside WSL.
func wslDistro() string {
	return os.Getenv("WSL_DISTRO_NAME")
}

// windowsPath is how Windows reaches a path of a WSL distribution: \\wsl.localhost\<distro>\...
func windowsPath(path, distro string) string {
	out := ""
	if path != "" && distro != "" {
		out = `\\wsl.localhost\` + distro + strings.ReplaceAll(path, "/", `\`)
	}
	return out
}

// absReportsDir is the reports folder as an absolute path.
func (s *Server) absReportsDir() string {
	dir := s.runs.ReportsDir()
	if abs, err := filepath.Abs(dir); err == nil && dir != "" {
		dir = abs
	}
	return dir
}

// fileManagerCommand is the command that shows dir, or file selected inside it, for the
// operating system the testbench runs on.
func fileManagerCommand(goos, distro, dir, file string, lookPath func(string) (string, error)) (string, []string) {
	name, args := "", []string(nil)
	explorer := func(dir, file string) []string {
		out := []string{dir}
		if file != "" {
			out = []string{"/select," + file}
		}
		return out
	}
	switch {
	case goos == "windows":
		name, args = "explorer.exe", explorer(dir, file)
	case goos == "darwin":
		name, args = "open", []string{dir}
		if file != "" {
			args = []string{"-R", file}
		}
	case distro != "":
		if path, err := lookPath("explorer.exe"); err == nil {
			name = path
		} else if path, err := lookPath("/mnt/c/Windows/explorer.exe"); err == nil {
			name = path
		}
		args = explorer(windowsPath(dir, distro), windowsPath(file, distro))
	default:
		if path, err := lookPath("xdg-open"); err == nil {
			name, args = path, []string{dir}
		}
	}
	return name, args
}

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}

// handleReportsOpen opens the reports folder; ?run=<id> selects that run's report in it.
func (s *Server) handleReportsOpen(w http.ResponseWriter, r *http.Request) {
	dir := s.absReportsDir()
	file := ""
	if id := r.URL.Query().Get("run"); id != "" {
		file = s.runs.ReportFile(id)
		if abs, err := filepath.Abs(file); err == nil && file != "" {
			file = abs
		}
	}
	switch {
	case dir == "":
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no_reports_dir", "detail": "this instance keeps runs in memory only"})
	case !isLoopback(r):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not_local",
			"detail": fmt.Sprintf("the folder opens on the machine the testbench runs on; open it from a browser on that machine (request from %s)", r.RemoteAddr)})
	case !sameOrigin(r):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross_site", "detail": "only the testbench's own pages may open the folder"})
	default:
		_ = os.MkdirAll(dir, 0o755)
		if err := openInFileManager(dir, file); err != nil {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "no_file_manager", "detail": err.Error()})
		} else {
			writeJSON(w, http.StatusOK, map[string]string{"opened": dir, "selected": file, "windows_path": windowsPath(dir, wslDistro())})
		}
	}
}

// sameOrigin refuses a request another website made the browser send: a foreign page must
// not open windows on the tester's desktop. Tools without a browser send neither header.
func sameOrigin(r *http.Request) bool {
	site, origin := r.Header.Get("Sec-Fetch-Site"), r.Header.Get("Origin")
	ok := site == "same-origin" || site == "none" || (site == "" && origin == "")
	if !ok && origin != "" {
		if u, err := url.Parse(origin); err == nil {
			ok = strings.EqualFold(u.Host, r.Host)
		}
	}
	return ok
}

func openInFileManager(dir, file string) error {
	name, args := fileManagerCommand(runtime.GOOS, wslDistro(), dir, file, exec.LookPath)
	var err error
	if name == "" {
		err = errors.New("no file manager is known on this system")
	} else {
		cmd := exec.Command(name, args...)
		if err = cmd.Start(); err == nil {
			// Explorer exits with status 1 even when it opened the window; nothing to report.
			go func() { _ = cmd.Wait() }()
		}
	}
	return err
}
