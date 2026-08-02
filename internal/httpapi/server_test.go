package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"ratewatch/internal/config"
	"ratewatch/internal/connectors"
	"ratewatch/internal/security"
	"ratewatch/internal/store"
	"ratewatch/internal/syncer"
)

func TestRegisterAndTenantDashboard(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	cfg := config.Config{MasterKey: key, SessionSecret: key, PollInterval: time.Hour, ProbeInterval: time.Hour, ModelInterval: time.Hour, EmailInterval: time.Hour}
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	vault, _ := security.NewVault(key)
	client := connectors.New()
	hub := syncer.NewHub()
	engine := syncer.New(st, vault, client, hub, cfg)
	srv := httptest.NewServer(New(cfg, st, vault, client, engine, hub).Handler())
	defer srv.Close()
	payload, _ := json.Marshal(map[string]string{"email": "tenant@example.com", "password": "password-123"})
	resp, err := http.Post(srv.URL+"/api/auth/register", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var session struct {
		Token string `json:"token"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&session); err != nil || session.Token == "" || resp.StatusCode != 200 {
		t.Fatalf("register status=%d token=%q err=%v", resp.StatusCode, session.Token, err)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/dashboard", nil)
	req.Header.Set("Authorization", "Bearer "+session.Token)
	dashResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer dashResp.Body.Close()
	var dash map[string]int64
	_ = json.NewDecoder(dashResp.Body).Decode(&dash)
	if dashResp.StatusCode != 200 || dash["sites"] != 0 || dash["tasks"] != 0 {
		t.Fatalf("dashboard status=%d data=%v", dashResp.StatusCode, dash)
	}
}

func TestDetectSourceAcceptsCompleteFrontendPayload(t *testing.T) {
	upstreamMux := http.NewServeMux()
	upstreamMux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "cheap-model"}}})
	})
	upstreamMux.HandleFunc("GET /v1/sub2api/billing", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"effective_rate_multiplier": 0.8}})
	})
	upstream := httptest.NewServer(upstreamMux)
	defer upstream.Close()

	key := []byte("0123456789abcdef0123456789abcdef")
	cfg := config.Config{MasterKey: key, SessionSecret: key, PollInterval: time.Hour, ProbeInterval: time.Hour, ModelInterval: time.Hour, EmailInterval: time.Hour}
	st, err := store.Open(filepath.Join(t.TempDir(), "detect.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	vault, _ := security.NewVault(key)
	client := connectors.New()
	hub := syncer.NewHub()
	engine := syncer.New(st, vault, client, hub, cfg)
	srv := httptest.NewServer(New(cfg, st, vault, client, engine, hub).Handler())
	defer srv.Close()

	registerBody, _ := json.Marshal(map[string]string{"email": "detect@example.com", "password": "password-123"})
	registerResp, err := http.Post(srv.URL+"/api/auth/register", "application/json", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatal(err)
	}
	defer registerResp.Body.Close()
	var session struct {
		Token string `json:"token"`
	}
	if err = json.NewDecoder(registerResp.Body).Decode(&session); err != nil || registerResp.StatusCode != http.StatusOK {
		t.Fatalf("register status=%d err=%v", registerResp.StatusCode, err)
	}

	payload, _ := json.Marshal(map[string]any{
		"name":                  "test",
		"base_url":              upstream.URL,
		"key":                   "sk-test",
		"probe_model":           "",
		"create_target":         false,
		"bind_existing":         true,
		"site_id":               0,
		"group_id":              0,
		"minimum_upstream_rate": 0.6,
		"adjustment":            -0.1,
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/sources/detect", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+session.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		t.Fatalf("detect status=%d body=%v", resp.StatusCode, body)
	}
	var preview struct {
		Capability connectors.Capability `json:"capability"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	if preview.Capability.MonitorState != connectors.MonitorDirect {
		t.Fatalf("monitor state=%q", preview.Capability.MonitorState)
	}

	createReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/sources", bytes.NewReader(payload))
	createReq.Header.Set("Authorization", "Bearer "+session.Token)
	createReq.Header.Set("Content-Type", "application/json")
	createResp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		var body map[string]any
		_ = json.NewDecoder(createResp.Body).Decode(&body)
		t.Fatalf("create status=%d body=%v", createResp.StatusCode, body)
	}
	var createdSource store.Source
	if err = json.NewDecoder(createResp.Body).Decode(&createdSource); err != nil {
		t.Fatal(err)
	}
	syncReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/sources/"+strconv.FormatInt(createdSource.ID, 10)+"/sync", nil)
	syncReq.Header.Set("Authorization", "Bearer "+session.Token)
	syncResp, err := http.DefaultClient.Do(syncReq)
	if err != nil {
		t.Fatal(err)
	}
	defer syncResp.Body.Close()
	var syncResult struct {
		Status  string       `json:"status"`
		Source  store.Source `json:"source"`
		Results []any        `json:"results"`
	}
	if err = json.NewDecoder(syncResp.Body).Decode(&syncResult); err != nil {
		t.Fatal(err)
	}
	if syncResp.StatusCode != http.StatusOK || syncResult.Status != "success" || syncResult.Source.LastRate == nil || *syncResult.Source.LastRate != 0.8 || len(syncResult.Source.Models) != 1 || len(syncResult.Results) != 0 {
		t.Fatalf("sync status=%d result=%+v", syncResp.StatusCode, syncResult)
	}

	missingKeyPayload, _ := json.Marshal(map[string]any{
		"name": "missing-key", "base_url": upstream.URL, "key": "", "probe_model": "",
		"create_target": false, "bind_existing": false, "site_id": 0, "group_id": 0, "adjustment": 0,
	})
	missingKeyReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/sources", bytes.NewReader(missingKeyPayload))
	missingKeyReq.Header.Set("Authorization", "Bearer "+session.Token)
	missingKeyReq.Header.Set("Content-Type", "application/json")
	missingKeyResp, err := http.DefaultClient.Do(missingKeyReq)
	if err != nil {
		t.Fatal(err)
	}
	defer missingKeyResp.Body.Close()
	if missingKeyResp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing-key create status=%d, want %d", missingKeyResp.StatusCode, http.StatusUnprocessableEntity)
	}
}

