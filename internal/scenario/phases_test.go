package scenario

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSKI = "0000000000000000000000000000000000000001"

func opevServer(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/opev/"+testSKI {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestOpevPhasesSkipsCombinedPhaseDevice(t *testing.T) {
	srv := opevServer(http.StatusOK, `{"phases":["abc"]}`)
	defer srv.Close()
	rn := NewRunner(srv.URL)
	missing := rn.missingRequirements(requirementsSpec{OpevPhases: []string{"a", "b", "c"}},
		map[string]any{"peer": map[string]any{"ski": testSKI}})
	if len(missing) != 3 || !strings.Contains(missing[0], "abc") {
		t.Fatalf("missing = %v, want one entry per absent phase naming abc", missing)
	}
}

func TestOpevPhasesPassesPerPhaseDevice(t *testing.T) {
	srv := opevServer(http.StatusOK, `{"phases":["a","b","c"]}`)
	defer srv.Close()
	rn := NewRunner(srv.URL)
	if missing := rn.missingRequirements(requirementsSpec{OpevPhases: []string{"a", "b", "c"}},
		map[string]any{"peer": map[string]any{"ski": testSKI}}); len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
}

func TestOpevPhasesUnknownDoesNotSkip(t *testing.T) {
	// No vehicle: the scenario must run and fail on its own rather than skip.
	for _, srv := range []*httptest.Server{
		opevServer(http.StatusNotFound, `{"error":"not_found"}`),
		opevServer(http.StatusOK, `{"phases":[]}`),
	} {
		rn := NewRunner(srv.URL)
		if missing := rn.missingRequirements(requirementsSpec{OpevPhases: []string{"a"}},
			map[string]any{"peer": map[string]any{"ski": testSKI}}); len(missing) != 0 {
			t.Errorf("missing = %v, want none", missing)
		}
		srv.Close()
	}
}
