package syncer

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
	"time"
)

func (e *Engine) emailLoop(ctx context.Context) {
	for {
		interval := e.cfg.EmailInterval
		if settings, err := e.store.SystemSettings(); err == nil && settings.EmailMinutes >= 1 {
			interval = time.Duration(settings.EmailMinutes) * time.Minute
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			e.sendDigests()
		}
	}
}
func (e *Engine) sendDigests() {
	settings, settingsErr := e.store.SystemSettings()
	host, port, user, password, from := e.cfg.SMTPHost, e.cfg.SMTPPort, e.cfg.SMTPUser, e.cfg.SMTPPassword, e.cfg.SMTPFrom
	if settingsErr == nil && settings.SMTPHost != "" {
		host, port, user, from = settings.SMTPHost, settings.SMTPPort, settings.SMTPUser, settings.SMTPFrom
		if settings.SMTPPassword != "" {
			password, _ = e.vault.Decrypt(settings.SMTPPassword)
		}
	}
	if host == "" || from == "" {
		return
	}
	users, err := e.store.EmailUsers()
	if err != nil {
		return
	}
	for _, u := range users {
		events, err := e.store.PendingEmailEvents(u.ID, u.NotifyKinds)
		if err != nil || len(events) == 0 {
			continue
		}
		var body strings.Builder
		body.WriteString("倍率同步平台变更摘要\r\n\r\n")
		ids := make([]int64, 0, len(events))
		for _, v := range events {
			ids = append(ids, v.ID)
			fmt.Fprintf(&body, "[%s] %s\r\n站点：%s｜上游：%s｜分组：%s\r\n%s\r\n\r\n", v.CreatedAt.Format("01-02 15:04"), v.Title, v.SiteName, v.SourceName, v.GroupName, v.Detail)
		}
		msg := []byte("To: " + u.NotifyEmail + "\r\nSubject: =?UTF-8?B?5YCN546H5ZCM5q2l5Y+Y5pu05pGY6KaB?=\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body.String())
		var auth smtp.Auth
		if user != "" {
			auth = smtp.PlainAuth("", user, password, host)
		}
		if smtp.SendMail(fmt.Sprintf("%s:%d", host, port), auth, from, []string{u.NotifyEmail}, msg) == nil {
			_ = e.store.MarkEventsEmailed(ids)
		}
	}
}
