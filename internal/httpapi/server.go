package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"net/http"
	"net/smtp"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"ratewatch/internal/config"
	"ratewatch/internal/connectors"
	"ratewatch/internal/security"
	"ratewatch/internal/store"
	"ratewatch/internal/syncer"
	"ratewatch/internal/updater"
)

type Server struct {
	cfg     config.Config
	store   *store.Store
	vault   *security.Vault
	client  *connectors.Client
	engine  *syncer.Engine
	hub     *syncer.Hub
	update  *updater.Manager
	restart func()
	mux     *http.ServeMux
}
type contextKey string

const userKey contextKey = "userID"

func New(cfg config.Config, st *store.Store, v *security.Vault, c *connectors.Client, e *syncer.Engine, h *syncer.Hub) *Server {
	s := &Server{cfg: cfg, store: st, vault: v, client: c, engine: e, hub: h, update: updater.New(), mux: http.NewServeMux()}
	s.routes()
	return s
}
func (s *Server) Handler() http.Handler      { return s.securityHeaders(s.recover(s.mux)) }
func (s *Server) SetRestart(callback func()) { s.restart = callback }
func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]any{"ok": true}) })
	s.mux.HandleFunc("GET /api/public-config", s.publicConfig)
	s.mux.HandleFunc("POST /api/auth/register", s.register)
	s.mux.HandleFunc("POST /api/auth/login", s.login)
	s.mux.HandleFunc("POST /api/auth/forgot", s.forgotPassword)
	s.mux.HandleFunc("POST /api/auth/reset", s.resetPassword)
	s.mux.HandleFunc("POST /api/admin/auth/login", s.adminLogin)
	s.mux.Handle("GET /api/me", s.auth(http.HandlerFunc(s.me)))
	s.mux.Handle("PUT /api/me/profile", s.auth(http.HandlerFunc(s.updateProfile)))
	s.mux.Handle("PUT /api/me/password", s.auth(http.HandlerFunc(s.updatePassword)))
	s.mux.Handle("PUT /api/me/onboarding", s.auth(http.HandlerFunc(s.updateOnboarding)))
	s.mux.Handle("PUT /api/me/notifications", s.auth(http.HandlerFunc(s.updateNotifications)))
	s.mux.Handle("GET /api/dashboard", s.auth(http.HandlerFunc(s.dashboard)))
	s.mux.Handle("GET /api/sites", s.auth(http.HandlerFunc(s.listSites)))
	s.mux.Handle("POST /api/sites", s.auth(http.HandlerFunc(s.createSite)))
	s.mux.Handle("PUT /api/sites/{id}", s.auth(http.HandlerFunc(s.updateSite)))
	s.mux.Handle("DELETE /api/sites/{id}", s.auth(http.HandlerFunc(s.deleteSite)))
	s.mux.Handle("POST /api/sites/{id}/import", s.auth(http.HandlerFunc(s.importSite)))
	s.mux.Handle("GET /api/sites/{id}/inventory", s.auth(http.HandlerFunc(s.inventory)))
	s.mux.Handle("POST /api/sources/detect", s.auth(http.HandlerFunc(s.detectSource)))
	s.mux.Handle("GET /api/sources", s.auth(http.HandlerFunc(s.listSources)))
	s.mux.Handle("POST /api/sources", s.auth(http.HandlerFunc(s.createSource)))
	s.mux.Handle("PUT /api/sources/{id}", s.auth(http.HandlerFunc(s.updateSource)))
	s.mux.Handle("POST /api/sources/{id}/sync", s.auth(http.HandlerFunc(s.syncSource)))
	s.mux.Handle("DELETE /api/sources/{id}", s.auth(http.HandlerFunc(s.deleteSource)))
	s.mux.Handle("GET /api/tasks", s.auth(http.HandlerFunc(s.listTasks)))
	s.mux.Handle("POST /api/tasks", s.auth(http.HandlerFunc(s.createTask)))
	s.mux.Handle("PUT /api/tasks/{id}", s.auth(http.HandlerFunc(s.updateTask)))
	s.mux.Handle("PUT /api/tasks/{id}/enabled", s.auth(http.HandlerFunc(s.toggleTask)))
	s.mux.Handle("POST /api/tasks/{id}/run", s.auth(http.HandlerFunc(s.runTask)))
	s.mux.Handle("DELETE /api/tasks/{id}", s.auth(http.HandlerFunc(s.deleteTask)))
	s.mux.Handle("GET /api/events", s.auth(http.HandlerFunc(s.events)))
	s.mux.Handle("GET /api/events/stream", s.auth(http.HandlerFunc(s.stream)))
	s.mux.Handle("POST /api/image-observations", s.auth(http.HandlerFunc(s.imageObservation)))
	s.mux.Handle("GET /api/admin/overview", s.auth(s.admin(http.HandlerFunc(s.adminOverview))))
	s.mux.Handle("GET /api/admin/me", s.auth(s.admin(http.HandlerFunc(s.me))))
	s.mux.Handle("GET /api/admin/users", s.auth(s.admin(http.HandlerFunc(s.adminUsers))))
	s.mux.Handle("POST /api/admin/users", s.auth(s.admin(http.HandlerFunc(s.adminCreateUser))))
	s.mux.Handle("PUT /api/admin/users/{id}", s.auth(s.admin(http.HandlerFunc(s.adminUpdateUser))))
	s.mux.Handle("DELETE /api/admin/users/{id}", s.auth(s.admin(http.HandlerFunc(s.adminDeleteUser))))
	s.mux.Handle("POST /api/admin/demo-data", s.auth(s.admin(http.HandlerFunc(s.adminDemoData))))
	s.mux.Handle("GET /api/admin/settings", s.auth(s.admin(http.HandlerFunc(s.adminSettings))))
	s.mux.Handle("PUT /api/admin/settings", s.auth(s.admin(http.HandlerFunc(s.updateAdminSettings))))
	s.mux.Handle("GET /api/admin/update", s.auth(s.admin(http.HandlerFunc(s.adminUpdateStatus))))
	s.mux.Handle("POST /api/admin/update", s.auth(s.admin(http.HandlerFunc(s.adminInstallUpdate))))
	s.mux.HandleFunc("/", s.frontend)
}

