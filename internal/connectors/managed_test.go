package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ratewatch/internal/store"
)

func TestNewAPIGroupRateMergePreservesOtherGroups(t *testing.T) {
	var written map[string]float64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/option/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"key": "GroupRatio", "value": `{"default":1,"vip":1.2}`}}})
	})
	mux.HandleFunc("PUT /api/option/", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Key, Value string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Key != "GroupRatio" {
			t.Errorf("key=%s", body.Key)
		}
		if err := json.Unmarshal([]byte(body.Value), &written); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	err := New().SetGroupRate(context.Background(), ManagedSite{BaseURL: srv.URL, Platform: "newapi", Auth: Auth{Secret: "admin"}}, store.Group{Name: "vip"}, 1.5)
	if err != nil {
		t.Fatal(err)
	}
	if written["default"] != 1 || written["vip"] != 1.5 || len(written) != 2 {
		t.Fatalf("merged ratios=%v", written)
	}
}

func TestNewAPIDoesNotRecreateMissingGroup(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/option/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"key": "GroupRatio", "value": `{"default":1}`}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	err := New().SetGroupRate(context.Background(), ManagedSite{BaseURL: srv.URL, Platform: "newapi", Auth: Auth{Secret: "admin"}}, store.Group{Name: "deleted"}, 1.5)
	if err == nil {
		t.Fatal("missing group was unexpectedly recreated")
	}
}

func TestSub2APIAccountTypeFilter(t *testing.T) {
	cases := []struct {
		raw  map[string]any
		want bool
	}{
		{map[string]any{"auth_type": "api_key"}, true},
		{map[string]any{"type": "apikey"}, true},
		{map[string]any{"type": "oauth"}, false},
		{map[string]any{"type": "setup-token"}, false},
		{map[string]any{"credential_type": "oauth"}, false},
		{map[string]any{"account_type": "setup_token"}, false},
		{map[string]any{"credential_kind": "credential_file"}, false},
		{map[string]any{"credentials": map[string]any{"api_key": "***"}}, true},
		{map[string]any{"credentials": map[string]any{"refresh_token": "***"}}, false},
	}
	for _, tc := range cases {
		if got := isSub2APIKeyAccount(tc.raw); got != tc.want {
			t.Fatalf("filter(%v)=%v want %v", tc.raw, got, tc.want)
		}
	}
}

func TestParseSub2APICurrentAccountResponse(t *testing.T) {
	body := []byte(`{"code":0,"data":{"items":[{"id":1,"name":"API account","platform":"openai","type":"apikey","groups":[{"id":2,"name":"target"}],"group_ids":[2],"extra":{"openai_responses_supported":true},"credentials":{"base_url":"https://upstream.example/v1","model_mapping":{"gpt-5.6-sol":"gpt-5.6-sol","gpt-5.4":"gpt-5.4"}}}]}}`)
	accounts := parseAccounts(body, "sub2api")
	if len(accounts) != 1 || accounts[0].Name != "API account" || accounts[0].BaseURL != "https://upstream.example/v1" || len(accounts[0].Models) != 2 || accounts[0].Models[0] != "gpt-5.4" || accounts[0].Models[1] != "gpt-5.6-sol" || len(accounts[0].RelationGroups) != 1 || accounts[0].RelationGroups[0] != "2" {
		t.Fatalf("accounts=%+v", accounts)
	}
}

func TestSplitModelsDecodesJSONMappingString(t *testing.T) {
	models := splitModels(`{"gpt-b":"upstream-b","gpt-a":"upstream-a"}`)
	if len(models) != 2 || models[0] != "gpt-a" || models[1] != "gpt-b" {
		t.Fatalf("models=%v", models)
	}
}

func TestSub2APIImportAlwaysUsesAdminAPIKeyHeader(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("x-api-key"); got != "admin-secret" {
			t.Errorf("x-api-key=%q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("unexpected Authorization header=%q", got)
		}
		switch r.URL.Path {
		case "/api/v1/admin/groups":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": 1, "name": "default", "rate_multiplier": 1}}})
		case "/api/v1/admin/accounts":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []any{}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	groups, _, err := New().Import(context.Background(), ManagedSite{
		BaseURL:  srv.URL,
		Platform: "sub2api",
		Auth:     Auth{Secret: "admin-secret", Header: "Authorization", UserID: "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(groups) != 1 {
		t.Fatalf("requests=%d groups=%+v", requests, groups)
	}
}

func TestCreateAccountUsesCurrentPayloads(t *testing.T) {
	tests := []struct {
		platform string
		groupID  string
		check    func(*testing.T, map[string]any)
	}{
		{"newapi", "default", func(t *testing.T, body map[string]any) {
			channel := asMap(body["channel"])
			if body["mode"] != "single" || channel["group"] != "target" || channel["models"] != "cheap-model" {
				t.Fatalf("newapi body=%v", body)
			}
		}},
		{"sub2api", "2", func(t *testing.T, body map[string]any) {
			ids := asSlice(body["group_ids"])
			if body["platform"] != "openai" || body["type"] != "apikey" || len(ids) != 1 || ids[0] != float64(2) || asMap(body["extra"])["base_url"] != "https://up.example" {
				t.Fatalf("sub2api body=%v", body)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.platform, func(t *testing.T) {
			var got map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
			}))
			defer srv.Close()
			err := New().CreateAccount(context.Background(), ManagedSite{BaseURL: srv.URL, Platform: tc.platform}, CreateAccountInput{Name: "test", BaseURL: "https://up.example/", Key: "sk-test", Platform: "sub2api", GroupName: "target", GroupExternalID: tc.groupID, Models: []string{"cheap-model"}})
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, got)
		})
	}
}

func TestCreateAccountBindsMultipleGroupsInOneTarget(t *testing.T) {
	for _, platform := range []string{"newapi", "sub2api"} {
		t.Run(platform, func(t *testing.T) {
			var got map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
			}))
			defer srv.Close()
			err := New().CreateAccount(context.Background(), ManagedSite{BaseURL: srv.URL, Platform: platform}, CreateAccountInput{
				Name: "multi", BaseURL: "https://up.example", Key: "sk-test", GroupNames: []string{"group-a", "group-b"}, GroupExternalIDs: []string{"2", "3"}, Models: []string{"cheap-model"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if platform == "newapi" {
				if group := asMap(got["channel"])["group"]; group != "group-a,group-b" {
					t.Fatalf("group=%v body=%v", group, got)
				}
				return
			}
			ids := asSlice(got["group_ids"])
			if len(ids) != 2 || ids[0] != float64(2) || ids[1] != float64(3) {
				t.Fatalf("group_ids=%v body=%v", ids, got)
			}
		})
	}
}
