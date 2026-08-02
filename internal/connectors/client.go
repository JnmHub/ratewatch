package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Client struct{ HTTP *http.Client }

func New() *Client { return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}} }

type Auth struct{ Secret, Header, UserID string }
type Result struct {
	Status int
	Header http.Header
	Body   []byte
}

func normalizeBase(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("Base URL 必须是有效的 http(s) 地址")
	}
	return u.String(), nil
}

func (c *Client) do(ctx context.Context, method, rawURL string, auth Auth, body any) (Result, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return Result{}, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	header := auth.Header
	if header == "" {
		header = "Authorization"
	}
	if strings.EqualFold(header, "Authorization") {
		req.Header.Set(header, "Bearer "+auth.Secret)
	} else {
		req.Header.Set(header, auth.Secret)
	}
	if auth.UserID != "" {
		req.Header.Set("New-Api-User", auth.UserID)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Result{}, err
	}
	r := Result{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: b}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return r, fmt.Errorf("上游返回 HTTP %d: %s", resp.StatusCode, compact(b))
	}
	return r, nil
}

func compact(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}
func anyMap(b []byte) (map[string]any, error) {
	var v map[string]any
	e := json.Unmarshal(b, &v)
	return v, e
}
func dataOf(v any) any {
	if m, ok := v.(map[string]any); ok {
		if d, exists := m["data"]; exists {
			return d
		}
	}
	return v
}
func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asSlice(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"items", "data", "list", "records"} {
			if a, ok := m[k].([]any); ok {
				return a
			}
		}
	}
	return nil
}
func stringOf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	default:
		return ""
	}
}
func floatOf(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		f, e := x.Float64()
		return f, e == nil
	case string:
		f, e := strconv.ParseFloat(x, 64)
		return f, e == nil
	default:
		return 0, false
	}
}
func first(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}
func splitModels(v any) []string {
	switch x := v.(type) {
	case string:
		trimmed := strings.TrimSpace(x)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			var decoded any
			if json.Unmarshal([]byte(trimmed), &decoded) == nil {
				return splitModels(decoded)
			}
		}
		var out []string
		for _, s := range strings.Split(x, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case map[string]any:
		out := make([]string, 0, len(x))
		for model := range x {
			if model = strings.TrimSpace(model); model != "" {
				out = append(out, model)
			}
		}
		sort.Strings(out)
		return out
	case []any:
		out := make([]string, 0, len(x))
		for _, v := range x {
			if s := stringOf(v); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
