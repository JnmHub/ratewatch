package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	MonitorDirect       = "direct"
	MonitorNewAPIProbe  = "newapi_probe"
	MonitorPassiveImage = "passive_image"
	MonitorMissingKey   = "missing_key"
	MonitorUnsupported  = "unsupported"
	MonitorFailed       = "check_failed"
	MonitorDemo         = "demo"
)

type Capability struct {
	Platform          string             `json:"platform"`
	MonitorState      string             `json:"monitor_state"`
	Models            []string           `json:"models"`
	ModelsMessage     string             `json:"models_message,omitempty"`
	Rate              *float64           `json:"rate,omitempty"`
	RequestID         string             `json:"request_id,omitempty"`
	Message           string             `json:"message"`
	ImageObservations []ImageObservation `json:"-"`
}

type ImageObservation struct {
	Model, Size, Quality, RequestID string
	Count                           int
	GroupRate, UnitPrice            *float64
	ActualCost                      float64
	ObservedAt                      time.Time
}

func (c *Client) Models(ctx context.Context, base, key string) ([]string, error) {
	b, e := normalizeBase(base)
	if e != nil {
		return nil, e
	}
	r, e := c.do(ctx, http.MethodGet, b+"/v1/models", Auth{Secret: key}, nil)
	if e != nil {
		return nil, e
	}
	var raw any
	if e = json.Unmarshal(r.Body, &raw); e != nil {
		return nil, e
	}
	items := asSlice(dataOf(raw))
	out := make([]string, 0, len(items))
	for _, item := range items {
		m := asMap(item)
		if id := stringOf(first(m, "id", "model", "name")); id != "" {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("/v1/models 未返回模型")
	}
	return out, nil
}

func (c *Client) Detect(ctx context.Context, base, key, probeModel string) (Capability, error) {
	if strings.TrimSpace(key) == "" {
		return Capability{MonitorState: MonitorMissingKey, Message: "缺少普通 API Key"}, nil
	}
	b, e := normalizeBase(base)
	if e != nil {
		return Capability{MonitorState: MonitorFailed, Message: e.Error()}, e
	}
	// Sub2API 的专用计费端点不消耗额度，即使余额不足导致 /v1/models
	// 返回 403，普通 Key 仍然可以可靠读取当前生效倍率。
	if rate, req, billingErr := c.Sub2APIBilling(ctx, b, key); billingErr == nil {
		models, modelsErr := c.Models(ctx, b, key)
		modelsMessage := "已从上游读取当前可用模型"
		if modelsErr != nil {
			modelsMessage = "模型接口暂时无法读取：" + modelsErr.Error()
		}
		return Capability{
			Platform:      "sub2api",
			MonitorState:  MonitorDirect,
			Models:        models,
			ModelsMessage: modelsMessage,
			Rate:          &rate,
			RequestID:     req,
			Message:       "普通 Key 可直接读取生效倍率",
		}, nil
	}
	models, e := c.Models(ctx, b, key)
	if e != nil {
		return Capability{MonitorState: MonitorFailed, Message: e.Error()}, e
	}
	cap := Capability{Models: models, MonitorState: MonitorUnsupported, Message: "模型接口可用，但暂未识别到受支持的计费接口"}
	if probeModel == "" {
		if _, statusErr := c.do(ctx, http.MethodGet, b+"/api/status", Auth{Secret: key}, nil); statusErr != nil {
			cap.Platform = "unknown"
			cap.MonitorState = MonitorUnsupported
			cap.Message = "模型接口兼容，但未识别到 New API 或 Sub2API 的可监听计费接口"
			return cap, nil
		}
		cap.Platform = "newapi"
		cap.MonitorState = MonitorNewAPIProbe
		cap.Message = "需要选择便宜模型完成 New API 最小请求探测"
		return cap, nil
	}
	rate, requestID, observations, e := c.NewAPIProbeWithObservations(ctx, b, key, probeModel)
	if e != nil {
		cap.Platform = "newapi"
		cap.MonitorState = MonitorFailed
		cap.Message = e.Error()
		return cap, e
	}
	cap.Platform = "newapi"
	cap.MonitorState = MonitorNewAPIProbe
	cap.Rate = &rate
	cap.RequestID = requestID
	cap.ImageObservations = observations
	cap.Message = "已通过最小文本请求和 Key 日志获取倍率"
	return cap, nil
}

func (c *Client) Sub2APIBilling(ctx context.Context, base, key string) (float64, string, error) {
	r, e := c.do(ctx, http.MethodGet, base+"/v1/sub2api/billing", Auth{Secret: key}, nil)
	if e != nil {
		return 0, "", e
	}
	m, e := anyMap(r.Body)
	if e != nil {
		return 0, "", e
	}
	m = asMap(dataOf(m))
	for _, k := range []string{"effective_rate_multiplier", "resolved_rate_multiplier", "group_rate_multiplier"} {
		if f, ok := floatOf(m[k]); ok {
			return f, r.Header.Get("X-Oneapi-Request-Id"), nil
		}
	}
	return 0, "", errors.New("计费响应缺少有效倍率")
}

func (c *Client) NewAPIProbe(ctx context.Context, base, key, model string) (float64, string, error) {
	rate, requestID, _, err := c.NewAPIProbeWithObservations(ctx, base, key, model)
	return rate, requestID, err
}

func (c *Client) NewAPIProbeWithObservations(ctx context.Context, base, key, model string) (float64, string, []ImageObservation, error) {
	payload := map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "."}}, "stream": false, "max_tokens": 1}
	r, e := c.do(ctx, http.MethodPost, base+"/v1/chat/completions", Auth{Secret: key}, payload)
	if e != nil {
		return 0, "", nil, fmt.Errorf("最小文本请求失败: %w", e)
	}
	rid := r.Header.Get("X-Oneapi-Request-Id")
	if rid == "" {
		rid = r.Header.Get("X-Request-Id")
	}
	if rid == "" {
		return 0, "", nil, errors.New("响应缺少 X-Oneapi-Request-Id，无法精确匹配计费日志")
	}
	for attempt := 0; attempt < 7; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, rid, nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
		logs, e := c.do(ctx, http.MethodGet, base+"/api/log/token", Auth{Secret: key}, nil)
		if e != nil {
			continue
		}
		if rate, ok := findLogRate(logs.Body, rid); ok {
			observations, _ := parseNewAPIImageObservations(logs.Body)
			return rate, rid, observations, nil
		}
	}
	return 0, rid, nil, errors.New("在 Key 日志中未找到本次请求倍率；上游可能关闭了普通用户日志")
}