func (s *Server) publicConfig(w http.ResponseWriter, _ *http.Request) {
	v, err := s.store.SystemSettings()
	if err != nil {
		problem(w, 500, "暂时无法读取网站设置")
		return
	}
	write(w, 200, map[string]any{"site_name": v.SiteName, "admin_path": v.AdminPath, "registration_open": v.RegistrationOpen})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.store.SystemSettings()
	if !settings.RegistrationOpen {
		problem(w, http.StatusForbidden, "当前暂未开放注册")
		return
	}
	var in struct{ Email, Password string }
	if !decode(w, r, &in) {
		return
	}
	if !strings.Contains(in.Email, "@") {
		problem(w, 400, "请输入有效邮箱")
		return
	}
	hash, e := security.HashPassword(in.Password)
	if e != nil {
		problem(w, 400, e.Error())
		return
	}
	u, e := s.store.CreateUser(in.Email, hash)
	if e != nil {
		problem(w, 409, "邮箱已注册")
		return
	}
	s.session(w, u)
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password string }
	if !decode(w, r, &in) {
		return
	}
	u, e := s.store.UserByEmail(in.Email)
	if e != nil || !security.CheckPassword(u.PasswordHash, in.Password) {
		problem(w, 401, "账号或密码错误")
		return
	}
	if u.Status != "active" {
		problem(w, http.StatusForbidden, "账户已停用，请联系管理员")
		return
	}
	if u.Role == "admin" {
		problem(w, http.StatusForbidden, "管理员请从 /admin 进入管理后台")
		return
	}
	s.session(w, u)
}

func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password string }
	if !decode(w, r, &in) {
		return
	}
	u, err := s.store.UserByEmail(in.Email)
	if err != nil || u.Role != "admin" || u.Status != "active" || !security.CheckPassword(u.PasswordHash, in.Password) {
		problem(w, http.StatusUnauthorized, "管理员账号或密码错误")
		return
	}
	s.session(w, u)
}

func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &in) {
		return
	}
	// 无论账号是否存在都返回相同结果，避免泄露注册信息。
	u, err := s.store.UserByEmail(in.Email)
	if err == nil && strings.Contains(u.Email, "@") {
		raw := make([]byte, 32)
		if _, err = rand.Read(raw); err == nil {
			token := hex.EncodeToString(raw)
			sum := sha256.Sum256([]byte(token))
			if s.store.CreatePasswordReset(u.ID, hex.EncodeToString(sum[:]), time.Now().Add(30*time.Minute)) == nil {
				s.sendResetMail(u.Email, token)
			}
		}
	}
	write(w, 200, map[string]any{"message": "如果账号存在，重置邮件会在几分钟内送达"})
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Password string }
	if !decode(w, r, &in) {
		return
	}
	hash, err := security.HashPassword(in.Password)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(in.Token)))
	if err = s.store.ConsumePasswordReset(hex.EncodeToString(sum[:]), hash); err != nil {
		problem(w, 400, "链接已失效，请重新申请")
		return
	}
	write(w, 200, map[string]any{"message": "密码已更新，请重新登录"})
}

