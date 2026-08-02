package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewAPIProbeMatchesRequestID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Oneapi-Request-Id", "req-42")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
	})
	mux.HandleFunc("GET /api/log/token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"request_id": "image-1", "model_name": "gpt-image-1", "other": `{"endpoint":"/v1/images/generations","size":"1024x1024","group_ratio":1.25}`}, map[string]any{"request_id": "req-42", "other": "{\"group_ratio\":1.25,\"user_group_ratio\":2}"}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rate, rid, observations, err := New().NewAPIProbeWithObservations(context.Background(), srv.URL, "sk-test", "cheap-model")
	if err != nil {
		t.Fatal(err)
	}
	if rid != "req-42" || rate != 1.25 {
		t.Fatalf("rate=%v rid=%s", rate, rid)
	}
	if len(observations) != 1 || observations[0].RequestID != "image-1" {
		t.Fatalf("observations=%+v", observations)
	}
}

func TestSub2APIBillingPrefersEffectiveRate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"group_rate_multiplier": 1.1, "resolved_rate_multiplier": 1.2, "effective_rate_multiplier": 1.35})
	}))
	defer srv.Close()
	rate, _, err := New().Sub2APIBilling(context.Background(), srv.URL, "sk-test")
	if err != nil || rate != 1.35 {
		t.Fatalf("rate=%v err=%v", rate, err)
	}
}

func TestDetectSub2APIDoesNotRequireModelBalance(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sub2api/billing", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"effective_rate_multiplier": 0.8})
	})
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":"INSUFFICIENT_BALANCE"}`, http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cap, err := New().Detect(context.Background(), srv.URL, "sk-test", "")
	if err != nil || cap.Platform != "sub2api" || cap.MonitorState != MonitorDirect || cap.Rate == nil || *cap.Rate != 0.8 {
		t.Fatalf("cap=%+v err=%v", cap, err)
	}
	if cap.ModelsMessage == "" {
		t.Fatalf("模型读取失败时应返回具体原因: cap=%+v", cap)
	}
}

func TestNewAPIImageObservationsParseRealLog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"request_id": "img-1", "model_name": "gpt-image-1", "quota": 250, "created_at": 1700000000, "other": `{"endpoint":"/v1/images/generations","size":"1024x1024","quality":"hd","n":2,"group_ratio":1.3,"model_price":0.04}`}, map[string]any{"request_id": "text-1", "model_name": "gpt-4", "other": `{"group_ratio":1.2}`}}})
	}))
	defer srv.Close()
	items, err := New().NewAPIImageObservations(context.Background(), srv.URL, "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("observations=%v", items)
	}
	got := items[0]
	if got.RequestID != "img-1" || got.Model != "gpt-image-1" || got.Size != "1024x1024" || got.Quality != "hd" || got.Count != 2 || got.ActualCost != 250 || got.GroupRate == nil || *got.GroupRate != 1.3 || got.UnitPrice == nil || *got.UnitPrice != .04 {
		t.Fatalf("parsed=%+v", got)
	}
}
