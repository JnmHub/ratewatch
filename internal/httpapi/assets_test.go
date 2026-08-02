package httpapi

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestEmbeddedFrontend(t *testing.T) {
	previous, configured := os.LookupEnv("RATEWATCH_WEB_DIR")
	if err := os.Unsetenv("RATEWATCH_WEB_DIR"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if configured {
			_ = os.Setenv("RATEWATCH_WEB_DIR", previous)
		} else {
			_ = os.Unsetenv("RATEWATCH_WEB_DIR")
		}
	})

	server := &Server{}
	for _, route := range []string{"/", "/about"} {
		response := httptest.NewRecorder()
		server.frontend(response, httptest.NewRequest(http.MethodGet, route, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s returned %d", route, response.Code)
		}
		if !strings.Contains(response.Body.String(), `<div id="app"></div>`) {
			t.Fatalf("GET %s did not return the SPA entry", route)
		}
	}

	scripts, err := fs.Glob(embeddedFrontendFS, "assets/*.js")
	if err != nil || len(scripts) == 0 {
		t.Fatalf("embedded JavaScript asset missing: %v", err)
	}
	response := httptest.NewRecorder()
	server.frontend(response, httptest.NewRequest(http.MethodGet, "/"+scripts[0], nil))
	if response.Code != http.StatusOK || response.Body.Len() == 0 {
		t.Fatalf("embedded asset returned status=%d bytes=%d", response.Code, response.Body.Len())
	}
}