func (s *Server) sendResetMail(to, token string) {
	v, err := s.store.SystemSettings()
	if err != nil || v.SMTPHost == "" || v.SMTPFrom == "" {
		return
	}
	password := ""
	if v.SMTPPassword != "" {
		password, _ = s.vault.Decrypt(v.SMTPPassword)
	}
	base := strings.TrimRight(v.PublicURL, "/")
	if base == "" {
		base = strings.TrimRight(s.cfg.PublicURL, "/")
	}
	link := base + "/reset-password?token=" + token
	body := "您申请了密码重置。请在 30 分钟内打开以下链接：\r\n\r\n" + link + "\r\n\r\n如果不是您本人操作，请忽略此邮件。"
	msg := []byte("To: " + to + "\r\nSubject: =?UTF-8?B?UmF0ZVdhdGNoIOWvhueggeivt+mHjQ==?=\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body)
	var auth smtp.Auth
	if v.SMTPUser != "" {
		auth = smtp.PlainAuth("", v.SMTPUser, password, v.SMTPHost)
	}
	_ = smtp.SendMail(fmt.Sprintf("%s:%d", v.SMTPHost, v.SMTPPort), auth, v.SMTPFrom, []string{to}, msg)
}
func (s *Server) session(w http.ResponseWriter, u store.User) {
	token, e := security.SignSession(s.cfg.SessionSecret, u.ID, 30*24*time.Hour)
	if e != nil {
		problem(w, 500, "无法创建会话")
		return
	}
	write(w, 200, map[string]any{"token": token, "user": u})
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, e := s.store.UserByID(uid(r))
	if e != nil {
		problem(w, 404, "用户不存在")
		return
	}
	write(w, 200, u)
}
func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !strings.Contains(in.Email, "@") {
		problem(w, 400, "请输入有效邮箱")
		return
	}
	if err := s.store.UpdateUserEmail(uid(r), in.Email); err != nil {
		problem(w, 409, "邮箱已被使用")
		return
	}
	s.me(w, r)
}
func (s *Server) updatePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.store.UserByID(uid(r))
	if err != nil || !security.CheckPassword(u.PasswordHash, in.CurrentPassword) {
		problem(w, 400, "当前密码不正确")
		return
	}
	hash, err := security.HashPassword(in.NewPassword)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	if err = s.store.UpdateUserPassword(u.ID, hash); err != nil {
		problem(w, 500, "密码更新失败")
		return
	}
	write(w, 200, map[string]any{"message": "密码已更新"})
}
func (s *Server) updateOnboarding(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Completed bool `json:"completed"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.store.SetUserOnboarded(uid(r), in.Completed); err != nil {
		problem(w, 500, "保存失败")
		return
	}
	s.me(w, r)
}

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.AdminOverview()
	if err != nil {
		problem(w, 500, "监控数据暂时不可用")
		return
	}
	write(w, 200, v)
}
func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	v, err := s.store.AdminUsers(page, pageSize, r.URL.Query().Get("query"))
	if err != nil {
		problem(w, 500, "用户列表暂时不可用")
		return
	}
	if v.Items == nil {
		v.Items = []store.AdminUser{}
	}
	write(w, 200, v)
}
func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password, Role string }
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Email) == "" || (in.Role != "admin" && !strings.Contains(in.Email, "@")) {
		problem(w, 400, "请输入有效邮箱")
		return
	}
	hash, err := security.HashPassword(in.Password)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	v, err := s.store.AdminCreateUser(in.Email, hash, in.Role)
	if err != nil {
		problem(w, 409, "用户已存在或资料无效")
		return
	}
	write(w, http.StatusCreated, v)
}
func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password, Role, Status string }
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Email) == "" || (in.Role != "admin" && !strings.Contains(in.Email, "@")) {
		problem(w, 400, "请输入有效邮箱")
		return
	}
	hash := ""
	var err error
	if strings.TrimSpace(in.Password) != "" {
		hash, err = security.HashPassword(in.Password)
		if err != nil {
			problem(w, 400, err.Error())
			return
		}
	}
	v, err := s.store.AdminUpdateUser(uid(r), pathID(r), in.Email, in.Role, in.Status, hash)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	write(w, 200, v)
}
func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	if err := s.store.AdminDeleteUser(uid(r), pathID(r)); err != nil {
		problem(w, 400, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) adminDemoData(w http.ResponseWriter, r *http.Request) {
	hash, err := security.HashPassword("demo123456")
	if err != nil {
		problem(w, 500, "演示数据创建失败")
		return
	}
	id, err := s.store.SeedDemoData(hash)
	if err != nil {
		problem(w, 500, "演示数据创建失败: "+err.Error())
		return
	}
	write(w, 201, map[string]any{"user_id": id, "email": "demo@ratewatch.local", "message": "演示数据已准备完成"})
}
func (s *Server) adminSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.SystemSettings()
	if err != nil {
		problem(w, 500, "设置暂时不可用")
		return
	}
	v.SMTPPassword = ""
	write(w, 200, v)
}
func (s *Server) updateAdminSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SiteName          string `json:"site_name"`
		AdminPath         string `json:"admin_path"`
		RegistrationOpen  bool   `json:"registration_open"`
		PublicURL         string `json:"public_url"`
		SMTPHost          string `json:"smtp_host"`
		SMTPPort          int    `json:"smtp_port"`
		SMTPUser          string `json:"smtp_user"`
		SMTPPassword      string `json:"smtp_password"`
		SMTPPasswordSet   bool   `json:"smtp_password_set"`
		SMTPFrom          string `json:"smtp_from"`
		PollSeconds       int    `json:"poll_seconds"`
		ProbeSeconds      int    `json:"probe_seconds"`
		ModelCheckMinutes int    `json:"model_check_minutes"`
		EmailMinutes      int    `json:"email_minutes"`
	}
	if !decode(w, r, &in) {
		return
	}
	current, _ := s.store.SystemSettings()
	password := current.SMTPPassword
	keep := strings.TrimSpace(in.SMTPPassword) == ""
	if !keep {
		var err error
		password, err = s.vault.Encrypt(in.SMTPPassword)
		if err != nil {
			problem(w, 500, "邮件密码保存失败")
			return
		}
	}
	v := store.SystemSettings{SiteName: in.SiteName, AdminPath: in.AdminPath, RegistrationOpen: in.RegistrationOpen, PublicURL: in.PublicURL, SMTPHost: in.SMTPHost, SMTPPort: in.SMTPPort, SMTPUser: in.SMTPUser, SMTPPassword: password, SMTPFrom: in.SMTPFrom, PollSeconds: in.PollSeconds, ProbeSeconds: in.ProbeSeconds, ModelCheckMinutes: in.ModelCheckMinutes, EmailMinutes: in.EmailMinutes}
	if err := s.store.UpdateSystemSettings(v, keep); err != nil {
		problem(w, 400, err.Error())
		return
	}
	s.adminSettings(w, r)
}
func (s *Server) adminUpdateStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.update.Check(r.Context())
	if err != nil {
		problem(w, http.StatusBadGateway, "检查 GitHub 发行版失败: "+err.Error())
		return
	}
	if status.CanAutoUpdate && s.restart == nil {
		status.CanAutoUpdate = false
		status.Reason = "当前运行方式未启用自动重启"
	}
	write(w, http.StatusOK, status)
}
func (s *Server) adminInstallUpdate(w http.ResponseWriter, r *http.Request) {
	if s.restart == nil {
		problem(w, http.StatusConflict, "当前运行方式未启用自动重启")
		return
	}
	result, err := s.update.Install(r.Context())
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	write(w, http.StatusAccepted, result)
	go func() {
		time.Sleep(800 * time.Millisecond)
		s.restart()
	}()
}
func (s *Server) updateNotifications(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email   string   `json:"notify_email"`
		Enabled bool     `json:"email_enabled"`
		Kinds   []string `json:"notify_kinds"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Enabled && !strings.Contains(in.Email, "@") {
		problem(w, 400, "请输入有效收件邮箱")
		return
	}
	allowed := map[string]bool{"rate_changed": true, "write_failed": true, "probe_failed": true, "partial_probe": true, "invalid_rate": true, "model_diff": true, "image_observed": true}
	for _, k := range in.Kinds {
		if !allowed[k] {
			problem(w, 400, "包含无效通知类型")
			return
		}
	}
	if e := s.store.UpdateUserNotifications(uid(r), in.Email, in.Enabled, in.Kinds); e != nil {
		problem(w, 500, e.Error())
		return
	}
	s.me(w, r)
}
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Dashboard(uid(r))
	if e != nil {
		problem(w, 500, e.Error())
		return
	}
	write(w, 200, v)
}

func (s *Server) listSites(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Sites(uid(r))
	if e != nil {
		problem(w, 500, e.Error())
		return
	}
	write(w, 200, nonNil(v))
}

type siteInput struct {
	Name        string `json:"name"`
	BaseURL     string `json:"base_url"`
	Platform    string `json:"platform"`
	AdminKey    string `json:"admin_key"`
	AdminUserID string `json:"admin_user_id"`
	AdminHeader string `json:"admin_header"`
}

func normalizeSiteInput(in *siteInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if in.Platform != "newapi" && in.Platform != "sub2api" {
		return errors.New("站点类型必须是 newapi 或 sub2api")
	}
	if in.Name == "" || in.BaseURL == "" {
		return errors.New("名称和域名必填")
	}
	if in.Platform == "sub2api" {
		in.AdminUserID = ""
		in.AdminHeader = "x-api-key"
	} else {
		if in.AdminUserID == "" {
			in.AdminUserID = "1"
		}
		if in.AdminHeader == "" {
			in.AdminHeader = "Authorization"
		}
	}
	return nil
}

