package syncer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"ratewatch/internal/config"
	"ratewatch/internal/connectors"
	"ratewatch/internal/security"
	"ratewatch/internal/store"
)

type Engine struct {
	store  *store.Store
	vault  *security.Vault
	client *connectors.Client
	hub    *Hub
	cfg    config.Config
	locks  sync.Map
}

func New(st *store.Store, v *security.Vault, c *connectors.Client, h *Hub, cfg config.Config) *Engine {
	return &Engine{store: st, vault: v, client: c, hub: h, cfg: cfg}
}
func (e *Engine) Start(ctx context.Context) {
	go e.syncLoop(ctx)
	go e.modelLoop(ctx)
	go e.emailLoop(ctx)
}
func (e *Engine) syncLoop(ctx context.Context) {
	e.RunAll(ctx)
	for {
		interval := e.cfg.PollInterval
		if settings, err := e.store.SystemSettings(); err == nil && settings.PollSeconds >= 10 {
			interval = time.Duration(settings.PollSeconds) * time.Second
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			e.RunAll(ctx)
		}
	}
}
func (e *Engine) RunAll(ctx context.Context) {
	tasks, err := e.store.EnabledTasks()
	if err != nil {
		return
	}
	for _, t := range tasks {
		t := t
		go func() { _ = e.RunTask(ctx, t, false) }()
	}
}