func TestDetectSourceUsesImportedAccountModelsWhenUpstreamModelsUnavailable(t *testing.T) {
	upstreamMux := http.NewServeMux()
	upstreamMux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":"INSUFFICIENT_BALANCE"}`, http.StatusForbidden)
	})
	upstreamMux.HandleFunc("GET /v1/sub2api/billing", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"effective_rate_multiplier": 0.03})
	})
	upstream := httptest.NewServer(upstreamMux)
	defer upstream.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "model-fallback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	user, err := st.CreateUser("fallback@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	site, err := st.CreateSite(store.Site{UserID: user.ID, Name: "target", BaseURL: "https://target.example.com", Platform: "sub2api", AdminSecret: "encrypted", AdminHeader: "x-api-key"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.ReplaceInventory(user.ID, site.ID,
		[]store.Group{{ExternalID: "g-1", Name: "group", Rate: 1, Status: "active"}},
		[]store.Account{{ExternalID: "a-1", Name: "account", Platform: "sub2api", BaseURL: upstream.URL, Models: []string{"model-b", "model-a"}, RelationGroups: []string{"g-1"}}},
	); err != nil {
		t.Fatal(err)
	}
	groups, err := st.Inventory(user.ID, site.ID)
	if err != nil || len(groups) != 1 || len(groups[0].Accounts) != 1 {
		t.Fatalf("inventory=%+v err=%v", groups, err)
	}

	key := []byte("0123456789abcdef0123456789abcdef")
	cfg := config.Config{MasterKey: key, SessionSecret: key, PollInterval: time.Hour, ProbeInterval: time.Hour, ModelInterval: time.Hour, EmailInterval: time.Hour}
	vault, _ := security.NewVault(key)
	client := connectors.New()
	hub := syncer.NewHub()
	engine := syncer.New(st, vault, client, hub, cfg)
	server := New(cfg, st, vault, client, engine, hub)
	capability, err := server.detectSourceCapability(t.Context(), user.ID, sourceInput{
		BaseURL: upstream.URL, Key: "sk-test", SiteID: site.ID, GroupID: groups[0].ID, AccountID: groups[0].Accounts[0].ID, BindExisting: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(capability.Models) != 2 || capability.Models[0] != "model-a" || capability.Models[1] != "model-b" {
		t.Fatalf("models=%v", capability.Models)
	}
	if capability.ModelsMessage != "上游模型接口暂时无法读取，已采用该账号最近一次导入的模型清单" {
		t.Fatalf("models message=%q", capability.ModelsMessage)
	}
}

func TestCreateSourceBuildsIndependentTasksForMultipleTargetGroups(t *testing.T) {
	upstreamMux := http.NewServeMux()
	upstreamMux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "cheap-model"}}})
	})
	upstreamMux.HandleFunc("GET /v1/sub2api/billing", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"effective_rate_multiplier": 0.8})
	})
	upstream := httptest.NewServer(upstreamMux)
	defer upstream.Close()

	key := []byte("0123456789abcdef0123456789abcdef")
	cfg := config.Config{MasterKey: key, SessionSecret: key, PollInterval: time.Hour, ProbeInterval: time.Hour, ModelInterval: time.Hour, EmailInterval: time.Hour}
	st, err := store.Open(filepath.Join(t.TempDir(), "multi-target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	vault, _ := security.NewVault(key)
	client := connectors.New()
	hub := syncer.NewHub()
	engine := syncer.New(st, vault, client, hub, cfg)
	srv := httptest.NewServer(New(cfg, st, vault, client, engine, hub).Handler())
	defer srv.Close()

	registerBody, _ := json.Marshal(map[string]string{"email": "multi-target@example.com", "password": "password-123"})
	registerResp, err := http.Post(srv.URL+"/api/auth/register", "application/json", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatal(err)
	}
	defer registerResp.Body.Close()
	var session struct {
		Token string `json:"token"`
	}
	if err = json.NewDecoder(registerResp.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	user, err := st.UserByEmail("multi-target@example.com")
	if err != nil {
		t.Fatal(err)
	}
	site, err := st.CreateSite(store.Site{UserID: user.ID, Name: "target", BaseURL: "https://target.example.com", Platform: "sub2api", AdminSecret: "encrypted", AdminHeader: "x-api-key"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.ReplaceInventory(user.ID, site.ID, []store.Group{
		{ExternalID: "1", Name: "group-a", Rate: 1, Status: "active"},
		{ExternalID: "2", Name: "group-b", Rate: 1, Status: "active"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	groups, err := st.Inventory(user.ID, site.ID)
	if err != nil || len(groups) != 2 {
		t.Fatalf("groups=%+v err=%v", groups, err)
	}
	payload, _ := json.Marshal(map[string]any{
		"name": "multi upstream", "base_url": upstream.URL, "key": "sk-test", "probe_model": "", "create_target": false, "bind_existing": true,
		"targets": []map[string]any{
			{"site_id": site.ID, "group_id": groups[0].ID, "minimum_upstream_rate": 1.0, "adjustment": 0.1},
			{"site_id": site.ID, "group_id": groups[1].ID, "minimum_upstream_rate": 1.2, "adjustment": -0.1},
		},
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/sources", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+session.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		t.Fatalf("status=%d body=%v", resp.StatusCode, body)
	}
	tasks, err := st.Tasks(user.ID)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	byGroup := map[int64]store.Task{tasks[0].GroupID: tasks[0], tasks[1].GroupID: tasks[1]}
	if byGroup[groups[0].ID].MinUpstreamRate != 1.0 || byGroup[groups[0].ID].Adjustment != 0.1 || byGroup[groups[1].ID].MinUpstreamRate != 1.2 || byGroup[groups[1].ID].Adjustment != -0.1 {
		t.Fatalf("tasks=%+v", tasks)
	}
}

func TestSyncDemoSourceIsSkipped(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	cfg := config.Config{MasterKey: key, SessionSecret: key, PollInterval: time.Hour, ProbeInterval: time.Hour, ModelInterval: time.Hour, EmailInterval: time.Hour}
	st, err := store.Open(filepath.Join(t.TempDir(), "demo-sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	vault, _ := security.NewVault(key)
	client := connectors.New()
	hub := syncer.NewHub()
	engine := syncer.New(st, vault, client, hub, cfg)
	srv := httptest.NewServer(New(cfg, st, vault, client, engine, hub).Handler())
	defer srv.Close()

	registerBody, _ := json.Marshal(map[string]string{"email": "demo-source@example.com", "password": "password-123"})
	registerResp, err := http.Post(srv.URL+"/api/auth/register", "application/json", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatal(err)
	}
	defer registerResp.Body.Close()
	var session struct {
		Token string `json:"token"`
	}
	if err = json.NewDecoder(registerResp.Body).Decode(&session); err != nil || registerResp.StatusCode != http.StatusOK {
		t.Fatalf("register status=%d err=%v", registerResp.StatusCode, err)
	}
	user, err := st.UserByEmail("demo-source@example.com")
	if err != nil {
		t.Fatal(err)
	}
	source, err := st.CreateSource(store.Source{UserID: user.ID, Name: "演示上游", BaseURL: "https://upstream.demo.local", Platform: "newapi", Secret: "demo", MonitorState: connectors.MonitorNewAPIProbe, Models: []string{"gpt-4o-mini"}})
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/sources/"+strconv.FormatInt(source.ID, 10)+"/sync", nil)
	req.Header.Set("Authorization", "Bearer "+session.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result struct {
		Status  string       `json:"status"`
		Message string       `json:"message"`
		Source  store.Source `json:"source"`
		Results []any        `json:"results"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || result.Status != "skipped" || result.Message != store.DemoSourceMessage {
		t.Fatalf("sync status=%d result=%+v", resp.StatusCode, result)
	}
	if result.Source.MonitorState != connectors.MonitorDemo || result.Source.LastError != "" || len(result.Results) != 0 || len(result.Source.HealthHistory) == 0 || result.Source.HealthHistory[0].State != "skipped" {
		t.Fatalf("unexpected demo source result: %+v", result)
	}
}