func (s *Server) createSite(w http.ResponseWriter, r *http.Request) {
	var in siteInput
	if !decode(w, r, &in) {
		return
	}
	if err := normalizeSiteInput(&in); err != nil {
		problem(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(in.AdminKey) == "" {
		problem(w, 400, "名称、域名和管理员 Key 必填")
		return
	}
	enc, e := s.vault.Encrypt(in.AdminKey)
	if e != nil {
		problem(w, 500, e.Error())
		return
	}
	site, e := s.store.CreateSite(store.Site{UserID: uid(r), Name: in.Name, BaseURL: in.BaseURL, Platform: in.Platform, AdminSecret: enc, AdminUserID: in.AdminUserID, AdminHeader: in.AdminHeader})
	if e != nil {
		problem(w, 409, "站点已存在或配置无效: "+e.Error())
		return
	}
	if e = s.doImport(r.Context(), site); e != nil {
		_ = s.store.SetSiteStatus(uid(r), site.ID, "error", e.Error(), false)
		site, _ = s.store.Site(uid(r), site.ID)
		site.AdminSecret = ""
		write(w, 201, site)
		return
	}
	site, _ = s.store.Site(uid(r), site.ID)
	site.AdminSecret = ""
	write(w, 201, site)
}
func (s *Server) updateSite(w http.ResponseWriter, r *http.Request) {
	userID, siteID := uid(r), pathID(r)
	existing, err := s.store.Site(userID, siteID)
	if err != nil {
		problem(w, http.StatusNotFound, "站点不存在")
		return
	}
	var in siteInput
	if !decode(w, r, &in) {
		return
	}
	if err = normalizeSiteInput(&in); err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	secret := existing.AdminSecret
	adminKey, err := s.vault.Decrypt(secret)
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "管理员 Key 解密失败，请重新填写")
		return
	}
	if strings.TrimSpace(in.AdminKey) != "" {
		adminKey = in.AdminKey
		secret, err = s.vault.Encrypt(adminKey)
		if err != nil {
			problem(w, http.StatusInternalServerError, "管理员 Key 保存失败")
			return
		}
	}
	managed := connectors.ManagedSite{BaseURL: in.BaseURL, Platform: in.Platform, Auth: connectors.Auth{Secret: adminKey, Header: in.AdminHeader, UserID: in.AdminUserID}}
	groups, accounts, err := s.client.Import(r.Context(), managed)
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "新设置连接失败，原配置未修改: "+err.Error())
		return
	}
	updated, err := s.store.UpdateSite(store.Site{ID: siteID, UserID: userID, Name: in.Name, BaseURL: in.BaseURL, Platform: in.Platform, AdminSecret: secret, AdminUserID: in.AdminUserID, AdminHeader: in.AdminHeader})
	if err != nil {
		problem(w, http.StatusConflict, "站点更新失败: "+err.Error())
		return
	}
	if err = s.store.ReplaceInventory(userID, siteID, groups, accounts); err != nil {
		_ = s.store.SetSiteStatus(userID, siteID, "error", err.Error(), false)
		problem(w, http.StatusInternalServerError, "站点已保存，但关系树更新失败: "+err.Error())
		return
	}
	_ = s.store.SetSiteStatus(userID, siteID, "ready", "", true)
	updated, _ = s.store.Site(userID, siteID)
	updated.AdminSecret = ""
	write(w, http.StatusOK, updated)
}
func (s *Server) deleteSite(w http.ResponseWriter, r *http.Request) {
	if e := s.store.DeleteSite(uid(r), pathID(r)); e != nil {
		problem(w, 404, "站点不存在")
		return
	}
	w.WriteHeader(204)
}
func (s *Server) importSite(w http.ResponseWriter, r *http.Request) {
	site, e := s.store.Site(uid(r), pathID(r))
	if e != nil {
		problem(w, 404, "站点不存在")
		return
	}
	if e = s.doImport(r.Context(), site); e != nil {
		_ = s.store.SetSiteStatus(uid(r), site.ID, "error", e.Error(), false)
		problem(w, 422, e.Error())
		return
	}
	groups, _ := s.store.Inventory(uid(r), site.ID)
	write(w, 200, nonNil(groups))
}
func (s *Server) doImport(ctx context.Context, site store.Site) error {
	key, e := s.vault.Decrypt(site.AdminSecret)
	if e != nil {
		return e
	}
	groups, accounts, e := s.client.Import(ctx, connectors.ManagedSite{BaseURL: site.BaseURL, Platform: site.Platform, Auth: connectors.Auth{Secret: key, Header: site.AdminHeader, UserID: site.AdminUserID}})
	if e != nil {
		return e
	}
	if e = s.store.ReplaceInventory(site.UserID, site.ID, groups, accounts); e != nil {
		return e
	}
	return s.store.SetSiteStatus(site.UserID, site.ID, "ready", "", true)
}
func (s *Server) inventory(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Inventory(uid(r), pathID(r))
	if e != nil {
		problem(w, 500, e.Error())
		return
	}
	write(w, 200, nonNil(v))
}

type sourceInput struct {
	Name            string              `json:"name"`
	BaseURL         string              `json:"base_url"`
	Key             string              `json:"key"`
	ProbeModel      string              `json:"probe_model"`
	SiteID          int64               `json:"site_id"`
	GroupID         int64               `json:"group_id"`
	AccountID       int64               `json:"account_id"`
	CreateTarget    bool                `json:"create_target"`
	BindExisting    bool                `json:"bind_existing"`
	MinUpstreamRate float64             `json:"minimum_upstream_rate"`
	Adjustment      float64             `json:"adjustment"`
	Targets         []sourceTargetInput `json:"targets"`
}

type sourceTargetInput struct {
	SiteID          int64   `json:"site_id"`
	GroupID         int64   `json:"group_id"`
	AccountID       int64   `json:"account_id"`
	MinUpstreamRate float64 `json:"minimum_upstream_rate"`
	Adjustment      float64 `json:"adjustment"`
}

func (in sourceInput) targetInputs() []sourceTargetInput {
	if len(in.Targets) > 0 {
		return in.Targets
	}
	if in.SiteID > 0 && in.GroupID > 0 {
		return []sourceTargetInput{{SiteID: in.SiteID, GroupID: in.GroupID, AccountID: in.AccountID, MinUpstreamRate: in.MinUpstreamRate, Adjustment: in.Adjustment}}
	}
	return nil
}

func (s *Server) detectSourceCapability(ctx context.Context, userID int64, in sourceInput) (connectors.Capability, error) {
	capability, err := s.client.Detect(ctx, in.BaseURL, in.Key, in.ProbeModel)
	targets := in.targetInputs()
	if err != nil || len(capability.Models) > 0 || !in.BindExisting || len(targets) == 0 {
		return capability, err
	}
	target := targets[0]

	groups, inventoryErr := s.store.Inventory(userID, target.SiteID)
	if inventoryErr != nil {
		return capability, err
	}
	wantedBase := strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	models := make(map[string]struct{})
	for _, group := range groups {
		if group.ID != target.GroupID {
			continue
		}
		for _, account := range group.Accounts {
			if target.AccountID > 0 && account.ID != target.AccountID {
				continue
			}
			if !strings.EqualFold(strings.TrimRight(strings.TrimSpace(account.BaseURL), "/"), wantedBase) {
				continue
			}
			for _, model := range account.Models {
				if model = strings.TrimSpace(model); model != "" {
					models[model] = struct{}{}
				}
			}
		}
	}
	if len(models) == 0 {
		return capability, err
	}
	capability.Models = make([]string, 0, len(models))
	for model := range models {
		capability.Models = append(capability.Models, model)
	}
	sort.Strings(capability.Models)
	capability.ModelsMessage = "上游模型接口暂时无法读取，已采用该账号最近一次导入的模型清单"
	return capability, err
}

