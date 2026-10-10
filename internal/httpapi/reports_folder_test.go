package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWindowsPathOfAWSLPath(t *testing.T) {
	got := windowsPath("/home/tester/eebuster/data/reports", "Ubuntu")
	if got != `\\wsl.localhost\Ubuntu\home\tester\eebuster\data\reports` {
		t.Errorf("windowsPath = %q", got)
	}
	if windowsPath("/home/tester", "") != "" {
		t.Error("outside WSL there is no Windows path")
	}
}

func TestFileManagerCommand(t *testing.T) {
	found := func(name string) (string, error) {
		if strings.Contains(name, "explorer") || name == "xdg-open" {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	cases := []struct {
		goos, distro, file string
		wantName, wantArg  string
	}{
		{"windows", "", "", "explorer.exe", "/data/reports"},
		{"linux", "Ubuntu", "", "/usr/bin/explorer.exe", `\\wsl.localhost\Ubuntu\data\reports`},
		{"linux", "Ubuntu", "/data/reports/r1.html", "/usr/bin/explorer.exe", `/select,\\wsl.localhost\Ubuntu\data\reports\r1.html`},
		{"darwin", "", "/data/reports/r1.html", "open", "-R"},
		{"linux", "", "", "/usr/bin/xdg-open", "/data/reports"},
	}
	for _, c := range cases {
		name, args := fileManagerCommand(c.goos, c.distro, "/data/reports", c.file, found)
		if name != c.wantName || len(args) == 0 || args[0] != c.wantArg {
			t.Errorf("%s/%s: %s %v, want %s %s", c.goos, c.distro, name, args, c.wantName, c.wantArg)
		}
	}
	if name, _ := fileManagerCommand("linux", "", "/data", "", func(string) (string, error) { return "", errors.New("none") }); name != "" {
		t.Errorf("without xdg-open the command is %q, want none", name)
	}
}

func TestSameOriginRefusesForeignPages(t *testing.T) {
	cases := []struct {
		site, origin string
		want         bool
	}{
		{"", "", true}, // curl and other tools
		{"same-origin", "http://localhost:8080", true}, // the dashboard
		{"none", "", true}, // typed into the address bar
		{"cross-site", "https://evil.example", false}, // another website
		{"", "http://localhost:8080", true},           // an old browser, same host
		{"", "http://attacker.example", false},        // an old browser, other host
		{"same-site", "http://other.localhost:8080", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/v1/reports/open", nil)
		if c.site != "" {
			r.Header.Set("Sec-Fetch-Site", c.site)
		}
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := sameOrigin(r); got != c.want {
			t.Errorf("site %q origin %q: got %v, want %v", c.site, c.origin, got, c.want)
		}
	}
}
