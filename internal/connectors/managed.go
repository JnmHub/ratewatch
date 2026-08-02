package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"ratewatch/internal/store"
)

type ManagedSite struct {
	BaseURL, Platform string
	Auth              Auth
}

func (c *Client) Import(ctx context.Context, site ManagedSite) ([]store.Group, []store.Account, error) {
	site = normalizeManagedSiteAuth(site)
	b, e := normalizeBase(site.BaseURL)
	if e != nil {
		return nil, nil, e
	}
	site.BaseURL = b
	switch site.Platform {
	case "newapi":
		return c.importNewAPI(ctx, site)
	case "sub2api":
		return c.importSub2API(ctx, site)
	default:
		return nil, nil, errors.New("不支持的目标站点类型")
	}
}

func (c *Client) importNewAPI(ctx context.Context, s ManagedSite) ([]store.Group, []store.Account, error) {
	opts, e := c.do(ctx, http.MethodGet, s.BaseURL+"/api/option/", s.Auth, nil)
	if e != nil {
		opts, e = c.do(ctx, http.MethodGet, s.BaseURL+"/api/option", s.Auth, nil)
	}
	if e != nil {
		return nil, nil, fmt.Errorf("读取 New API 配置失败: %w", e)
	}
	ratios, e := parseGroupRatios(opts.Body)
	if e != nil {
		return nil, nil, e
	}
	groups := make([]store.Group, 0, len(ratios))
	for name, rate := range ratios {
		groups = append(groups, store.Group{ExternalID: name, Name: name, Rate: rate, Status: "active"})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	ch, e := c.do(ctx, http.MethodGet, s.BaseURL+"/api/channel/?p=0&page_size=1000", s.Auth, nil)
	if e != nil {
		ch, e = c.do(ctx, http.MethodGet, s.BaseURL+"/api/channel/", s.Auth, nil)
	}
	if e != nil {
		return nil, nil, fmt.Errorf("读取 New API 渠道失败: %w", e)
	}
	accounts := parseAccounts(ch.Body, "newapi")
	return groups, accounts, nil
}

func parseGroupRatios(body []byte) (map[string]float64, error) {
	var raw any
	if e := json.Unmarshal(body, &raw); e != nil {
		return nil, e
	}
	var candidate any
	for _, item := range asSlice(dataOf(raw)) {
		m := asMap(item)
		if stringOf(first(m, "key", "name")) == "GroupRatio" {
			candidate = first(m, "value", "Value")
			break
		}
	}
	if candidate == nil {
		m := asMap(dataOf(raw))
		candidate = first(m, "GroupRatio", "group_ratio")
	}
	if s, ok := candidate.(string); ok {
		var decoded any
		if json.Unmarshal([]byte(s), &decoded) == nil {
			candidate = decoded
		}
	}
	out := map[string]float64{}
	for k, v := range asMap(candidate) {
		if f, ok := floatOf(v); ok {
			out[k] = f
		}
	}
	if len(out) == 0 {
		return nil, errors.New("New API 配置中未找到 GroupRatio")
	}
	return out, nil
}

func parseAccounts(body []byte, platform string) []store.Account {
	var raw any
	if json.Unmarshal(body, &raw) != nil {
		return nil
	}
	items := asSlice(dataOf(raw))
	out := make([]store.Account, 0, len(items))
	for _, v := range items {
		m := asMap(v)
		if platform == "sub2api" && !isSub2APIKeyAccount(m) {
			continue
		}
		credentials := asMap(first(m, "credentials", "credential"))
		baseURL := stringOf(first(m, "base_url", "baseUrl", "api_base"))
		if baseURL == "" {
			baseURL = stringOf(first(asMap(m["extra"]), "base_url", "baseUrl", "api_base"))
		}
		if baseURL == "" {
			baseURL = stringOf(first(credentials, "base_url", "baseUrl", "api_base"))
		}
		models := first(m, "models", "model_mapping")
		if models == nil {
			models = first(credentials, "models", "model_mapping")
		}
		a := store.Account{ExternalID: stringOf(first(m, "id", "uuid")), Name: stringOf(first(m, "name", "remark")), Platform: platform, BaseURL: baseURL, Models: splitModels(models)}
		if a.ExternalID == "" {
			continue
		}
		if a.Name == "" {
			a.Name = "账号 " + a.ExternalID
		}
		// 当前 Sub2API 同时返回 groups（对象数组）和 group_ids（数字数组）；
		// 关联时应优先使用稳定的 ID，New API 则继续读取 group 字符串。
		groups := splitModels(first(m, "group_ids", "group", "groups"))
		a.RelationGroups = groups
		out = append(out, a)
	}
	return out
}

func isSub2APIKeyAccount(m map[string]any) bool {
	authType := strings.ToLower(stringOf(first(m, "type", "auth_type", "credential_type", "account_type", "login_type", "credential_kind")))
	for _, blocked := range []string{"oauth", "setup", "session", "cookie", "file", "credential_file", "refresh_token"} {
		if strings.Contains(authType, blocked) {
			return false
		}
	}
	if authType != "" {
		return strings.Contains(authType, "api") || strings.Contains(authType, "key") || strings.Contains(authType, "token")
	}
	if credentials := asMap(first(m, "credentials", "credential")); len(credentials) > 0 {
		for key := range credentials {
			normalized := strings.ToLower(key)
			if strings.Contains(normalized, "api_key") || normalized == "key" {
				return true
			}
		}
		return false
	}
	// 部分管理员列表会把凭证整体脱敏且不返回类型；保留这类未知项供用户手工确认，
	// 但明确标注的 OAuth/会话/文件账号已经在上面排除。
	return true
}

func (c *Client) importSub2API(ctx context.Context, s ManagedSite) ([]store.Group, []store.Account, error) {
	gr, e := c.do(ctx, http.MethodGet, s.BaseURL+"/api/v1/admin/groups", s.Auth, nil)
	if e != nil {
		return nil, nil, fmt.Errorf("读取 Sub2API 分组失败: %w", e)
	}
	var raw any
	_ = json.Unmarshal(gr.Body, &raw)
	var groups []store.Group
	for _, v := range asSlice(dataOf(raw)) {
		m := asMap(v)
		rate, _ := floatOf(first(m, "rate_multiplier", "rate", "multiplier"))
		id := stringOf(first(m, "id", "uuid"))
		if id != "" {
			groups = append(groups, store.Group{ExternalID: id, Name: stringOf(first(m, "name", "title")), Rate: rate, Status: stringOf(first(m, "status", "state"))})
		}
	}
	ac, e := c.do(ctx, http.MethodGet, s.BaseURL+"/api/v1/admin/accounts?page=1&page_size=1000&limit=1000", s.Auth, nil)
	if e != nil {
		return nil, nil, fmt.Errorf("读取 Sub2API 账号失败: %w", e)
	}
	accounts := parseAccounts(ac.Body, "sub2api")
	return groups, accounts, nil
}

func (c *Client) SetGroupRate(ctx context.Context, s ManagedSite, g store.Group, rate float64) error {
	s = normalizeManagedSiteAuth(s)
	switch s.Platform {
	case "sub2api":
		_, e := c.do(ctx, http.MethodPut, s.BaseURL+"/api/v1/admin/groups/"+url.PathEscape(g.ExternalID), s.Auth, map[string]any{"rate_multiplier": rate})
		return e
	case "newapi":
		return c.setNewAPIGroupRate(ctx, s, g.Name, rate)
	default:
		return errors.New("不支持的目标站点类型")
	}
}

func (c *Client) setNewAPIGroupRate(ctx context.Context, s ManagedSite, name string, rate float64) error {
	r, e := c.do(ctx, http.MethodGet, s.BaseURL+"/api/option/", s.Auth, nil)
	if e != nil {
		r, e = c.do(ctx, http.MethodGet, s.BaseURL+"/api/option", s.Auth, nil)
	}
	if e != nil {
		return e
	}
	ratios, e := parseGroupRatios(r.Body)
	if e != nil {
		return e
	}
	if _, exists := ratios[name]; !exists {
		return errors.New("目标分组已不存在，拒绝自动创建")
	}
	ratios[name] = rate
	b, _ := json.Marshal(ratios)
	_, e = c.do(ctx, http.MethodPut, s.BaseURL+"/api/option/", s.Auth, map[string]any{"key": "GroupRatio", "value": string(b)})
	return e
}

type CreateAccountInput struct {
	Name, BaseURL, Key, Platform, GroupName, GroupExternalID string
	GroupNames, GroupExternalIDs                             []string
	Models                                                   []string
}

func (c *Client) CreateAccount(ctx context.Context, s ManagedSite, in CreateAccountInput) error {
	s = normalizeManagedSiteAuth(s)
	groupNames := append([]string(nil), in.GroupNames...)
	if len(groupNames) == 0 && in.GroupName != "" {
		groupNames = []string{in.GroupName}
	}
	groupExternalIDs := append([]string(nil), in.GroupExternalIDs...)
	if len(groupExternalIDs) == 0 && in.GroupExternalID != "" {
		groupExternalIDs = []string{in.GroupExternalID}
	}
	switch s.Platform {
	case "newapi":
		channel := map[string]any{"type": 1, "name": in.Name, "key": in.Key, "base_url": strings.TrimRight(in.BaseURL, "/"), "models": strings.Join(in.Models, ","), "group": strings.Join(groupNames, ","), "status": 1}
		payload := map[string]any{"mode": "single", "multi_key_mode": "random", "batch_add_set_key_prefix_2_name": false, "channel": channel}
		_, e := c.do(ctx, http.MethodPost, s.BaseURL+"/api/channel/", s.Auth, payload)
		return e
	case "sub2api":
		groupIDs := make([]int64, 0, len(groupExternalIDs))
		for _, externalID := range groupExternalIDs {
			groupID, e := strconv.ParseInt(externalID, 10, 64)
			if e != nil || groupID <= 0 {
				return errors.New("Sub2API 分组 ID 无效")
			}
			groupIDs = append(groupIDs, groupID)
		}
		if len(groupIDs) == 0 {
			return errors.New("Sub2API 至少需要一个分组")
		}
		payload := map[string]any{
			"name": in.Name,
			// New API 与 Sub2API 上游在这里都通过 OpenAI 兼容 /v1 接口接入；
			// Sub2API 的 platform 是协议适配器名称，不是上游站点品牌。
			"platform":                   "openai",
			"type":                       "apikey",
			"credentials":                map[string]any{"api_key": in.Key},
			"extra":                      map[string]any{"base_url": strings.TrimRight(in.BaseURL, "/")},
			"concurrency":                5,
			"rate_multiplier":            1,
			"group_ids":                  groupIDs,
			"confirm_mixed_channel_risk": true,
		}
		_, e := c.do(ctx, http.MethodPost, s.BaseURL+"/api/v1/admin/accounts", s.Auth, payload)
		return e
	default:
		return errors.New("不支持自动创建账号")
	}
}

func normalizeManagedSiteAuth(site ManagedSite) ManagedSite {
	if site.Platform == "sub2api" {
		site.Auth.Header = "x-api-key"
		site.Auth.UserID = ""
	}
	return site
}