func (s *Server) detectSource(w http.ResponseWriter, r *http.Request) {
	var in sourceInput
	if !decode(w, r, &in) {
		return
	}
	if s.store.SourceFingerprintExists(uid(r), security.Fingerprint(in.BaseURL, in.Key)) {
		problem(w, 409, "该上游地址和 Key 已经添加，请勿重复创建")
		return
	}
	cap, e := s.detectSourceCapability(r.Context(), uid(r), in)
	if e != nil {
		write(w, 422, cap)
		return
	}
	preview := map[string]any{"capability": cap, "name": in.Name, "base_url": strings.TrimRight(in.BaseURL, "/"), "probe_model": in.ProbeModel}
	if targets := in.targetInputs(); len(targets) > 0 {
		site, se := s.store.Site(uid(r), targets[0].SiteID)
		group, ge := s.store.Group(uid(r), targets[0].GroupID)
		if se == nil && ge == nil && group.SiteID == site.ID {
			preview["target_site"] = site.Name
			preview["target_group"] = group.Name
		}
	}
	write(w, 200, preview)
}
func (s *Server) listSources(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Sources(uid(r))
	if e != nil {
		problem(w, 500, e.Error())
		return
	}
	for i := range v {
		key, _ := s.vault.Decrypt(v[i].Secret)
		v[i].SecretMask = security.MaskSecret(key)
		v[i].Secret = ""
	}
	write(w, 200, nonNil(v))
}
func (s *Server) createSource(w http.ResponseWriter, r *http.Request) {
	var in sourceInput
	if !decode(w, r, &in) {
		return
	}
	targetInputs := []sourceTargetInput(nil)
	if in.CreateTarget || in.BindExisting {
		targetInputs = in.targetInputs()
	}
	if in.CreateTarget && len(targetInputs) == 0 {
		problem(w, 400, "至少选择一个目标分组")
		return
	}
	type resolvedTarget struct {
		input sourceTargetInput
		site  store.Site
		group store.Group
	}
	resolvedTargets := make([]resolvedTarget, 0, len(targetInputs))
	seenGroups := make(map[int64]bool, len(targetInputs))
	existingTasks, _ := s.store.Tasks(uid(r))
	for _, target := range targetInputs {
		if seenGroups[target.GroupID] {
			problem(w, 400, "同一个目标分组不能重复添加")
			return
		}
		seenGroups[target.GroupID] = true
		if target.MinUpstreamRate < 0 || math.IsNaN(target.MinUpstreamRate) || math.IsInf(target.MinUpstreamRate, 0) {
			problem(w, 400, "最低上游倍率必须大于或等于 0")
			return
		}
		site, siteErr := s.store.Site(uid(r), target.SiteID)
		group, groupErr := s.store.Group(uid(r), target.GroupID)
		if siteErr != nil || groupErr != nil || group.SiteID != site.ID || group.Status == "deleted" {
			problem(w, 400, "包含无效或已删除的目标分组")
			return
		}
		for _, task := range existingTasks {
			if task.Enabled && task.GroupID == group.ID {
				problem(w, 409, "目标分组「"+group.Name+"」已经有启用中的同步任务")
				return
			}
		}
		resolvedTargets = append(resolvedTargets, resolvedTarget{input: target, site: site, group: group})
	}
	fingerprint := security.Fingerprint(in.BaseURL, in.Key)
	if s.store.SourceFingerprintExists(uid(r), fingerprint) {
		problem(w, 409, "该上游地址和 Key 已经添加，请勿重复创建")
		return
	}
	cap, e := s.detectSourceCapability(r.Context(), uid(r), in)
	monitorable := cap.MonitorState == connectors.MonitorDirect ||
		(cap.MonitorState == connectors.MonitorNewAPIProbe && strings.TrimSpace(in.ProbeModel) != "" && cap.Rate != nil)
	if e != nil || !monitorable {
		problem(w, 422, "该上游不可监听: "+cap.Message)
		return
	}
	enc, e := s.vault.Encrypt(in.Key)
	if e != nil {
		problem(w, 500, e.Error())
		return
	}
	src, e := s.store.CreateSource(store.Source{UserID: uid(r), Name: in.Name, BaseURL: in.BaseURL, Platform: cap.Platform, Secret: enc, Fingerprint: fingerprint, MonitorState: cap.MonitorState, ProbeModel: in.ProbeModel, Models: cap.Models, LastRate: cap.Rate})
	if e != nil {
		problem(w, 409, "上游重复或保存失败: "+e.Error())
		return
	}
	_ = s.store.AddSourceHealth(uid(r), src.ID, "healthy", "连接正常，已完成首次倍率检查", cap.Rate)
	if in.CreateTarget && len(resolvedTargets) > 0 {
		type siteProvision struct {
			site   store.Site
			groups []store.Group
		}
		provisions := make(map[int64]*siteProvision)
		for _, target := range resolvedTargets {
			provision := provisions[target.site.ID]
			if provision == nil {
				provision = &siteProvision{site: target.site}
				provisions[target.site.ID] = provision
			}
			provision.groups = append(provision.groups, target.group)
		}
		for _, provision := range provisions {
			admin, decryptErr := s.vault.Decrypt(provision.site.AdminSecret)
			if decryptErr != nil {
				_ = s.store.DeleteSource(uid(r), src.ID)
				problem(w, 422, "目标站点管理员凭证解密失败")
				return
			}
			groupNames := make([]string, 0, len(provision.groups))
			groupExternalIDs := make([]string, 0, len(provision.groups))
			for _, group := range provision.groups {
				groupNames = append(groupNames, group.Name)
				groupExternalIDs = append(groupExternalIDs, group.ExternalID)
			}
			e = s.client.CreateAccount(r.Context(), connectors.ManagedSite{BaseURL: provision.site.BaseURL, Platform: provision.site.Platform, Auth: connectors.Auth{Secret: admin, Header: provision.site.AdminHeader, UserID: provision.site.AdminUserID}}, connectors.CreateAccountInput{Name: in.Name, BaseURL: in.BaseURL, Key: in.Key, Platform: cap.Platform, GroupNames: groupNames, GroupExternalIDs: groupExternalIDs, Models: cap.Models})
			if e != nil {
				_ = s.store.DeleteSource(uid(r), src.ID)
				problem(w, 422, "目标账号/渠道创建失败: "+e.Error())
				return
			}
			_ = s.doImport(r.Context(), provision.site)
		}
	}
	createdTaskIDs := make([]int64, 0, len(resolvedTargets))
	for _, target := range resolvedTargets {
		task, taskErr := s.store.CreateTask(store.Task{UserID: uid(r), Name: in.Name + " → " + target.group.Name, SourceIDs: []int64{src.ID}, SiteID: target.site.ID, GroupID: target.group.ID, Adjustment: target.input.Adjustment, MinUpstreamRate: target.input.MinUpstreamRate, Enabled: true, LargeChangePct: 50})
		if taskErr != nil {
			for _, taskID := range createdTaskIDs {
				_ = s.store.DeleteTask(uid(r), taskID)
			}
			_ = s.store.DeleteSource(uid(r), src.ID)
			problem(w, 409, "同步任务创建失败: "+taskErr.Error())
			return
		}
		createdTaskIDs = append(createdTaskIDs, task.ID)
	}
	src.Secret = ""
	src.SecretMask = security.MaskSecret(in.Key)
	write(w, 201, src)
}
func (s *Server) updateSource(w http.ResponseWriter, r *http.Request) {
	userID, sourceID := uid(r), pathID(r)
	existing, err := s.store.Source(userID, sourceID)
	if err != nil {
		problem(w, http.StatusNotFound, "上游不存在")
		return
	}
	var in sourceInput
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	in.ProbeModel = strings.TrimSpace(in.ProbeModel)
	if in.Name == "" || in.BaseURL == "" {
		problem(w, http.StatusBadRequest, "上游名称和地址必填")
		return
	}
	secret := existing.Secret
	var key string
	if strings.TrimSpace(in.Key) != "" {
		key = in.Key
		secret, err = s.vault.Encrypt(key)
		if err != nil {
			problem(w, http.StatusInternalServerError, "上游 Key 保存失败")
			return
		}
	} else {
		key, err = s.vault.Decrypt(secret)
		if err != nil {
			problem(w, http.StatusUnprocessableEntity, "上游 Key 解密失败，请重新填写")
			return
		}
	}
	fingerprint := security.Fingerprint(in.BaseURL, key)
	if s.store.SourceFingerprintExistsExcept(userID, sourceID, fingerprint) {
		problem(w, http.StatusConflict, "相同地址和 Key 的上游已经存在")
		return
	}
	in.Key = key
	capability, err := s.detectSourceCapability(r.Context(), userID, in)
	monitorable := capability.MonitorState == connectors.MonitorDirect ||
		(capability.MonitorState == connectors.MonitorNewAPIProbe && in.ProbeModel != "" && capability.Rate != nil)
	if err != nil || !monitorable {
		problem(w, http.StatusUnprocessableEntity, "新设置无法持续监听，原配置未修改: "+capability.Message)
		return
	}
	models := capability.Models
	if len(models) == 0 {
		models = existing.Models
	}
	lastRate := capability.Rate
	if lastRate == nil {
		lastRate = existing.LastRate
	}
	updated, err := s.store.UpdateSource(store.Source{ID: sourceID, UserID: userID, Name: in.Name, BaseURL: in.BaseURL, Platform: capability.Platform, Secret: secret, Fingerprint: fingerprint, MonitorState: capability.MonitorState, ProbeModel: in.ProbeModel, Models: models, LastRate: lastRate})
	if err != nil {
		problem(w, http.StatusConflict, "上游更新失败: "+err.Error())
		return
	}
	_ = s.store.AddSourceHealth(userID, sourceID, "healthy", "配置已更新并通过连接检查", lastRate)
	updated.Secret = ""
	updated.SecretMask = security.MaskSecret(key)
	write(w, http.StatusOK, updated)
}
func (s *Server) deleteSource(w http.ResponseWriter, r *http.Request) {
	if e := s.store.DeleteSource(uid(r), pathID(r)); e != nil {
		problem(w, 409, "上游不存在或仍被同步任务使用")
		return
	}
	w.WriteHeader(204)
}