func (e *Engine) RunTask(ctx context.Context, t store.Task, force bool) error {
	lockAny, _ := e.locks.LoadOrStore(t.GroupID, &sync.Mutex{})
	lock := lockAny.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	probeInterval := e.cfg.ProbeInterval
	if settings, settingsErr := e.store.SystemSettings(); settingsErr == nil && settings.ProbeSeconds >= 10 {
		probeInterval = time.Duration(settings.ProbeSeconds) * time.Second
	}
	ids, err := e.store.TaskSourceIDs(t.UserID, t.ID)
	if err != nil {
		return err
	}
	var rates []float64
	var failures []string
	var skipped []string
	var requestIDs []string
	var checked []store.Source
	for _, id := range ids {
		s, err := e.store.Source(t.UserID, id)
		if err != nil {
			failures = append(failures, fmt.Sprintf("上游 %d 不存在", id))
			continue
		}
		if store.IsDemoSource(s) {
			skipped = append(skipped, s.Name+": "+store.DemoSourceMessage)
			_ = e.store.UpdateSourceCheck(t.UserID, s.ID, connectors.MonitorDemo, nil, "")
			_ = e.store.AddSourceHealth(t.UserID, s.ID, "skipped", store.DemoSourceMessage, nil)
			continue
		}
		secret, err := e.vault.Decrypt(s.Secret)
		if err != nil {
			message := "上游 Key 解密失败，无法执行倍率检查"
			failures = append(failures, s.Name+": "+message)
			_ = e.store.UpdateSourceCheck(t.UserID, s.ID, connectors.MonitorFailed, nil, message)
			_ = e.store.AddSourceHealth(t.UserID, s.ID, "failed", message, nil)
			continue
		}
		if !force && s.LastCheckedAt != nil && s.Platform == "newapi" && s.MonitorState != connectors.MonitorFailed && time.Since(*s.LastCheckedAt) < probeInterval {
			if s.LastRate != nil {
				rates = append(rates, *s.LastRate)
				checked = append(checked, s)
			}
			continue
		}
		cap, err := e.client.Detect(ctx, s.BaseURL, secret, s.ProbeModel)
		if err != nil {
			message := strings.TrimSpace(cap.Message)
			if message == "" {
				message = err.Error()
			}
			failure := s.Name + ": " + message
			if s.LastRate != nil {
				rates = append(rates, *s.LastRate)
				failure += "；沿用最近成功倍率 " + formatRate(*s.LastRate)
			}
			failures = append(failures, failure)
			_ = e.store.UpdateSourceCheck(t.UserID, s.ID, connectors.MonitorFailed, nil, message)
			_ = e.store.AddSourceHealth(t.UserID, s.ID, "failed", message, nil)
			continue
		}
		_ = e.store.UpdateSourceCheck(t.UserID, s.ID, cap.MonitorState, cap.Rate, "")
		if len(cap.Models) > 0 {
			_ = e.store.UpdateSourceModels(t.UserID, s.ID, cap.Models)
		}
		_ = e.store.AddSourceHealth(t.UserID, s.ID, "healthy", "连接正常，倍率无变化", cap.Rate)
		checked = append(checked, s)
		if cap.Rate != nil {
			rates = append(rates, *cap.Rate)
		}
		if cap.RequestID != "" {
			requestIDs = append(requestIDs, cap.RequestID)
		}
		e.recordImages(t, s, cap.ImageObservations)
	}
	if len(rates) == 0 {
		if len(skipped) > 0 && len(failures) == 0 {
			msg := strings.Join(skipped, "；")
			_ = e.store.UpdateTaskResult(t.ID, nil, t.LastTargetRate, "skipped", msg)
			return nil
		}
		msg := "没有可用的上游倍率"
		if len(failures) > 0 {
			msg += ": " + strings.Join(failures, "；")
		}
		_ = e.store.UpdateTaskResult(t.ID, nil, t.LastTargetRate, "failed", msg)
		e.event(t, "error", "probe_failed", "倍率探测失败", msg, "", t.LastTargetRate, nil)
		return errors.New(msg)
	}
	sort.Float64s(rates)
	upstream := rates[len(rates)-1]
	billingBase := upstream
	minimumApplied := t.MinUpstreamRate > 0 && upstream < t.MinUpstreamRate
	if minimumApplied {
		billingBase = t.MinUpstreamRate
	}
	target, valid := targetRate(upstream, t.MinUpstreamRate, t.Adjustment)
	if !valid {
		msg := fmt.Sprintf("计算结果 %s 小于等于 0，已拒绝写入", formatRate(target))
		_ = e.store.UpdateTaskResult(t.ID, &upstream, t.LastTargetRate, "blocked", msg)
		e.event(t, "warning", "invalid_rate", "倍率未写入", msg, strings.Join(requestIDs, ","), t.LastTargetRate, &target)
		return nil
	}
	if t.LastTargetRate != nil && math.Abs(*t.LastTargetRate-target) < 1e-9 {
		_ = e.store.UpdateTaskResult(t.ID, &upstream, &target, "ok", strings.Join(failures, "；"))
		if len(failures) > 0 {
			e.event(t, "warning", "partial_probe", "部分上游未参与聚合", "按有效上游最高倍率同步；"+strings.Join(failures, "；"), strings.Join(requestIDs, ","), &target, &target)
		}
		return nil
	}
	site, err := e.store.Site(t.UserID, t.SiteID)
	if err != nil {
		return e.failWrite(t, upstream, fmt.Sprintf("目标站点（ID %d）不存在", t.SiteID), checked)
	}
	group, err := e.store.Group(t.UserID, t.GroupID)
	if err != nil {
		return e.failWrite(t, upstream, fmt.Sprintf("站点「%s」的目标分组（ID %d）已不存在，不会自动重建", site.Name, t.GroupID), checked)
	}
	if group.Status == "deleted" {
		return e.failWrite(t, upstream, fmt.Sprintf("站点「%s」的分组「%s」已被删除，不会自动重建", site.Name, group.Name), checked)
	}
	admin, err := e.vault.Decrypt(site.AdminSecret)
	if err != nil {
		return e.failWrite(t, upstream, fmt.Sprintf("目标站点「%s」的管理员凭证解密失败", site.Name), checked)
	}
	managed := connectors.ManagedSite{BaseURL: site.BaseURL, Platform: site.Platform, Auth: connectors.Auth{Secret: admin, Header: site.AdminHeader, UserID: site.AdminUserID}}
	before := group.Rate
	changePct := 0.0
	if math.Abs(before) > 1e-9 {
		changePct = math.Abs((target-before)/before) * 100
	}
	if t.ShadowMode {
		msg := fmt.Sprintf("检测到倍率将从 %s 调整为 %s；当前为观察模式，未写入目标站点", formatRate(before), formatRate(target))
		_ = e.store.UpdateTaskResult(t.ID, &upstream, &before, "observed", msg)
		for _, src := range checked {
			_ = e.store.AddSourceHealth(t.UserID, src.ID, "changed", msg, &upstream)
		}
		e.event(t, "warning", "rate_observed", "发现倍率变化", msg, strings.Join(requestIDs, ","), &before, &target)
		return nil
	}
	if t.LargeChangePct > 0 && changePct >= t.LargeChangePct {
		e.event(t, "warning", "large_change", "检测到较大倍率变化", fmt.Sprintf("目标分组 %s 将变化 %.1f%%，已按任务设置立即同步", group.Name, changePct), strings.Join(requestIDs, ","), &before, &target)
	}
	if err = e.client.SetGroupRate(ctx, managed, group, target); err != nil {
		message := fmt.Sprintf("站点「%s」/ 分组「%s」写入失败：%v", site.Name, group.Name, err)
		return e.failWrite(t, upstream, message, checked)
	}
	// 写入后重新读取目标站点，确认真实值并刷新本地关系树。
	verified := target
	if groups, accounts, importErr := e.client.Import(ctx, managed); importErr == nil {
		_ = e.store.ReplaceInventory(t.UserID, site.ID, groups, accounts)
		if refreshed, groupErr := e.store.Group(t.UserID, t.GroupID); groupErr == nil {
			verified = refreshed.Rate
		}
	}
	if math.Abs(verified-target) > 1e-9 {
		message := fmt.Sprintf("站点「%s」/ 分组「%s」写入后校验不一致：期望 %s，实际 %s", site.Name, group.Name, formatRate(target), formatRate(verified))
		return e.failWrite(t, upstream, message, checked)
	}
	_ = e.store.UpdateGroupRate(t.UserID, t.GroupID, verified)
	_ = e.store.UpdateTaskResult(t.ID, &upstream, &target, "ok", strings.Join(failures, "；"))
	for _, src := range checked {
		message := fmt.Sprintf("已推送到站点「%s」的分组「%s」：%s → %s", site.Name, group.Name, formatRate(before), formatRate(verified))
		_ = e.store.AddSourceHealth(t.UserID, src.ID, "synced", message, &upstream)
	}
	detail := fmt.Sprintf("最高有效上游倍率 %s %s = 目标倍率 %s", formatRate(upstream), formatSignedRate(t.Adjustment), formatRate(target))
	if minimumApplied {
		detail = fmt.Sprintf("最高有效上游倍率 %s 低于最低倍率 %s，按 %s 为基准 %s = 目标倍率 %s", formatRate(upstream), formatRate(t.MinUpstreamRate), formatRate(billingBase), formatSignedRate(t.Adjustment), formatRate(target))
	}
	if len(failures) > 0 {
		detail += "；忽略: " + strings.Join(failures, "；")
	}
	e.event(t, "success", "rate_changed", "目标倍率已同步", detail, strings.Join(requestIDs, ","), &before, &target)
	return nil
}

