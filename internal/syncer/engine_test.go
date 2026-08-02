package syncer

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ratewatch/internal/config"
	"ratewatch/internal/connectors"
	"ratewatch/internal/security"
	"ratewatch/internal/store"
)

func TestRunTaskAggregatesAndWrites(t *testing.T) {
	var writes atomic.Int64
	var written float64
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "cheap"}}})
		case "/v1/sub2api/billing":
			_ = json.NewEncoder(w).Encode(map[string]any{"effective_rate_multiplier": 1.15})
		default:
			http.NotFound(w, r)
		}
	}))
	defer sourceServer.Close()
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/api/v1/admin/groups/g1" {
			var body map[string]float64
			_ = json.NewDecoder(r.Body).Decode(&body)
			written = body["rate_multiplier"]
			writes.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
			return
		}
		http.NotFound(w, r)
	}))
	defer targetServer.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	key := []byte("0123456789abcdef0123456789abcdef")
	vault, _ := security.NewVault(key)
	hash, _ := security.HashPassword("password-123")
	u, err := st.CreateUser("test@example.com", hash)
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := vault.Encrypt("admin-key")
	site, err := st.CreateSite(store.Site{UserID: u.ID, Name: "target", BaseURL: targetServer.URL, Platform: "sub2api", AdminSecret: admin, AdminHeader: "Authorization"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.ReplaceInventory(u.ID, site.ID, []store.Group{{ExternalID: "g1", Name: "vip", Rate: 1, Status: "active"}}, nil); err != nil {
		t.Fatal(err)
	}
	groups, _ := st.Inventory(u.ID, site.ID)
	secret, _ := vault.Encrypt("source-key")
	src, err := st.CreateSource(store.Source{UserID: u.ID, Name: "source", BaseURL: sourceServer.URL, Platform: "sub2api", Secret: secret, MonitorState: connectors.MonitorDirect, ProbeModel: "cheap", Models: []string{"cheap"}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := st.CreateTask(store.Task{UserID: u.ID, Name: "sync", SourceIDs: []int64{src.ID}, SiteID: site.ID, GroupID: groups[0].ID, Adjustment: .15, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{MasterKey: key, SessionSecret: key, PollInterval: time.Hour, ProbeInterval: 5 * time.Minute, ModelInterval: time.Hour, EmailInterval: time.Hour}
	engine := New(st, vault, connectors.New(), NewHub(), cfg)
	if err = engine.RunTask(context.Background(), task, true); err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 1 || written != 1.3 {
		t.Fatalf("writes=%d rate=%v", writes.Load(), written)
	}
	checkedSource, err := st.Source(u.ID, src.ID)
	if err != nil || checkedSource.LastError != "" {
		t.Fatalf("successful check must not populate last_error: source=%+v err=%v", checkedSource, err)
	}
	history, err := st.SourceHealth(u.ID, src.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) == 0 || history[0].State != "synced" || !strings.Contains(history[0].Message, "站点「target」的分组「vip」：1 → 1.3") {
		t.Fatalf("unexpected source health: %+v", history)
	}
}

func TestTargetRateRejectsNonPositiveAndNonFinite(t *testing.T) {
	for _, tc := range []struct{ upstream, adjustment float64 }{{1, -1}, {.5, -1}, {math.Inf(1), 0}, {math.NaN(), 0}} {
		if _, ok := targetRate(tc.upstream, 0, tc.adjustment); ok {
			t.Fatalf("accepted invalid result for %+v", tc)
		}
	}
	if value, ok := targetRate(1.2, 0, -.1); !ok || math.Abs(value-1.1) > 1e-9 {
		t.Fatalf("valid result=%v ok=%v", value, ok)
	}
	if value, ok := targetRate(1.15, 0, .15); !ok || value != 1.3 {
		t.Fatalf("precision result=%v ok=%v, want exactly 1.3", value, ok)
	}
	if value, ok := targetRate(.8, 1.1, .2); !ok || value != 1.3 {
		t.Fatalf("minimum result=%v ok=%v, want exactly 1.3", value, ok)
	}
}

func TestRunTaskSkipsDemoSource(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	key := []byte("0123456789abcdef0123456789abcdef")
	vault, _ := security.NewVault(key)
	hash, _ := security.HashPassword("password-123")
	user, err := st.CreateUser("demo-test@example.com", hash)
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := vault.Encrypt("admin-key")
	site, err := st.CreateSite(store.Site{UserID: user.ID, Name: "target", BaseURL: "https://target.invalid", Platform: "sub2api", AdminSecret: admin})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.ReplaceInventory(user.ID, site.ID, []store.Group{{ExternalID: "g1", Name: "vip", Rate: 1, Status: "active"}}, nil); err != nil {
		t.Fatal(err)
	}
	groups, _ := st.Inventory(user.ID, site.ID)
	source, err := st.CreateSource(store.Source{UserID: user.ID, Name: "演示上游", BaseURL: "https://source.demo.local", Platform: "newapi", Secret: "demo", MonitorState: connectors.MonitorNewAPIProbe})
	if err != nil {
		t.Fatal(err)
	}
	task, err := st.CreateTask(store.Task{UserID: user.ID, Name: "demo sync", SourceIDs: []int64{source.ID}, SiteID: site.ID, GroupID: groups[0].ID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{MasterKey: key, SessionSecret: key, PollInterval: time.Hour, ProbeInterval: time.Hour, ModelInterval: time.Hour, EmailInterval: time.Hour}
	engine := New(st, vault, connectors.New(), NewHub(), cfg)
	if err = engine.RunTask(context.Background(), task, true); err != nil {
		t.Fatal(err)
	}
	updated, err := st.Task(user.ID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastStatus != "skipped" || !strings.Contains(updated.LastError, store.DemoSourceMessage) {
		t.Fatalf("task status=%q error=%q", updated.LastStatus, updated.LastError)
	}
	history, err := st.SourceHealth(user.ID, source.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].State != "skipped" || history[0].Message != store.DemoSourceMessage {
		t.Fatalf("unexpected source health: %+v", history)
	}
}