func (s *Server) syncSource(w http.ResponseWriter, r *http.Request) {
	userID, sourceID := uid(r), pathID(r)
	source, err := s.store.Source(userID, sourceID)
	if err != nil {
		problem(w, http.StatusNotFound, "上游不存在")
		return
	}

	allTasks, err := s.store.Tasks(userID)
	if err != nil {
		problem(w, http.StatusInternalServerError, "暂时无法读取关联任务")
		return
	}
	linkedCount := 0
	var tasks []store.Task
	for _, task := range allTasks {
		linked := false
		for _, id := range task.SourceIDs {
			if id == sourceID {
				linked = true
				break
			}
		}
		if !linked {
			continue
		}
		linkedCount++
		if task.Enabled {
			tasks = append(tasks, task)
		}
	}

	results := make([]map[string]any, 0, len(tasks))
	syncedTargets := make([]string, 0, len(tasks))
	syncedTargetSet := make(map[string]bool, len(tasks))
	failed := 0
	skipped := 0
	message := "上游检查完成"
	if store.IsDemoSource(source) {
		_, _ = s.checkSourceNow(r.Context(), source)
		source, _ = s.store.Source(userID, sourceID)
		source.HealthHistory, _ = s.store.SourceHealth(userID, sourceID, 30)
		source.Secret = ""
		write(w, http.StatusOK, map[string]any{
			"status":             "skipped",
			"message":            store.DemoSourceMessage,
			"source":             source,
			"linked_task_count":  linkedCount,
			"enabled_task_count": len(tasks),
			"results":            results,
		})
		return
	}
	if len(tasks) == 0 {
		capability, probeErr := s.checkSourceNow(r.Context(), source)
		if probeErr != nil {
			failed = 1
			message = capability.Message
			if strings.TrimSpace(message) == "" {
				message = probeErr.Error()
			}
		} else if linkedCount > 0 {
			message = "上游检查完成；关联任务目前均已暂停，本次没有写入目标站点"
		} else {
			message = "上游检查完成；当前没有关联同步任务"
		}
	} else {
		for index, task := range tasks {
			before := task.LastTargetRate
			runErr := s.engine.RunTask(r.Context(), task, index == 0)
			current, currentErr := s.store.Task(userID, task.ID)
			if currentErr != nil {
				current = task
			}
			site, _ := s.store.Site(userID, task.SiteID)
			group, _ := s.store.Group(userID, task.GroupID)
			outcome, detail := "unchanged", "目标倍率无变化"
			switch current.LastStatus {
			case "failed":
				outcome, detail = "failed", current.LastError
			case "blocked":
				outcome, detail = "blocked", current.LastError
			case "observed":
				outcome, detail = "observed", current.LastError
			case "skipped":
				outcome, detail = "skipped", current.LastError
				skipped++
			case "ok":
				if before == nil || current.LastTargetRate == nil || math.Abs(*before-*current.LastTargetRate) >= 1e-9 {
					outcome = "synced"
					targetName := fmt.Sprintf("%s / %s", site.Name, group.Name)
					if !syncedTargetSet[targetName] {
						syncedTargetSet[targetName] = true
						syncedTargets = append(syncedTargets, targetName)
					}
					detail = fmt.Sprintf("已推送到 %s，目标倍率 %s", targetName, formatRate(current.LastTargetRate))
				}
			}
			if runErr != nil {
				outcome = "failed"
				if strings.TrimSpace(detail) == "" {
					detail = runErr.Error()
				}
			}
			if strings.TrimSpace(detail) == "" {
				detail = "本次任务已完成"
			}
			if outcome == "failed" {
				failed++
			}
			results = append(results, map[string]any{
				"task_id":       task.ID,
				"task_name":     task.Name,
				"site_name":     site.Name,
				"group_name":    group.Name,
				"outcome":       outcome,
				"message":       detail,
				"upstream_rate": current.LastUpstreamRate,
				"target_rate":   current.LastTargetRate,
				"run_at":        current.LastRunAt,
			})
		}
		message = fmt.Sprintf("已完成 %d 条关联同步任务", len(tasks))
		if len(syncedTargets) > 0 {
			message += "；已推送到：" + strings.Join(syncedTargets, "；")
		}
		if failed > 0 {
			message += fmt.Sprintf("，其中 %d 条失败", failed)
		}
		if skipped > 0 {
			message += fmt.Sprintf("，其中 %d 条已跳过", skipped)
		}
	}

	source, _ = s.store.Source(userID, sourceID)
	if len(syncedTargets) > 1 {
		detail := fmt.Sprintf("本次已推送到 %d 个分组：%s", len(syncedTargets), strings.Join(syncedTargets, "；"))
		_ = s.store.AddSourceHealth(userID, sourceID, "synced", detail, source.LastRate)
	}
	source.HealthHistory, _ = s.store.SourceHealth(userID, sourceID, 30)
	source.Secret = ""
	status := "success"
	if failed > 0 && (len(tasks) == 0 || failed == len(tasks)) {
		status = "failed"
	} else if failed > 0 {
		status = "partial"
	} else if skipped > 0 && skipped == len(tasks) {
		status = "skipped"
	} else if skipped > 0 {
		status = "partial"
	}
	write(w, http.StatusOK, map[string]any{
		"status":             status,
		"message":            message,
		"source":             source,
		"linked_task_count":  linkedCount,
		"enabled_task_count": len(tasks),
		"results":            results,
	})
}