func (e *Engine) recordImages(task store.Task, source store.Source, observations []connectors.ImageObservation) {
	for _, observation := range observations {
		v := store.ImageObservation{UserID: task.UserID, SourceID: source.ID, Model: observation.Model, Size: observation.Size, Quality: observation.Quality, Count: observation.Count, GroupRate: observation.GroupRate, UnitPrice: observation.UnitPrice, ActualCost: observation.ActualCost, RequestID: observation.RequestID, ObservedAt: observation.ObservedAt}
		inserted, addErr := e.store.AddImageObservation(v)
		if addErr != nil || !inserted {
			continue
		}
		detail := fmt.Sprintf("%s · %s · %s · %s ×%d，实际扣费 %.6g；已被动观测，目标计费维度不能精确映射时不会自动写入。", source.Name, v.Model, v.Size, v.Quality, v.Count, v.ActualCost)
		e.event(task, "info", "image_observed", "观测到真实生图价格", detail, v.RequestID, nil, nil)
	}
}

func targetRate(upstream, minimumUpstreamRate, adjustment float64) (float64, bool) {
	const precision = 10000.0
	base := upstream
	if minimumUpstreamRate > base {
		base = minimumUpstreamRate
	}
	raw := base + adjustment
	if math.IsNaN(raw) || math.IsInf(raw, 0) {
		return raw, false
	}
	value := math.Round(raw*precision) / precision
	return value, value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
func formatRate(value float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", value), "0"), ".")
}
func formatSignedRate(value float64) string {
	if value >= 0 {
		return "+" + formatRate(value)
	}
	return formatRate(value)
}
func (e *Engine) failWrite(t store.Task, upstream float64, msg string, checked []store.Source) error {
	for _, src := range checked {
		_ = e.store.AddSourceHealth(t.UserID, src.ID, "write_failed", msg, &upstream)
	}
	_ = e.store.UpdateTaskResult(t.ID, &upstream, t.LastTargetRate, "failed", msg)
	e.event(t, "error", "write_failed", "目标倍率写入失败", msg, "", t.LastTargetRate, nil)
	return errors.New(msg)
}
func (e *Engine) event(t store.Task, level, kind, title, detail, requestID string, before, after *float64) {
	id := t.ID
	var siteName, sourceName, groupName string
	var sourceID *int64
	if site, err := e.store.Site(t.UserID, t.SiteID); err == nil {
		siteName = site.Name
	}
	if group, err := e.store.Group(t.UserID, t.GroupID); err == nil {
		groupName = group.Name
	}
	if source, err := e.store.Source(t.UserID, t.SourceID); err == nil {
		sourceName = source.Name
		sid := source.ID
		sourceID = &sid
	}
	siteID, groupID := t.SiteID, t.GroupID
	v, err := e.store.AddEvent(store.Event{UserID: t.UserID, TaskID: &id, Level: level, Kind: kind, Title: title, Detail: detail, SiteID: &siteID, SourceID: sourceID, GroupID: &groupID, SiteName: siteName, SourceName: sourceName, GroupName: groupName, Reason: detail, RequestID: requestID, BeforeRate: before, AfterRate: after})
	if err == nil {
		e.hub.Publish(v)
	}
}

func (e *Engine) modelLoop(ctx context.Context) {
	for {
		interval := e.cfg.ModelInterval
		if settings, err := e.store.SystemSettings(); err == nil && settings.ModelCheckMinutes >= 1 {
			interval = time.Duration(settings.ModelCheckMinutes) * time.Minute
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			e.checkModels(ctx)
		}
	}
}
func (e *Engine) checkModels(ctx context.Context) {
	tasks, _ := e.store.EnabledTasks()
	seen := map[int64]bool{}
	for _, t := range tasks {
		ids, _ := e.store.TaskSourceIDs(t.UserID, t.ID)
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			s, err := e.store.Source(t.UserID, id)
			if err != nil {
				continue
			}
			key, err := e.vault.Decrypt(s.Secret)
			if err != nil {
				continue
			}
			models, err := e.client.Models(ctx, s.BaseURL, key)
			if err != nil {
				continue
			}
			added, removed := diff(s.Models, models)
			if len(added)+len(removed) > 0 {
				detail := fmt.Sprintf("新增: %s；移除: %s。系统不会自动修改目标模型，请人工确认。", strings.Join(added, ", "), strings.Join(removed, ", "))
				e.event(t, "warning", "model_diff", "上游模型列表发生变化", detail, "", nil, nil)
				// 以本次清单作为下一轮比较基线，避免重复发送相同差异。
				_ = e.store.UpdateSourceModels(t.UserID, s.ID, models)
			}
		}
	}
}
func diff(old, next []string) ([]string, []string) {
	a, b := map[string]bool{}, map[string]bool{}
	for _, v := range old {
		a[v] = true
	}
	for _, v := range next {
		b[v] = true
	}
	var added, removed []string
	for v := range b {
		if !a[v] {
			added = append(added, v)
		}
	}
	for v := range a {
		if !b[v] {
			removed = append(removed, v)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

var _ = sql.ErrNoRows
