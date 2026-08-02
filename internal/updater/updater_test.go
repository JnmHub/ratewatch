package updater

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

func TestVersionComparison(t *testing.T) {
	tests := []struct {
		candidate string
		current   string
		want      bool
	}{
		{"v1.2.0", "v1.1.9", true},
		{"v2.0.0", "v1.99.99", true},
		{"v1.2.0", "v1.2.0", false},
		{"v1.1.9", "v1.2.0", false},
		{"latest", "v1.2.0", false},
	}
	for _, test := range tests {
		if got := newer(test.candidate, test.current); got != test.want {
			t.Fatalf("newer(%q, %q)=%v want %v", test.candidate, test.current, got, test.want)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	data := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  ratewatch-linux-amd64\n")
	if got := checksumFor(data, "ratewatch-linux-amd64"); got != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected checksum %q", got)
	}
	if got := checksumFor(data, "ratewatch-windows-amd64.exe"); got != "" {
		t.Fatalf("unexpected checksum for missing asset %q", got)
	}
}

func TestCheckLatestRelease(t *testing.T) {
	oldVersion, oldCommit, oldBuildTime := Version, Commit, BuildTime
	Version, Commit, BuildTime = "v1.0.0", "abc123", "2026-08-02T00:00:00Z"
	t.Cleanup(func() { Version, Commit, BuildTime = oldVersion, oldCommit, oldBuildTime })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assetName := binaryAssetName(runtime.GOOS, runtime.GOARCH)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1.1.0", "html_url": "https://github.com/JnmHub/ratewatch/releases/tag/v1.1.0", "published_at": time.Now().UTC(),
			"assets": []map[string]string{{"name": assetName, "browser_download_url": serverURL(r) + "/binary"}, {"name": "checksums.txt", "browser_download_url": serverURL(r) + "/checksums.txt"}},
		})
	}))
	defer server.Close()

	manager := New()
	manager.APIURL = server.URL
	manager.Repository = "JnmHub/ratewatch"
	status, err := manager.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.UpdateAvailable || status.LatestVersion != "v1.1.0" || status.CurrentVersion != "v1.0.0" || status.Commit != "abc123" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