func findLogRate(body []byte, requestID string) (float64, bool) {
	var raw any
	if json.Unmarshal(body, &raw) != nil {
		return 0, false
	}
	var walk func(any) (float64, bool)
	walk = func(v any) (float64, bool) {
		switch x := v.(type) {
		case []any:
			for _, it := range x {
				if f, ok := walk(it); ok {
					return f, true
				}
			}
		case map[string]any:
			rid := stringOf(first(x, "request_id", "requestId"))
			if rid == requestID {
				other := x["other"]
				if s, ok := other.(string); ok {
					var parsed any
					if json.Unmarshal([]byte(s), &parsed) == nil {
						other = parsed
					}
				}
				if f, ok := floatOf(asMap(other)["group_ratio"]); ok {
					return f, true
				}
				if f, ok := floatOf(x["group_ratio"]); ok {
					return f, true
				}
			}
			for _, k := range []string{"data", "items", "logs", "records"} {
				if child, ok := x[k]; ok {
					if f, ok := walk(child); ok {
						return f, true
					}
				}
			}
		}
		return 0, false
	}
	return walk(raw)
}

func (c *Client) NewAPIImageObservations(ctx context.Context, base, key string) ([]ImageObservation, error) {
	b, e := normalizeBase(base)
	if e != nil {
		return nil, e
	}
	r, e := c.do(ctx, http.MethodGet, b+"/api/log/token", Auth{Secret: key}, nil)
	if e != nil {
		return nil, e
	}
	return parseNewAPIImageObservations(r.Body)
}

func parseNewAPIImageObservations(body []byte) ([]ImageObservation, error) {
	var raw any
	if e := json.Unmarshal(body, &raw); e != nil {
		return nil, e
	}
	var out []ImageObservation
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				walk(item)
			}
		case map[string]any:
			other := x["other"]
			if text, ok := other.(string); ok {
				var decoded any
				if json.Unmarshal([]byte(text), &decoded) == nil {
					other = decoded
				}
			}
			om := asMap(other)
			size := stringOf(first(om, "size", "image_size", "dimensions"))
			if size == "" {
				size = stringOf(first(x, "size", "image_size", "dimensions"))
			}
			quality := stringOf(first(om, "quality", "image_quality"))
			if quality == "" {
				quality = stringOf(first(x, "quality", "image_quality"))
			}
			marker := strings.ToLower(stringOf(first(om, "endpoint", "task_type", "type")) + " " + stringOf(first(x, "endpoint", "type_name", "request_type")))
			if size != "" || quality != "" || strings.Contains(marker, "image") {
				model := stringOf(first(x, "model_name", "model", "model_id"))
				if model == "" {
					model = stringOf(first(om, "model", "model_name"))
				}
				rid := stringOf(first(x, "request_id", "requestId"))
				if rid == "" {
					rid = stringOf(first(om, "request_id", "requestId"))
				}
				if model != "" && rid != "" {
					count := 1
					if n, ok := floatOf(first(om, "n", "count", "image_count")); ok && n > 0 {
						count = int(n)
					}
					var groupRate, unitPrice *float64
					if f, ok := floatOf(first(om, "group_ratio", "group_rate", "group_multiplier")); ok {
						groupRate = &f
					}
					if f, ok := floatOf(first(om, "model_price", "image_price", "unit_price")); ok {
						unitPrice = &f
					}
					cost, _ := floatOf(first(x, "actual_cost", "cost", "quota"))
					if cost == 0 {
						cost, _ = floatOf(first(om, "actual_cost", "cost", "quota"))
					}
					observed := time.Now()
					if unix, ok := floatOf(first(x, "created_at", "createdAt", "timestamp")); ok && unix > 0 {
						observed = time.Unix(int64(unix), 0)
					}
					out = append(out, ImageObservation{Model: model, Size: size, Quality: quality, RequestID: rid, Count: count, GroupRate: groupRate, UnitPrice: unitPrice, ActualCost: cost, ObservedAt: observed})
				}
			}
			for _, name := range []string{"data", "items", "logs", "records"} {
				if child, ok := x[name]; ok {
					walk(child)
				}
			}
		}
	}
	walk(raw)
	return out, nil
}
