package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY, email TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, notify_email TEXT NOT NULL DEFAULT '', email_enabled INTEGER NOT NULL DEFAULT 1, notify_kinds TEXT NOT NULL DEFAULT '["rate_changed","write_failed","probe_failed","partial_probe","invalid_rate","model_diff","image_observed"]', role TEXT NOT NULL DEFAULT 'user', status TEXT NOT NULL DEFAULT 'active', onboarded INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS sites (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL, base_url TEXT NOT NULL, platform TEXT NOT NULL, admin_secret TEXT NOT NULL, admin_user_id TEXT NOT NULL DEFAULT '1', admin_header TEXT NOT NULL DEFAULT 'Authorization', status TEXT NOT NULL DEFAULT 'unchecked', last_error TEXT NOT NULL DEFAULT '', last_imported_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE(user_id, base_url));
CREATE TABLE IF NOT EXISTS groups (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE, external_id TEXT NOT NULL, name TEXT NOT NULL, rate REAL NOT NULL DEFAULT 1, status TEXT NOT NULL DEFAULT 'active', UNIQUE(site_id, external_id));
CREATE TABLE IF NOT EXISTS accounts (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE, external_id TEXT NOT NULL, name TEXT NOT NULL, platform TEXT NOT NULL DEFAULT '', base_url TEXT NOT NULL DEFAULT '', secret TEXT NOT NULL DEFAULT '', monitor_state TEXT NOT NULL DEFAULT 'missing_key', models_json TEXT NOT NULL DEFAULT '[]', rate REAL, last_error TEXT NOT NULL DEFAULT '', last_checked_at DATETIME, UNIQUE(site_id, external_id));
CREATE TABLE IF NOT EXISTS group_accounts (group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE, account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE, PRIMARY KEY(group_id, account_id));
CREATE TABLE IF NOT EXISTS sources (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL, base_url TEXT NOT NULL, platform TEXT NOT NULL, secret TEXT NOT NULL, fingerprint TEXT NOT NULL DEFAULT '', monitor_state TEXT NOT NULL, probe_model TEXT NOT NULL DEFAULT '', models_json TEXT NOT NULL DEFAULT '[]', last_rate REAL, last_error TEXT NOT NULL DEFAULT '', last_checked_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS sync_tasks (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL, source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE, site_id INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE, group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE, adjustment REAL NOT NULL DEFAULT 0, minimum_upstream_rate REAL NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1, last_upstream_rate REAL, last_target_rate REAL, last_status TEXT NOT NULL DEFAULT 'pending', last_error TEXT NOT NULL DEFAULT '', last_run_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS task_sources (task_id INTEGER NOT NULL REFERENCES sync_tasks(id) ON DELETE CASCADE, source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE, PRIMARY KEY(task_id, source_id));
CREATE UNIQUE INDEX IF NOT EXISTS one_enabled_writer ON sync_tasks(group_id) WHERE enabled = 1;
CREATE TABLE IF NOT EXISTS events (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, task_id INTEGER REFERENCES sync_tasks(id) ON DELETE SET NULL, level TEXT NOT NULL, kind TEXT NOT NULL, title TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '', request_id TEXT NOT NULL DEFAULT '', before_rate REAL, after_rate REAL, emailed INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS event_tenant_time ON events(user_id, created_at DESC);
CREATE TABLE IF NOT EXISTS image_observations (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE, model TEXT NOT NULL, size TEXT NOT NULL DEFAULT '', quality TEXT NOT NULL DEFAULT '', count INTEGER NOT NULL DEFAULT 1, group_rate REAL, unit_price REAL, actual_cost REAL NOT NULL, request_id TEXT NOT NULL DEFAULT '', observed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS source_health_checks (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, source_id INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE, state TEXT NOT NULL, message TEXT NOT NULL DEFAULT '', rate REAL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS source_health_time ON source_health_checks(source_id, id DESC);
CREATE TABLE IF NOT EXISTS system_settings (id INTEGER PRIMARY KEY CHECK(id=1), site_name TEXT NOT NULL DEFAULT 'RateWatch', admin_path TEXT NOT NULL DEFAULT 'admin', registration_open INTEGER NOT NULL DEFAULT 1, public_url TEXT NOT NULL DEFAULT '', smtp_host TEXT NOT NULL DEFAULT '', smtp_port INTEGER NOT NULL DEFAULT 587, smtp_user TEXT NOT NULL DEFAULT '', smtp_password TEXT NOT NULL DEFAULT '', smtp_from TEXT NOT NULL DEFAULT '', poll_seconds INTEGER NOT NULL DEFAULT 45, probe_seconds INTEGER NOT NULL DEFAULT 300, model_check_minutes INTEGER NOT NULL DEFAULT 30, email_minutes INTEGER NOT NULL DEFAULT 10, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
INSERT OR IGNORE INTO system_settings(id) VALUES(1);
CREATE TABLE IF NOT EXISTS password_reset_tokens (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, token_hash TEXT NOT NULL UNIQUE, expires_at DATETIME NOT NULL, used_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
`
	_, err := s.DB.Exec(schema)
	if err != nil {
		return err
	}
	_, _ = s.DB.Exec(`ALTER TABLE users ADD COLUMN notify_kinds TEXT NOT NULL DEFAULT '["rate_changed","write_failed","probe_failed","partial_probe","invalid_rate","model_diff","image_observed"]'`)
	_, _ = s.DB.Exec(`ALTER TABLE sources ADD COLUMN fingerprint TEXT NOT NULL DEFAULT ''`)
	_, _ = s.DB.Exec(`ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'user'`)
	_, _ = s.DB.Exec(`ALTER TABLE users ADD COLUMN status TEXT NOT NULL DEFAULT 'active'`)
	_, _ = s.DB.Exec(`ALTER TABLE users ADD COLUMN onboarded INTEGER NOT NULL DEFAULT 0`)
	_, _ = s.DB.Exec(`ALTER TABLE sync_tasks ADD COLUMN shadow_mode INTEGER NOT NULL DEFAULT 0`)
	_, _ = s.DB.Exec(`ALTER TABLE sync_tasks ADD COLUMN large_change_percent REAL NOT NULL DEFAULT 50`)
	_, _ = s.DB.Exec(`ALTER TABLE sync_tasks ADD COLUMN minimum_upstream_rate REAL NOT NULL DEFAULT 0`)
	for _, q := range []string{
		`ALTER TABLE events ADD COLUMN site_id INTEGER`, `ALTER TABLE events ADD COLUMN source_id INTEGER`, `ALTER TABLE events ADD COLUMN group_id INTEGER`,
		`ALTER TABLE events ADD COLUMN site_name TEXT NOT NULL DEFAULT ''`, `ALTER TABLE events ADD COLUMN source_name TEXT NOT NULL DEFAULT ''`, `ALTER TABLE events ADD COLUMN group_name TEXT NOT NULL DEFAULT ''`, `ALTER TABLE events ADD COLUMN reason TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = s.DB.Exec(q)
	}
	_, _ = s.DB.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS source_fingerprint_unique ON sources(user_id,fingerprint) WHERE fingerprint<>''`)
	_, _ = s.DB.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS image_observation_request_unique ON image_observations(source_id,request_id) WHERE request_id<>''`)
	_, _ = s.DB.Exec(`UPDATE sources SET last_error='' WHERE monitor_state IN ('direct','newapi_probe') AND last_error<>''`)
	return nil
}

func (s *Store) CreateUser(email, hash string) (User, error) {
	r, e := s.DB.Exec(`INSERT INTO users(email,password_hash,notify_email) VALUES(?,?,?)`, strings.ToLower(strings.TrimSpace(email)), hash, strings.ToLower(strings.TrimSpace(email)))
	if e != nil {
		return User{}, e
	}
	id, _ := r.LastInsertId()
	return s.UserByID(id)
}
func (s *Store) UserByEmail(email string) (User, error) {
	var u User
	var enabled int
	var kinds string
	var onboarded int
	e := s.DB.QueryRow(`SELECT id,email,password_hash,notify_email,email_enabled,notify_kinds,role,status,onboarded,created_at FROM users WHERE email=?`, strings.ToLower(strings.TrimSpace(email))).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.NotifyEmail, &enabled, &kinds, &u.Role, &u.Status, &onboarded, &u.CreatedAt)
	u.EmailEnabled = enabled == 1
	u.Onboarded = onboarded == 1
	_ = json.Unmarshal([]byte(kinds), &u.NotifyKinds)
	return u, e
}
func (s *Store) UserByID(id int64) (User, error) {
	var u User
	var enabled int
	var kinds string
	var onboarded int
	e := s.DB.QueryRow(`SELECT id,email,password_hash,notify_email,email_enabled,notify_kinds,role,status,onboarded,created_at FROM users WHERE id=?`, id).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.NotifyEmail, &enabled, &kinds, &u.Role, &u.Status, &onboarded, &u.CreatedAt)
	u.EmailEnabled = enabled == 1
	u.Onboarded = onboarded == 1
	_ = json.Unmarshal([]byte(kinds), &u.NotifyKinds)
	return u, e
}

func (s *Store) EnsureAdmin(email, hash string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	_, err := s.DB.Exec(`INSERT INTO users(email,password_hash,notify_email,role,status,onboarded) VALUES(?,?,?,'admin','active',1) ON CONFLICT(email) DO UPDATE SET role='admin',status='active'`, email, hash, email)
	return err
}
func (s *Store) UpdateUserEmail(id int64, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	_, err := s.DB.Exec(`UPDATE users SET email=?,notify_email=CASE WHEN notify_email='' THEN ? ELSE notify_email END WHERE id=?`, email, email, id)
	return err
}
func (s *Store) UpdateUserPassword(id int64, hash string) error {
	_, err := s.DB.Exec(`UPDATE users SET password_hash=? WHERE id=?`, hash, id)
	return err
}
func (s *Store) SetUserOnboarded(id int64, value bool) error {
	_, err := s.DB.Exec(`UPDATE users SET onboarded=? WHERE id=?`, value, id)
	return err
}
func (s *Store) CreatePasswordReset(userID int64, tokenHash string, expires time.Time) error {
	_, _ = s.DB.Exec(`DELETE FROM password_reset_tokens WHERE user_id=? OR expires_at<CURRENT_TIMESTAMP`, userID)
	_, err := s.DB.Exec(`INSERT INTO password_reset_tokens(user_id,token_hash,expires_at) VALUES(?,?,?)`, userID, tokenHash, expires)
	return err
}
func (s *Store) ConsumePasswordReset(tokenHash, passwordHash string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id, userID int64
	if err = tx.QueryRow(`SELECT id,user_id FROM password_reset_tokens WHERE token_hash=? AND used_at IS NULL AND expires_at>CURRENT_TIMESTAMP`, tokenHash).Scan(&id, &userID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE users SET password_hash=? WHERE id=?`, passwordHash, userID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE password_reset_tokens SET used_at=CURRENT_TIMESTAMP WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) UpdateUserNotifications(id int64, email string, enabled bool, kinds []string) error {
	b, _ := json.Marshal(kinds)
	_, e := s.DB.Exec(`UPDATE users SET notify_email=?,email_enabled=?,notify_kinds=? WHERE id=?`, strings.TrimSpace(email), enabled, string(b), id)
	return e
}

func (s *Store) CreateSite(v Site) (Site, error) {
	r, e := s.DB.Exec(`INSERT INTO sites(user_id,name,base_url,platform,admin_secret,admin_user_id,admin_header) VALUES(?,?,?,?,?,?,?)`, v.UserID, v.Name, trimURL(v.BaseURL), v.Platform, v.AdminSecret, v.AdminUserID, v.AdminHeader)
	if e != nil {
		return Site{}, e
	}
	id, _ := r.LastInsertId()
	return s.Site(v.UserID, id)
}
func (s *Store) UpdateSite(v Site) (Site, error) {
	result, err := s.DB.Exec(`UPDATE sites SET name=?,base_url=?,platform=?,admin_secret=?,admin_user_id=?,admin_header=?,status='ready',last_error='',last_imported_at=CURRENT_TIMESTAMP WHERE user_id=? AND id=?`, v.Name, trimURL(v.BaseURL), v.Platform, v.AdminSecret, v.AdminUserID, v.AdminHeader, v.UserID, v.ID)
	if err != nil {
		return Site{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return Site{}, errors.New("站点不存在")
	}
	return s.Site(v.UserID, v.ID)
}
func (s *Store) Sites(uid int64) ([]Site, error) {
	rows, e := s.DB.Query(`SELECT id,name,base_url,platform,admin_user_id,admin_header,status,last_error,last_imported_at,created_at FROM sites WHERE user_id=? ORDER BY id DESC`, uid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		var v Site
		v.UserID = uid
		if e = rows.Scan(&v.ID, &v.Name, &v.BaseURL, &v.Platform, &v.AdminUserID, &v.AdminHeader, &v.Status, &v.LastError, &v.LastImportedAt, &v.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Site(uid, id int64) (Site, error) {
	var v Site
	v.UserID = uid
	e := s.DB.QueryRow(`SELECT id,name,base_url,platform,admin_secret,admin_user_id,admin_header,status,last_error,last_imported_at,created_at FROM sites WHERE user_id=? AND id=?`, uid, id).Scan(&v.ID, &v.Name, &v.BaseURL, &v.Platform, &v.AdminSecret, &v.AdminUserID, &v.AdminHeader, &v.Status, &v.LastError, &v.LastImportedAt, &v.CreatedAt)
	return v, e
}
func (s *Store) SetSiteStatus(uid, id int64, status, msg string, imported bool) error {
	q := `UPDATE sites SET status=?,last_error=? WHERE user_id=? AND id=?`
	args := []any{status, msg, uid, id}
	if imported {
		q = `UPDATE sites SET status=?,last_error=?,last_imported_at=CURRENT_TIMESTAMP WHERE user_id=? AND id=?`
	}
	_, e := s.DB.Exec(q, args...)
	return e
}
func (s *Store) DeleteSite(uid, id int64) error {
	r, e := s.DB.Exec(`DELETE FROM sites WHERE user_id=? AND id=?`, uid, id)
	return changed(r, e)
}

func (s *Store) ReplaceInventory(uid, siteID int64, groups []Group, accounts []Account) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`DELETE FROM group_accounts WHERE group_id IN (SELECT id FROM groups WHERE site_id=? AND user_id=?)`, siteID, uid); e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE groups SET status='deleted' WHERE site_id=? AND user_id=?`, siteID, uid); e != nil {
		return e
	}
	for _, g := range groups {
		_, e = tx.Exec(`INSERT INTO groups(user_id,site_id,external_id,name,rate,status) VALUES(?,?,?,?,?,?) ON CONFLICT(site_id,external_id) DO UPDATE SET name=excluded.name,rate=excluded.rate,status=excluded.status`, uid, siteID, g.ExternalID, g.Name, g.Rate, g.Status)
		if e != nil {
			return e
		}
	}
	for _, a := range accounts {
		b, _ := json.Marshal(a.Models)
		_, e = tx.Exec(`INSERT INTO accounts(user_id,site_id,external_id,name,platform,base_url,monitor_state,models_json,rate,last_error) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(site_id,external_id) DO UPDATE SET name=excluded.name,platform=excluded.platform,base_url=excluded.base_url,models_json=excluded.models_json,rate=excluded.rate`, uid, siteID, a.ExternalID, a.Name, a.Platform, a.BaseURL, "missing_key", string(b), a.Rate, "")
		if e != nil {
			return e
		}
	}
	for _, a := range accounts {
		for _, externalGroupID := range a.RelationGroups {
			_, e = tx.Exec(`INSERT OR IGNORE INTO group_accounts(group_id,account_id) SELECT g.id,a.id FROM groups g,accounts a WHERE g.site_id=? AND g.external_id=? AND a.site_id=? AND a.external_id=?`, siteID, fmt.Sprint(externalGroupID), siteID, a.ExternalID)
			if e != nil {
				return e
			}
		}
	}
	return tx.Commit()
}

func (s *Store) Inventory(uid, siteID int64) ([]Group, error) {
	rows, e := s.DB.Query(`SELECT id,external_id,name,rate,status FROM groups WHERE user_id=? AND site_id=? ORDER BY name`, uid, siteID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var gs []Group
	for rows.Next() {
		var g Group
		g.UserID = uid
		g.SiteID = siteID
		if e = rows.Scan(&g.ID, &g.ExternalID, &g.Name, &g.Rate, &g.Status); e != nil {
			return nil, e
		}
		ar, e := s.DB.Query(`SELECT a.id,a.external_id,a.name,a.platform,a.base_url,COALESCE((SELECT s.monitor_state FROM sources s WHERE s.user_id=a.user_id AND rtrim(s.base_url,'/')=rtrim(a.base_url,'/') ORDER BY s.id DESC LIMIT 1),a.monitor_state),a.models_json,COALESCE((SELECT s.last_rate FROM sources s WHERE s.user_id=a.user_id AND rtrim(s.base_url,'/')=rtrim(a.base_url,'/') AND s.last_rate IS NOT NULL ORDER BY s.id DESC LIMIT 1),a.rate),COALESCE((SELECT s.last_error FROM sources s WHERE s.user_id=a.user_id AND rtrim(s.base_url,'/')=rtrim(a.base_url,'/') ORDER BY s.id DESC LIMIT 1),a.last_error),CAST(strftime('%s',COALESCE((SELECT s.last_checked_at FROM sources s WHERE s.user_id=a.user_id AND rtrim(s.base_url,'/')=rtrim(a.base_url,'/') ORDER BY s.id DESC LIMIT 1),a.last_checked_at)) AS INTEGER),CASE WHEN EXISTS(SELECT 1 FROM sources s WHERE s.user_id=a.user_id AND rtrim(s.base_url,'/')=rtrim(a.base_url,'/') ORDER BY s.id DESC LIMIT 1) OR a.secret<>'' THEN 'set' ELSE '' END FROM accounts a JOIN group_accounts ga ON ga.account_id=a.id WHERE ga.group_id=? ORDER BY a.name`, g.ID)
		if e != nil {
			return nil, e
		}
		for ar.Next() {
			var a Account
			var models, secret string
			var lastCheckedAt sql.NullInt64
			a.UserID = uid
			a.SiteID = siteID
			if e = ar.Scan(&a.ID, &a.ExternalID, &a.Name, &a.Platform, &a.BaseURL, &a.MonitorState, &models, &a.Rate, &a.LastError, &lastCheckedAt, &secret); e != nil {
				ar.Close()
				return nil, e
			}
			if lastCheckedAt.Valid {
				checkedAt := time.Unix(lastCheckedAt.Int64, 0)
				a.LastCheckedAt = &checkedAt
			}
			_ = json.Unmarshal([]byte(models), &a.Models)
			if secret != "" {
				a.SecretMask = "••••••••"
			}
			g.Accounts = append(g.Accounts, a)
		}
		ar.Close()
		gs = append(gs, g)
	}
	return gs, rows.Err()
}
func (s *Store) Group(uid, id int64) (Group, error) {
	var g Group
	g.UserID = uid
	e := s.DB.QueryRow(`SELECT id,site_id,external_id,name,rate,status FROM groups WHERE user_id=? AND id=?`, uid, id).Scan(&g.ID, &g.SiteID, &g.ExternalID, &g.Name, &g.Rate, &g.Status)
	return g, e
}

func (s *Store) UpdateGroupRate(uid, id int64, rate float64) error {
	_, e := s.DB.Exec(`UPDATE groups SET rate=? WHERE user_id=? AND id=?`, rate, uid, id)
	return e
}

func (s *Store) CreateSource(v Source) (Source, error) {
	b, _ := json.Marshal(v.Models)
	r, e := s.DB.Exec(`INSERT INTO sources(user_id,name,base_url,platform,secret,fingerprint,monitor_state,probe_model,models_json,last_rate,last_error,last_checked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)`, v.UserID, v.Name, trimURL(v.BaseURL), v.Platform, v.Secret, v.Fingerprint, v.MonitorState, v.ProbeModel, string(b), v.LastRate, v.LastError)
	if e != nil {
		return Source{}, e
	}
	id, _ := r.LastInsertId()
	return s.Source(v.UserID, id)
}
func (s *Store) Sources(uid int64) ([]Source, error) {
	rows, e := s.DB.Query(`SELECT id,name,base_url,platform,secret,monitor_state,probe_model,models_json,last_rate,last_error,last_checked_at,created_at FROM sources WHERE user_id=? ORDER BY id DESC`, uid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		v, e := scanSource(rows, uid)
		if e != nil {
			return nil, e
		}
		v.HealthHistory, _ = s.SourceHealth(uid, v.ID, 30)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) SourceFingerprintExists(uid int64, fingerprint string) bool {
	var n int
	_ = s.DB.QueryRow(`SELECT count(*) FROM sources WHERE user_id=? AND fingerprint=?`, uid, fingerprint).Scan(&n)
	return n > 0
}
func (s *Store) SourceFingerprintExistsExcept(uid, sourceID int64, fingerprint string) bool {
	var n int
	_ = s.DB.QueryRow(`SELECT count(*) FROM sources WHERE user_id=? AND fingerprint=? AND id<>?`, uid, fingerprint, sourceID).Scan(&n)
	return n > 0
}
func (s *Store) Source(uid, id int64) (Source, error) {
	row := s.DB.QueryRow(`SELECT id,name,base_url,platform,secret,monitor_state,probe_model,models_json,last_rate,last_error,last_checked_at,created_at FROM sources WHERE user_id=? AND id=?`, uid, id)
	return scanSource(row, uid)
}

func (s *Store) UpdateSource(v Source) (Source, error) {
	models, err := json.Marshal(v.Models)
	if err != nil {
		return Source{}, err
	}
	result, err := s.DB.Exec(`UPDATE sources SET name=?,base_url=?,platform=?,secret=?,fingerprint=?,monitor_state=?,probe_model=?,models_json=?,last_rate=?,last_error='',last_checked_at=CURRENT_TIMESTAMP WHERE user_id=? AND id=?`, v.Name, trimURL(v.BaseURL), v.Platform, v.Secret, v.Fingerprint, v.MonitorState, v.ProbeModel, string(models), v.LastRate, v.UserID, v.ID)
	if err != nil {
		return Source{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return Source{}, errors.New("上游不存在")
	}
	return s.Source(v.UserID, v.ID)
}

type scanner interface{ Scan(...any) error }

func scanSource(row scanner, uid int64) (Source, error) {
	var v Source
	var models string
	v.UserID = uid
	e := row.Scan(&v.ID, &v.Name, &v.BaseURL, &v.Platform, &v.Secret, &v.MonitorState, &v.ProbeModel, &models, &v.LastRate, &v.LastError, &v.LastCheckedAt, &v.CreatedAt)
	_ = json.Unmarshal([]byte(models), &v.Models)
	return v, e
}
func (s *Store) UpdateSourceCheck(uid, id int64, state string, rate *float64, msg string) error {
	// 探测失败时保留最后一次成功倍率，避免短暂的限流、余额或资源保护
	// 把已验证的倍率清空并在每个调度周期重复制造相同异常。
	_, e := s.DB.Exec(`UPDATE sources SET monitor_state=?,last_rate=COALESCE(?,last_rate),last_error=?,last_checked_at=CURRENT_TIMESTAMP WHERE user_id=? AND id=?`, state, rate, msg, uid, id)
	return e
}
func (s *Store) AddSourceHealth(uid, sourceID int64, state, message string, rate *float64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO source_health_checks(user_id,source_id,state,message,rate) SELECT ?,id,?,?,? FROM sources WHERE id=? AND user_id=?`, uid, state, message, rate, sourceID, uid); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM source_health_checks WHERE source_id=? AND id NOT IN (SELECT id FROM source_health_checks WHERE source_id=? ORDER BY id DESC LIMIT 30)`, sourceID, sourceID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) SourceHealth(uid, sourceID int64, limit int) ([]SourceHealth, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	rows, err := s.DB.Query(`SELECT id,source_id,state,message,rate,created_at FROM source_health_checks WHERE user_id=? AND source_id=? ORDER BY id DESC LIMIT ?`, uid, sourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceHealth
	for rows.Next() {
		var v SourceHealth
		if err = rows.Scan(&v.ID, &v.SourceID, &v.State, &v.Message, &v.Rate, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) UpdateSourceModels(uid, id int64, models []string) error {
	b, e := json.Marshal(models)
	if e != nil {
		return e
	}
	_, e = s.DB.Exec(`UPDATE sources SET models_json=? WHERE user_id=? AND id=?`, string(b), uid, id)
	return e
}
func (s *Store) DeleteSource(uid, id int64) error {
	r, e := s.DB.Exec(`DELETE FROM sources WHERE user_id=? AND id=?`, uid, id)
	return changed(r, e)
}

func (s *Store) CreateTask(v Task) (Task, error) {
	if len(v.SourceIDs) == 0 && v.SourceID != 0 {
		v.SourceIDs = []int64{v.SourceID}
	}
	if len(v.SourceIDs) == 0 {
		return Task{}, errors.New("至少选择一个上游")
	}
	v.SourceID = v.SourceIDs[0]
	tx, e := s.DB.Begin()
	if e != nil {
		return Task{}, e
	}
	defer tx.Rollback()
	r, e := tx.Exec(`INSERT INTO sync_tasks(user_id,name,source_id,site_id,group_id,adjustment,minimum_upstream_rate,enabled,shadow_mode,large_change_percent) VALUES(?,?,?,?,?,?,?,?,?,?)`, v.UserID, v.Name, v.SourceID, v.SiteID, v.GroupID, v.Adjustment, v.MinUpstreamRate, v.Enabled, v.ShadowMode, v.LargeChangePct)
	if e != nil {
		return Task{}, e
	}
	id, _ := r.LastInsertId()
	for _, sourceID := range v.SourceIDs {
		if _, e = tx.Exec(`INSERT INTO task_sources(task_id,source_id) SELECT ?,id FROM sources WHERE id=? AND user_id=?`, id, sourceID, v.UserID); e != nil {
			return Task{}, e
		}
	}
	if e = tx.Commit(); e != nil {
		return Task{}, e
	}
	return s.Task(v.UserID, id)
}
func (s *Store) Tasks(uid int64) ([]Task, error) {
	rows, e := s.DB.Query(`SELECT id,name,source_id,site_id,group_id,adjustment,minimum_upstream_rate,enabled,shadow_mode,large_change_percent,last_upstream_rate,last_target_rate,last_status,last_error,last_run_at,created_at FROM sync_tasks WHERE user_id=? ORDER BY id DESC`, uid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		v, e := scanTask(rows, uid)
		if e != nil {
			return nil, e
		}
		v.SourceIDs, _ = s.TaskSourceIDs(uid, v.ID)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) EnabledTasks() ([]Task, error) {
	rows, e := s.DB.Query(`SELECT id,name,source_id,site_id,group_id,adjustment,minimum_upstream_rate,enabled,shadow_mode,large_change_percent,last_upstream_rate,last_target_rate,last_status,last_error,last_run_at,created_at,user_id FROM sync_tasks WHERE enabled=1`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var v Task
		var enabled, shadow int
		if e = rows.Scan(&v.ID, &v.Name, &v.SourceID, &v.SiteID, &v.GroupID, &v.Adjustment, &v.MinUpstreamRate, &enabled, &shadow, &v.LargeChangePct, &v.LastUpstreamRate, &v.LastTargetRate, &v.LastStatus, &v.LastError, &v.LastRunAt, &v.CreatedAt, &v.UserID); e != nil {
			return nil, e
		}
		v.Enabled = enabled == 1
		v.ShadowMode = shadow == 1
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Task(uid, id int64) (Task, error) {
	v, e := scanTask(s.DB.QueryRow(`SELECT id,name,source_id,site_id,group_id,adjustment,minimum_upstream_rate,enabled,shadow_mode,large_change_percent,last_upstream_rate,last_target_rate,last_status,last_error,last_run_at,created_at FROM sync_tasks WHERE user_id=? AND id=?`, uid, id), uid)
	if e == nil {
		v.SourceIDs, _ = s.TaskSourceIDs(uid, id)
	}
	return v, e
}

func (s *Store) TaskSourceIDs(uid, taskID int64) ([]int64, error) {
	rows, e := s.DB.Query(`SELECT ts.source_id FROM task_sources ts JOIN sync_tasks t ON t.id=ts.task_id WHERE ts.task_id=? AND t.user_id=? ORDER BY ts.source_id`, taskID, uid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		var id int64
		if e = s.DB.QueryRow(`SELECT source_id FROM sync_tasks WHERE id=? AND user_id=?`, taskID, uid).Scan(&id); e == nil {
			ids = []int64{id}
		}
	}
	return ids, rows.Err()
}
func scanTask(row scanner, uid int64) (Task, error) {
	var v Task
	var enabled, shadow int
	v.UserID = uid
	e := row.Scan(&v.ID, &v.Name, &v.SourceID, &v.SiteID, &v.GroupID, &v.Adjustment, &v.MinUpstreamRate, &enabled, &shadow, &v.LargeChangePct, &v.LastUpstreamRate, &v.LastTargetRate, &v.LastStatus, &v.LastError, &v.LastRunAt, &v.CreatedAt)
	v.Enabled = enabled == 1
	v.ShadowMode = shadow == 1
	return v, e
}

func (s *Store) UpdateTask(v Task) (Task, error) {
	if len(v.SourceIDs) == 0 && v.SourceID != 0 {
		v.SourceIDs = []int64{v.SourceID}
	}
	if len(v.SourceIDs) == 0 {
		return Task{}, errors.New("至少选择一个上游")
	}
	v.SourceID = v.SourceIDs[0]
	tx, e := s.DB.Begin()
	if e != nil {
		return Task{}, e
	}
	defer tx.Rollback()
	r, e := tx.Exec(`UPDATE sync_tasks SET name=?,source_id=?,site_id=?,group_id=?,adjustment=?,minimum_upstream_rate=?,shadow_mode=?,large_change_percent=? WHERE user_id=? AND id=?`, v.Name, v.SourceID, v.SiteID, v.GroupID, v.Adjustment, v.MinUpstreamRate, v.ShadowMode, v.LargeChangePct, v.UserID, v.ID)
	if e != nil {
		return Task{}, e
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return Task{}, errors.New("任务不存在")
	}
	if _, e = tx.Exec(`DELETE FROM task_sources WHERE task_id=?`, v.ID); e != nil {
		return Task{}, e
	}
	for _, sourceID := range v.SourceIDs {
		if _, e = tx.Exec(`INSERT INTO task_sources(task_id,source_id) SELECT ?,id FROM sources WHERE id=? AND user_id=?`, v.ID, sourceID, v.UserID); e != nil {
			return Task{}, e
		}
	}
	if e = tx.Commit(); e != nil {
		return Task{}, e
	}
	return s.Task(v.UserID, v.ID)
}
func (s *Store) UpdateTaskResult(id int64, upstream, target *float64, status, msg string) error {
	_, e := s.DB.Exec(`UPDATE sync_tasks SET last_upstream_rate=?,last_target_rate=?,last_status=?,last_error=?,last_run_at=CURRENT_TIMESTAMP WHERE id=?`, upstream, target, status, msg, id)
	return e
}
func (s *Store) ToggleTask(uid, id int64, enabled bool) error {
	_, e := s.DB.Exec(`UPDATE sync_tasks SET enabled=? WHERE user_id=? AND id=?`, enabled, uid, id)
	return e
}
func (s *Store) DeleteTask(uid, id int64) error {
	r, e := s.DB.Exec(`DELETE FROM sync_tasks WHERE user_id=? AND id=?`, uid, id)
	return changed(r, e)
}

func (s *Store) AddEvent(v Event) (Event, error) {
	r, e := s.DB.Exec(`INSERT INTO events(user_id,task_id,level,kind,title,detail,site_id,source_id,group_id,site_name,source_name,group_name,reason,request_id,before_rate,after_rate) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.UserID, v.TaskID, v.Level, v.Kind, v.Title, v.Detail, v.SiteID, v.SourceID, v.GroupID, v.SiteName, v.SourceName, v.GroupName, v.Reason, v.RequestID, v.BeforeRate, v.AfterRate)
	if e != nil {
		return Event{}, e
	}
	id, _ := r.LastInsertId()
	v.ID = id
	v.CreatedAt = time.Now()
	return v, nil
}

func (s *Store) AddImageObservation(v ImageObservation) (bool, error) {
	r, e := s.DB.Exec(`INSERT OR IGNORE INTO image_observations(user_id,source_id,model,size,quality,count,group_rate,unit_price,actual_cost,request_id,observed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, v.UserID, v.SourceID, v.Model, v.Size, v.Quality, v.Count, v.GroupRate, v.UnitPrice, v.ActualCost, v.RequestID, v.ObservedAt)
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	return n > 0, e
}
func (s *Store) Events(uid int64, limit int) ([]Event, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, e := s.DB.Query(`SELECT id,task_id,level,kind,title,detail,site_id,source_id,group_id,site_name,source_name,group_name,reason,request_id,before_rate,after_rate,emailed,created_at FROM events WHERE user_id=? ORDER BY id DESC LIMIT ?`, uid, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var v Event
		var mailed int
		v.UserID = uid
		if e = rows.Scan(&v.ID, &v.TaskID, &v.Level, &v.Kind, &v.Title, &v.Detail, &v.SiteID, &v.SourceID, &v.GroupID, &v.SiteName, &v.SourceName, &v.GroupName, &v.Reason, &v.RequestID, &v.BeforeRate, &v.AfterRate, &mailed, &v.CreatedAt); e != nil {
			return nil, e
		}
		v.Emailed = mailed == 1
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) PendingEmailEvents(uid int64, kinds []string) ([]Event, error) {
	if len(kinds) == 0 {
		return []Event{}, nil
	}
	marks := make([]string, len(kinds))
	args := []any{uid}
	for i, k := range kinds {
		marks[i] = "?"
		args = append(args, k)
	}
	rows, e := s.DB.Query(`SELECT id,task_id,level,kind,title,detail,site_id,source_id,group_id,site_name,source_name,group_name,reason,request_id,before_rate,after_rate,created_at FROM events WHERE user_id=? AND emailed=0 AND kind IN (`+strings.Join(marks, ",")+`) ORDER BY id LIMIT 100`, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var v Event
		v.UserID = uid
		if e = rows.Scan(&v.ID, &v.TaskID, &v.Level, &v.Kind, &v.Title, &v.Detail, &v.SiteID, &v.SourceID, &v.GroupID, &v.SiteName, &v.SourceName, &v.GroupName, &v.Reason, &v.RequestID, &v.BeforeRate, &v.AfterRate, &v.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) MarkEventsEmailed(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	p := make([]string, len(ids))
	a := make([]any, len(ids))
	for i, id := range ids {
		p[i] = "?"
		a[i] = id
	}
	_, e := s.DB.Exec(`UPDATE events SET emailed=1 WHERE id IN (`+strings.Join(p, ",")+`)`, a...)
	return e
}
func (s *Store) EmailUsers() ([]User, error) {
	rows, e := s.DB.Query(`SELECT id,email,password_hash,notify_email,email_enabled,notify_kinds,role,status,onboarded,created_at FROM users WHERE email_enabled=1 AND notify_email<>'' AND status='active'`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var enabled, onboarded int
		var kinds string
		if e = rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.NotifyEmail, &enabled, &kinds, &u.Role, &u.Status, &onboarded, &u.CreatedAt); e != nil {
			return nil, e
		}
		u.EmailEnabled = enabled == 1
		u.Onboarded = onboarded == 1
		_ = json.Unmarshal([]byte(kinds), &u.NotifyKinds)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) Dashboard(uid int64) (map[string]int64, error) {
	out := map[string]int64{}
	for k, q := range map[string]string{"sites": `SELECT count(*) FROM sites WHERE user_id=?`, "sources": `SELECT count(*) FROM sources WHERE user_id=?`, "tasks": `SELECT count(*) FROM sync_tasks WHERE user_id=? AND enabled=1`, "alerts": `SELECT count(*) FROM events WHERE user_id=? AND level IN ('error','warning') AND created_at>datetime('now','-24 hours')`} {
		var count int64
		if e := s.DB.QueryRow(q, uid).Scan(&count); e != nil {
			return nil, e
		}
		out[k] = count
	}
	return out, nil
}

func (s *Store) SystemSettings() (SystemSettings, error) {
	var v SystemSettings
	var registration int
	err := s.DB.QueryRow(`SELECT site_name,admin_path,registration_open,public_url,smtp_host,smtp_port,smtp_user,smtp_password,smtp_from,poll_seconds,probe_seconds,model_check_minutes,email_minutes FROM system_settings WHERE id=1`).Scan(&v.SiteName, &v.AdminPath, &registration, &v.PublicURL, &v.SMTPHost, &v.SMTPPort, &v.SMTPUser, &v.SMTPPassword, &v.SMTPFrom, &v.PollSeconds, &v.ProbeSeconds, &v.ModelCheckMinutes, &v.EmailMinutes)
	v.RegistrationOpen = registration == 1
	v.SMTPPasswordSet = v.SMTPPassword != ""
	return v, err
}

func (s *Store) UpdateSystemSettings(v SystemSettings, keepSMTPPassword bool) error {
	if v.SiteName == "" {
		v.SiteName = "RateWatch"
	}
	v.AdminPath = strings.Trim(strings.TrimSpace(v.AdminPath), "/")
	if v.AdminPath == "" || strings.Contains(v.AdminPath, "/") {
		return errors.New("后台路径只能是一段字母、数字或短横线")
	}
	if v.SMTPPort < 1 {
		v.SMTPPort = 587
	}
	if v.PollSeconds < 10 {
		v.PollSeconds = 10
	}
	if v.ProbeSeconds < 10 {
		v.ProbeSeconds = 10
	}
	if v.ModelCheckMinutes < 1 {
		v.ModelCheckMinutes = 1
	}
	if v.EmailMinutes < 1 {
		v.EmailMinutes = 1
	}
	if keepSMTPPassword {
		_, err := s.DB.Exec(`UPDATE system_settings SET site_name=?,admin_path=?,registration_open=?,public_url=?,smtp_host=?,smtp_port=?,smtp_user=?,smtp_from=?,poll_seconds=?,probe_seconds=?,model_check_minutes=?,email_minutes=?,updated_at=CURRENT_TIMESTAMP WHERE id=1`, v.SiteName, v.AdminPath, v.RegistrationOpen, v.PublicURL, v.SMTPHost, v.SMTPPort, v.SMTPUser, v.SMTPFrom, v.PollSeconds, v.ProbeSeconds, v.ModelCheckMinutes, v.EmailMinutes)
		return err
	}
	_, err := s.DB.Exec(`UPDATE system_settings SET site_name=?,admin_path=?,registration_open=?,public_url=?,smtp_host=?,smtp_port=?,smtp_user=?,smtp_password=?,smtp_from=?,poll_seconds=?,probe_seconds=?,model_check_minutes=?,email_minutes=?,updated_at=CURRENT_TIMESTAMP WHERE id=1`, v.SiteName, v.AdminPath, v.RegistrationOpen, v.PublicURL, v.SMTPHost, v.SMTPPort, v.SMTPUser, v.SMTPPassword, v.SMTPFrom, v.PollSeconds, v.ProbeSeconds, v.ModelCheckMinutes, v.EmailMinutes)
	return err
}

func (s *Store) AdminOverview() (map[string]any, error) {
	out := map[string]any{}
	counts := map[string]string{
		"users": `SELECT count(*) FROM users`, "sites": `SELECT count(*) FROM sites`, "sources": `SELECT count(*) FROM sources`, "tasks": `SELECT count(*) FROM sync_tasks`,
		"healthy_sources": `SELECT count(*) FROM sources WHERE monitor_state IN ('direct','newapi_probe') AND last_error=''`, "alerts_24h": `SELECT count(*) FROM events WHERE level IN ('error','warning') AND created_at>datetime('now','-24 hours')`,
		"writes_24h": `SELECT count(*) FROM events WHERE kind='rate_changed' AND created_at>datetime('now','-24 hours')`,
	}
	for key, q := range counts {
		var n int64
		if err := s.DB.QueryRow(q).Scan(&n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	var dbBytes int64
	_ = s.DB.QueryRow(`SELECT page_count*page_size FROM pragma_page_count(),pragma_page_size()`).Scan(&dbBytes)
	out["database_bytes"] = dbBytes

	trend := make([]map[string]any, 14)
	trendIndex := map[string]int{}
	for i := 0; i < 14; i++ {
		day := time.Now().AddDate(0, 0, i-13).Format("2006-01-02")
		trend[i] = map[string]any{"day": day, "success": int64(0), "warning": int64(0), "error": int64(0), "total": int64(0)}
		trendIndex[day] = i
	}
	rows, err := s.DB.Query(`SELECT date(created_at),level,count(*) FROM events WHERE created_at>=date('now','-13 days') GROUP BY date(created_at),level`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day, level string
		var count int64
		if err = rows.Scan(&day, &level, &count); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := trendIndex[day]; ok {
			trend[i][level] = count
			trend[i]["total"] = trend[i]["total"].(int64) + count
		}
	}
	rows.Close()
	out["trend"] = trend

	platforms := []map[string]any{}
	rows, err = s.DB.Query(`SELECT CASE platform WHEN 'newapi' THEN 'New API' WHEN 'sub2api' THEN 'Sub2API' ELSE platform END,count(*) FROM sources GROUP BY platform ORDER BY count(*) DESC`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var label string
		var value int64
		if err = rows.Scan(&label, &value); err != nil {
			rows.Close()
			return nil, err
		}
		platforms = append(platforms, map[string]any{"label": label, "value": value})
	}
	rows.Close()
	out["platforms"] = platforms

	health := []map[string]any{}
	rows, err = s.DB.Query(`SELECT state,count(*) FROM source_health_checks WHERE id IN (SELECT max(id) FROM source_health_checks GROUP BY source_id) GROUP BY state ORDER BY count(*) DESC`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var label string
		var value int64
		if err = rows.Scan(&label, &value); err != nil {
			rows.Close()
			return nil, err
		}
		health = append(health, map[string]any{"label": label, "value": value})
	}
	rows.Close()
	out["health"] = health

	tenants := []map[string]any{}
	rows, err = s.DB.Query(`SELECT u.email,(SELECT count(*) FROM sites WHERE user_id=u.id),(SELECT count(*) FROM sources WHERE user_id=u.id),(SELECT count(*) FROM sync_tasks WHERE user_id=u.id) FROM users u WHERE u.role='user' ORDER BY ((SELECT count(*) FROM sites WHERE user_id=u.id)+(SELECT count(*) FROM sources WHERE user_id=u.id)+(SELECT count(*) FROM sync_tasks WHERE user_id=u.id)) DESC,u.id DESC LIMIT 6`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var label string
		var sites, sources, tasks int64
		if err = rows.Scan(&label, &sites, &sources, &tasks); err != nil {
			rows.Close()
			return nil, err
		}
		tenants = append(tenants, map[string]any{"label": label, "sites": sites, "sources": sources, "tasks": tasks, "total": sites + sources + tasks})
	}
	rows.Close()
	out["tenants"] = tenants
	return out, nil
}

func (s *Store) AdminUsers(page, pageSize int, query string) (AdminUserPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 5 || pageSize > 100 {
		pageSize = 10
	}
	pattern := "%" + strings.ToLower(strings.TrimSpace(query)) + "%"
	var total int64
	if err := s.DB.QueryRow(`SELECT count(*) FROM users WHERE lower(email) LIKE ?`, pattern).Scan(&total); err != nil {
		return AdminUserPage{}, err
	}
	rows, err := s.DB.Query(`SELECT u.id,u.email,u.role,u.status,u.created_at,(SELECT count(*) FROM sites WHERE user_id=u.id),(SELECT count(*) FROM sources WHERE user_id=u.id),(SELECT count(*) FROM sync_tasks WHERE user_id=u.id),(SELECT count(*) FROM events WHERE user_id=u.id AND level IN ('error','warning')) FROM users u WHERE lower(u.email) LIKE ? ORDER BY u.id DESC LIMIT ? OFFSET ?`, pattern, pageSize, (page-1)*pageSize)
	if err != nil {
		return AdminUserPage{}, err
	}
	defer rows.Close()
	var out []AdminUser
	for rows.Next() {
		var v AdminUser
		if err = rows.Scan(&v.ID, &v.Email, &v.Role, &v.Status, &v.CreatedAt, &v.Sites, &v.Sources, &v.Tasks, &v.Alerts); err != nil {
			return AdminUserPage{}, err
		}
		out = append(out, v)
	}
	return AdminUserPage{Items: out, Total: total, Page: page, PageSize: pageSize}, rows.Err()
}

func (s *Store) AdminCreateUser(email, passwordHash, role string) (AdminUser, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if role != "admin" {
		role = "user"
	}
	r, err := s.DB.Exec(`INSERT INTO users(email,password_hash,notify_email,role,status,onboarded) VALUES(?,?,?,?, 'active',1)`, email, passwordHash, email, role)
	if err != nil {
		return AdminUser{}, err
	}
	id, _ := r.LastInsertId()
	return s.AdminUser(id)
}

func (s *Store) AdminUser(id int64) (AdminUser, error) {
	var v AdminUser
	err := s.DB.QueryRow(`SELECT u.id,u.email,u.role,u.status,u.created_at,(SELECT count(*) FROM sites WHERE user_id=u.id),(SELECT count(*) FROM sources WHERE user_id=u.id),(SELECT count(*) FROM sync_tasks WHERE user_id=u.id),(SELECT count(*) FROM events WHERE user_id=u.id AND level IN ('error','warning')) FROM users u WHERE u.id=?`, id).Scan(&v.ID, &v.Email, &v.Role, &v.Status, &v.CreatedAt, &v.Sites, &v.Sources, &v.Tasks, &v.Alerts)
	return v, err
}

func (s *Store) AdminUpdateUser(actorID, id int64, email, role, status, passwordHash string) (AdminUser, error) {
	if role != "admin" {
		role = "user"
	}
	if status != "disabled" {
		status = "active"
	}
	if actorID == id && (role != "admin" || status != "active") {
		return AdminUser{}, errors.New("不能取消当前管理员自己的权限或停用自己")
	}
	if passwordHash == "" {
		_, err := s.DB.Exec(`UPDATE users SET email=?,role=?,status=? WHERE id=?`, strings.ToLower(strings.TrimSpace(email)), role, status, id)
		if err != nil {
			return AdminUser{}, err
		}
	} else {
		_, err := s.DB.Exec(`UPDATE users SET email=?,role=?,status=?,password_hash=? WHERE id=?`, strings.ToLower(strings.TrimSpace(email)), role, status, passwordHash, id)
		if err != nil {
			return AdminUser{}, err
		}
	}
	return s.AdminUser(id)
}

func (s *Store) AdminDeleteUser(actorID, id int64) error {
	if actorID == id {
		return errors.New("不能删除当前登录的管理员")
	}
	r, err := s.DB.Exec(`DELETE FROM users WHERE id=?`, id)
	return changed(r, err)
}

func (s *Store) SeedDemoData(passwordHash string) (int64, error) {
	// Keep enough realistic tenants in the demo set for the user-management
	// pagination and aggregate charts to be exercised immediately.
	demoUsers := []struct {
		email  string
		status string
		days   int
	}{
		{"lin@northstar.example", "active", 41},
		{"chen@pixelharbor.example", "active", 38},
		{"zhou@modelbridge.example", "active", 35},
		{"tang@cloudlane.example", "disabled", 32},
		{"wu@promptworks.example", "active", 28},
		{"gao@blueorbit.example", "active", 24},
		{"he@apigarden.example", "active", 21},
		{"luo@tokenflow.example", "disabled", 18},
		{"sun@visiondock.example", "active", 14},
		{"fang@relaystack.example", "active", 11},
		{"xu@quotafox.example", "active", 7},
		{"ma@imageport.example", "active", 3},
	}
	for i, user := range demoUsers {
		if _, err := s.DB.Exec(`INSERT OR IGNORE INTO users(email,password_hash,notify_email,email_enabled,role,status,onboarded,created_at) VALUES(?,?,?,0,'user',?,1,datetime('now',?))`, user.email, passwordHash, user.email, user.status, fmt.Sprintf("-%d days", user.days)); err != nil {
			return 0, err
		}
		if i >= 8 {
			continue
		}
		var userID int64
		if err := s.DB.QueryRow(`SELECT id FROM users WHERE email=?`, user.email).Scan(&userID); err != nil {
			return 0, err
		}
		platform := "newapi"
		if i%3 == 1 {
			platform = "sub2api"
		}
		baseURL := fmt.Sprintf("https://tenant-%02d.demo.local", i+1)
		if _, err := s.DB.Exec(`INSERT OR IGNORE INTO sites(user_id,name,base_url,platform,admin_secret,status,last_imported_at,created_at) VALUES(?,?,?,?,?,'ready',datetime('now','-15 minutes'),datetime('now',?))`, userID, fmt.Sprintf("业务站点 %02d", i+1), baseURL, platform, "demo", fmt.Sprintf("-%d days", user.days-1)); err != nil {
			return 0, err
		}
		var siteID int64
		if err := s.DB.QueryRow(`SELECT id FROM sites WHERE user_id=? AND base_url=?`, userID, baseURL).Scan(&siteID); err != nil {
			return 0, err
		}
		if _, err := s.DB.Exec(`INSERT OR IGNORE INTO groups(user_id,site_id,external_id,name,rate,status) VALUES(?,?,'default','默认分组',?,'active')`, userID, siteID, 1.08+float64(i)*0.03); err != nil {
			return 0, err
		}
		var groupID int64
		if err := s.DB.QueryRow(`SELECT id FROM groups WHERE site_id=? AND external_id='default'`, siteID).Scan(&groupID); err != nil {
			return 0, err
		}
		var firstSourceID int64
		for j := 0; j < i%3+1; j++ {
			fingerprint := fmt.Sprintf("demo-tenant-%d-%d", i, j)
			if _, err := s.DB.Exec(`INSERT OR IGNORE INTO sources(user_id,name,base_url,platform,secret,fingerprint,monitor_state,probe_model,models_json,last_rate,last_checked_at,created_at) VALUES(?,?,?,?,?,?,?,'gpt-4o-mini','["gpt-4o-mini"]',?,datetime('now','-12 minutes'),datetime('now',?))`, userID, fmt.Sprintf("业务上游 %d", j+1), fmt.Sprintf("https://upstream-%02d-%d.demo.local", i+1, j+1), platform, "demo", fingerprint, map[bool]string{true: "direct", false: "newapi_probe"}[platform == "sub2api"], 1.0+float64(i+j)*0.025, fmt.Sprintf("-%d days", user.days-2)); err != nil {
				return 0, err
			}
			if j == 0 {
				if err := s.DB.QueryRow(`SELECT id FROM sources WHERE user_id=? AND fingerprint=?`, userID, fingerprint).Scan(&firstSourceID); err != nil {
					return 0, err
				}
			}
		}
		if _, err := s.DB.Exec(`INSERT OR IGNORE INTO sync_tasks(user_id,name,source_id,site_id,group_id,adjustment,enabled,last_upstream_rate,last_target_rate,last_status,last_run_at,created_at) VALUES(?,?,?,?,?,0.15,0,?,?,'ok',datetime('now','-20 minutes'),datetime('now',?))`, userID, "默认分组倍率同步", firstSourceID, siteID, groupID, 1.0+float64(i)*0.025, 1.15+float64(i)*0.025, fmt.Sprintf("-%d days", user.days-2)); err != nil {
			return 0, err
		}
	}

	var existing int64
	if err := s.DB.QueryRow(`SELECT id FROM users WHERE email='demo@ratewatch.local'`).Scan(&existing); err == nil {
		return existing, nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	r, err := tx.Exec(`INSERT INTO users(email,password_hash,notify_email,email_enabled,role,status,onboarded,created_at) VALUES('demo@ratewatch.local',?,'demo@ratewatch.local',0,'user','active',1,datetime('now','-45 days'))`, passwordHash)
	if err != nil {
		return 0, err
	}
	uid, _ := r.LastInsertId()
	r, err = tx.Exec(`INSERT INTO sites(user_id,name,base_url,platform,admin_secret,status,last_imported_at,created_at) VALUES(?, '演示主站','https://main.demo.local','newapi','demo','ready',datetime('now','-5 minutes'),datetime('now','-40 days'))`, uid)
	if err != nil {
		return 0, err
	}
	mainSite, _ := r.LastInsertId()
	r, err = tx.Exec(`INSERT INTO sites(user_id,name,base_url,platform,admin_secret,admin_header,status,last_imported_at,created_at) VALUES(?, '演示备用站','https://backup.demo.local','sub2api','demo','x-api-key','ready',datetime('now','-8 minutes'),datetime('now','-32 days'))`, uid)
	if err != nil {
		return 0, err
	}
	backupSite, _ := r.LastInsertId()
	groupIDs := []int64{}
	for _, g := range []struct {
		site           int64
		external, name string
		rate           float64
	}{{mainSite, "default", "默认分组", 1.25}, {mainSite, "vip", "会员分组", 1.08}, {backupSite, "101", "绘图分组", 1.4}} {
		r, err = tx.Exec(`INSERT INTO groups(user_id,site_id,external_id,name,rate,status) VALUES(?,?,?,?,?,'active')`, uid, g.site, g.external, g.name, g.rate)
		if err != nil {
			return 0, err
		}
		id, _ := r.LastInsertId()
		groupIDs = append(groupIDs, id)
	}
	sourceIDs := []int64{}
	for i, src := range []struct {
		name, url, platform, state string
		rate                       float64
	}{{"文本低价上游", "https://text.demo.local", "newapi", "newapi_probe", 1.05}, {"稳定备用上游", "https://stable.demo.local", "sub2api", "direct", 1.12}, {"生图供应商", "https://image.demo.local", "newapi", "newapi_probe", 1.28}, {"临时异常上游", "https://alert.demo.local", "sub2api", "check_failed", 1.18}} {
		r, err = tx.Exec(`INSERT INTO sources(user_id,name,base_url,platform,secret,fingerprint,monitor_state,probe_model,models_json,last_rate,last_error,last_checked_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,datetime('now',?),datetime('now','-30 days'))`, uid, src.name, src.url, src.platform, "demo", fmt.Sprintf("demo-%d", i), src.state, "gpt-4o-mini", `["gpt-4o-mini","gpt-4.1-mini"]`, src.rate, map[bool]string{true: "上游暂时无法访问", false: ""}[src.state == "check_failed"], fmt.Sprintf("-%d minutes", i*7+2))
		if err != nil {
			return 0, err
		}
		id, _ := r.LastInsertId()
		sourceIDs = append(sourceIDs, id)
	}
	for i, sourceID := range sourceIDs {
		for j := 0; j < 20; j++ {
			state := "healthy"
			message := "连接正常，倍率无变化"
			if i == 0 && j > 15 {
				state = "synced"
				message = "倍率变化已同步"
			}
			if i == 3 && j > 16 {
				state = "failed"
				message = "连接超时"
			}
			_, err = tx.Exec(`INSERT INTO source_health_checks(user_id,source_id,state,message,rate,created_at) VALUES(?,?,?,?,?,datetime('now',?))`, uid, sourceID, state, message, 1.05+float64(i)*.06, fmt.Sprintf("-%d hours", (19-j)*6))
			if err != nil {
				return 0, err
			}
		}
	}
	for i, groupID := range groupIDs {
		sourceID := sourceIDs[i%len(sourceIDs)]
		_, err = tx.Exec(`INSERT INTO sync_tasks(user_id,name,source_id,site_id,group_id,adjustment,enabled,last_upstream_rate,last_target_rate,last_status,last_run_at,created_at) SELECT ?,?, ?,site_id,?, ?,0,?,?,?,datetime('now','-20 minutes'),datetime('now','-25 days') FROM groups WHERE id=?`, uid, fmt.Sprintf("演示同步任务 %d", i+1), sourceID, groupID, .15, 1.05+float64(i)*.06, 1.20+float64(i)*.06, "ok", groupID)
		if err != nil {
			return 0, err
		}
	}
	levels := []string{"success", "success", "warning", "success", "error", "info"}
	for i := 0; i < 42; i++ {
		level := levels[i%len(levels)]
		kind, title := "rate_changed", "倍率同步完成"
		if level == "warning" {
			kind = "large_change"
			title = "发现较大倍率变化"
		}
		if level == "error" {
			kind = "probe_failed"
			title = "上游检查失败"
		}
		if level == "info" {
			kind = "image_observed"
			title = "记录生图实际价格"
		}
		before := 1.0 + float64(i%4)*.05
		after := before + .15
		_, err = tx.Exec(`INSERT INTO events(user_id,level,kind,title,detail,site_id,source_id,group_id,site_name,source_name,group_name,reason,before_rate,after_rate,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,datetime('now',?))`, uid, level, kind, title, "演示数据：用于查看统计面板与日志展示", mainSite, sourceIDs[i%len(sourceIDs)], groupIDs[i%len(groupIDs)], "演示主站", []string{"文本低价上游", "稳定备用上游", "生图供应商", "临时异常上游"}[i%4], []string{"默认分组", "会员分组", "绘图分组"}[i%3], "演示运行记录", before, after, fmt.Sprintf("-%d hours", i*8))
		if err != nil {
			return 0, err
		}
	}
	return uid, tx.Commit()
}

func trimURL(v string) string { return strings.TrimRight(strings.TrimSpace(v), "/") }
func changed(r sql.Result, e error) error {
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

var ErrConflict = errors.New("conflict")