func formatRate(value *float64) string {
	if value == nil {
		return "未记录"
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", *value), "0"), ".")
}

func (s *Server) checkSourceNow(ctx context.Context, source store.Source) (connectors.Capability, error) {
	if store.IsDemoSource(source) {
		_ = s.store.UpdateSourceCheck(source.UserID, source.ID, connectors.MonitorDemo, nil, "")
		_ = s.store.AddSourceHealth(source.UserID, source.ID, "skipped", store.DemoSourceMessage, nil)
		return connectors.Capability{Platform: source.Platform, MonitorState: connectors.MonitorDemo, Models: source.Models, Rate: source.LastRate, Message: store.DemoSourceMessage}, nil
	}
	key, err := s.vault.Decrypt(source.Secret)
	if err != nil {
		message := "上游 Key 解密失败"
		_ = s.store.UpdateSourceCheck(source.UserID, source.ID, connectors.MonitorFailed, nil, message)
		_ = s.store.AddSourceHealth(source.UserID, source.ID, "failed", message, nil)
		return connectors.Capability{Platform: source.Platform, MonitorState: connectors.MonitorFailed, Message: message}, err
	}
	capability, err := s.client.Detect(ctx, source.BaseURL, key, source.ProbeModel)
	if err != nil {
		message := capability.Message
		if strings.TrimSpace(message) == "" {
			message = err.Error()
		}
		_ = s.store.UpdateSourceCheck(source.UserID, source.ID, connectors.MonitorFailed, nil, message)
		_ = s.store.AddSourceHealth(source.UserID, source.ID, "failed", message, nil)
		return capability, err
	}
	_ = s.store.UpdateSourceCheck(source.UserID, source.ID, capability.MonitorState, capability.Rate, "")
	if len(capability.Models) > 0 {
		_ = s.store.UpdateSourceModels(source.UserID, source.ID, capability.Models)
	}
	_ = s.store.AddSourceHealth(source.UserID, source.ID, "healthy", "手动检查完成，连接正常", capability.Rate)
	return capability, nil
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Tasks(uid(r))
	if e != nil {
		problem(w, 500, e.Error())
		return
	}
	sites, _ := s.store.Sites(uid(r))
	sources, _ := s.store.Sources(uid(r))
	siteMap := map[int64]store.Site{}
	sourceMap := map[int64]store.Source{}
	for _, x := range sites {
		x.AdminSecret = ""
		siteMap[x.ID] = x
	}
	for _, x := range sources {
		x.Secret = ""
		sourceMap[x.ID] = x
	}
	for i := range v {
		site := siteMap[v[i].SiteID]
		group, _ := s.store.Group(uid(r), v[i].GroupID)
		v[i].Site = &site
		v[i].Group = &group
		if x, ok := sourceMap[v[i].SourceID]; ok {
			v[i].Source = &x
		}
	}
	write(w, 200, nonNil(v))
}
func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name            string  `json:"name"`
		SourceIDs       []int64 `json:"source_ids"`
		SiteID          int64   `json:"site_id"`
		GroupID         int64   `json:"group_id"`
		Adjustment      float64 `json:"adjustment"`
		MinUpstreamRate float64 `json:"minimum_upstream_rate"`
		Enabled         bool    `json:"enabled"`
		ShadowMode      bool    `json:"shadow_mode"`
		LargeChangePct  float64 `json:"large_change_percent"`
	}
	if !decode(w, r, &in) {
		return
	}
	site, se := s.store.Site(uid(r), in.SiteID)
	group, ge := s.store.Group(uid(r), in.GroupID)
	if se != nil || ge != nil || group.SiteID != site.ID {
		problem(w, 400, "目标站点或分组无效")
		return
	}
	for _, id := range in.SourceIDs {
		if _, e := s.store.Source(uid(r), id); e != nil {
			problem(w, 400, "包含无效上游")
			return
		}
	}
	if in.LargeChangePct <= 0 {
		in.LargeChangePct = 50
	}
	if in.MinUpstreamRate < 0 || math.IsNaN(in.MinUpstreamRate) || math.IsInf(in.MinUpstreamRate, 0) {
		problem(w, 400, "最低上游倍率必须大于或等于 0")
		return
	}
	v, e := s.store.CreateTask(store.Task{UserID: uid(r), Name: in.Name, SourceIDs: in.SourceIDs, SiteID: in.SiteID, GroupID: in.GroupID, Adjustment: in.Adjustment, MinUpstreamRate: in.MinUpstreamRate, Enabled: in.Enabled, ShadowMode: in.ShadowMode, LargeChangePct: in.LargeChangePct})
	if e != nil {
		if strings.Contains(e.Error(), "UNIQUE constraint") {
			problem(w, 409, "该目标分组已经有启用中的同步任务")
			return
		}
		problem(w, 400, e.Error())
		return
	}
	write(w, 201, v)
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name            string  `json:"name"`
		SourceIDs       []int64 `json:"source_ids"`
		SiteID          int64   `json:"site_id"`
		GroupID         int64   `json:"group_id"`
		Adjustment      float64 `json:"adjustment"`
		MinUpstreamRate float64 `json:"minimum_upstream_rate"`
		ShadowMode      bool    `json:"shadow_mode"`
		LargeChangePct  float64 `json:"large_change_percent"`
	}
	if !decode(w, r, &in) {
		return
	}
	if _, e := s.store.Task(uid(r), pathID(r)); e != nil {
		problem(w, 404, "任务不存在")
		return
	}
	site, siteErr := s.store.Site(uid(r), in.SiteID)
	group, groupErr := s.store.Group(uid(r), in.GroupID)
	if siteErr != nil || groupErr != nil || group.SiteID != site.ID {
		problem(w, 400, "目标站点或分组无效")
		return
	}
	for _, id := range in.SourceIDs {
		if _, e := s.store.Source(uid(r), id); e != nil {
			problem(w, 400, "包含无效上游")
			return
		}
	}
	if in.LargeChangePct <= 0 {
		in.LargeChangePct = 50
	}
	if in.MinUpstreamRate < 0 || math.IsNaN(in.MinUpstreamRate) || math.IsInf(in.MinUpstreamRate, 0) {
		problem(w, 400, "最低上游倍率必须大于或等于 0")
		return
	}
	v, e := s.store.UpdateTask(store.Task{ID: pathID(r), UserID: uid(r), Name: in.Name, SourceIDs: in.SourceIDs, SiteID: in.SiteID, GroupID: in.GroupID, Adjustment: in.Adjustment, MinUpstreamRate: in.MinUpstreamRate, ShadowMode: in.ShadowMode, LargeChangePct: in.LargeChangePct})
	if e != nil {
		if strings.Contains(e.Error(), "UNIQUE constraint") {
			problem(w, 409, "该目标分组已经有启用中的同步任务")
			return
		}
		problem(w, 400, e.Error())
		return
	}
	write(w, 200, v)
}
func (s *Server) toggleTask(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if e := s.store.ToggleTask(uid(r), pathID(r), in.Enabled); e != nil {
		problem(w, 409, "启用失败：目标分组可能已有活动任务")
		return
	}
	v, _ := s.store.Task(uid(r), pathID(r))
	write(w, 200, v)
}
func (s *Server) runTask(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Task(uid(r), pathID(r))
	if e != nil {
		problem(w, 404, "任务不存在")
		return
	}
	if e = s.engine.RunTask(r.Context(), v, true); e != nil {
		problem(w, 422, e.Error())
		return
	}
	v, _ = s.store.Task(uid(r), v.ID)
	write(w, 200, v)
}
func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	if e := s.store.DeleteTask(uid(r), pathID(r)); e != nil {
		problem(w, 404, "任务不存在")
		return
	}
	w.WriteHeader(204)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	v, e := s.store.Events(uid(r), 100)
	if e != nil {
		problem(w, 500, e.Error())
		return
	}
	write(w, 200, nonNil(v))
}
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		problem(w, 500, "不支持实时推送")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, cancel := s.hub.Subscribe(uid(r))
	defer cancel()
	fmt.Fprint(w, "event: ready\ndata: {}\n\n")
	flusher.Flush()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case b := <-ch:
			fmt.Fprintf(w, "event: notification\ndata: %s\n\n", b)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
func (s *Server) imageObservation(w http.ResponseWriter, r *http.Request) {
	var v store.ImageObservation
	if !decode(w, r, &v) {
		return
	}
	if v.SourceID == 0 || v.Model == "" || v.ActualCost < 0 {
		problem(w, 400, "观测数据不完整")
		return
	}
	src, e := s.store.Source(uid(r), v.SourceID)
	if e != nil {
		problem(w, 404, "上游不存在")
		return
	}
	v.UserID = uid(r)
	v.ObservedAt = time.Now()
	inserted, e := s.store.AddImageObservation(v)
	if e != nil {
		problem(w, 500, e.Error())
		return
	}
	if !inserted {
		write(w, http.StatusOK, v)
		return
	}
	reason := fmt.Sprintf("%s %s %s ×%d，实际成本 %.6g；仅观测，不会在目标不兼容时强行写入。", src.Name, v.Model, v.Size, v.Count, v.ActualCost)
	sourceID := src.ID
	event, _ := s.store.AddEvent(store.Event{UserID: uid(r), Level: "info", Kind: "image_observed", Title: "已记录生图实际价格", Detail: reason, SourceID: &sourceID, SourceName: src.Name, Reason: reason, RequestID: v.RequestID})
	s.hub.Publish(event)
	write(w, 201, v)
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		id, e := security.VerifySession(s.cfg.SessionSecret, token)
		if e != nil {
			problem(w, 401, "登录已失效")
			return
		}
		u, userErr := s.store.UserByID(id)
		if userErr != nil || u.Status != "active" {
			problem(w, http.StatusUnauthorized, "账户已停用或不存在")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, id)))
	})
}
func (s *Server) admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.store.UserByID(uid(r))
		if err != nil || u.Role != "admin" {
			problem(w, http.StatusForbidden, "需要管理员权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Printf("panic: %v", v)
				problem(w, 500, "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'")
		next.ServeHTTP(w, r)
	})
}
func uid(r *http.Request) int64    { v, _ := r.Context().Value(userKey).(int64); return v }
func pathID(r *http.Request) int64 { v, _ := strconv.ParseInt(r.PathValue("id"), 10, 64); return v }
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		problem(w, 400, "请求格式错误: "+e.Error())
		return false
	}
	return true
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, msg string) {
	write(w, status, map[string]any{"error": msg})
}
func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func (s *Server) frontend(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		problem(w, http.StatusNotFound, "接口不存在")
		return
	}
	clean := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if clean == "." {
		clean = "index.html"
	}
	if strings.Contains(clean, "..") {
		http.NotFound(w, r)
		return
	}
	if dist := strings.TrimSpace(os.Getenv("RATEWATCH_WEB_DIR")); dist != "" {
		filePath := filepath.Join(dist, filepath.FromSlash(clean))
		if info, e := os.Stat(filePath); e == nil && !info.IsDir() {
			http.ServeFile(w, r, filePath)
			return
		}
		index := filepath.Join(dist, "index.html")
		if _, e := os.Stat(index); e == nil {
			http.ServeFile(w, r, index)
			return
		}
		problem(w, 404, "配置的前端目录不可用")
		return
	}
	if info, err := fs.Stat(embeddedFrontendFS, clean); clean == "index.html" || err != nil || info.IsDir() {
		index, readErr := fs.ReadFile(embeddedFrontendFS, "index.html")
		if readErr != nil {
			problem(w, http.StatusInternalServerError, "内置管理网页不可用")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
		return
	}
	request := r.Clone(r.Context())
	request.URL.Path = "/" + clean
	request.URL.RawPath = ""
	embeddedFrontendHandler.ServeHTTP(w, request)
}
