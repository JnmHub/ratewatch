package store

import (
	"path/filepath"
	"testing"
)

func TestSourceFingerprintUniquePerTenant(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "fingerprint.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u1, err := st.CreateUser("one@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	u2, err := st.CreateUser("two@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	base := Source{Name: "one", BaseURL: "https://upstream.example.com", Platform: "sub2api", Secret: "encrypted", Fingerprint: "same", MonitorState: "direct"}
	base.UserID = u1.ID
	if _, err = st.CreateSource(base); err != nil {
		t.Fatal(err)
	}
	base.Name = "duplicate"
	if _, err = st.CreateSource(base); err == nil {
		t.Fatal("same tenant accepted a duplicate URL+Key fingerprint")
	}
	base.Name = "different-key"
	base.Fingerprint = "different"
	if _, err = st.CreateSource(base); err != nil {
		t.Fatalf("same URL with a different Key should be allowed: %v", err)
	}
	base.UserID = u2.ID
	base.Name = "other-tenant"
	base.Fingerprint = "same"
	if _, err = st.CreateSource(base); err != nil {
		t.Fatalf("fingerprints must be tenant scoped: %v", err)
	}
}

func TestFailedSourceCheckPreservesLastSuccessfulRate(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "last-rate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, err := st.CreateUser("rate@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	rate := 1.25
	source, err := st.CreateSource(Source{UserID: u.ID, Name: "source", BaseURL: "https://upstream.example.com", Platform: "newapi", Secret: "encrypted", Fingerprint: "rate", MonitorState: "newapi_probe", LastRate: &rate})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.UpdateSourceCheck(u.ID, source.ID, "check_failed", nil, "temporary failure"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Source(u.ID, source.ID)
	if err != nil || got.LastRate == nil || *got.LastRate != rate {
		t.Fatalf("source=%+v err=%v", got, err)
	}
}

func TestOneSourceCanUseDifferentRulesForDifferentGroups(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "group-rules.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	user, err := st.CreateUser("groups@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	site, err := st.CreateSite(Site{UserID: user.ID, Name: "target", BaseURL: "https://target.example.com", Platform: "sub2api", AdminSecret: "encrypted", AdminHeader: "x-api-key"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.ReplaceInventory(user.ID, site.ID, []Group{
		{ExternalID: "group-a", Name: "A", Rate: 1, Status: "active"},
		{ExternalID: "group-b", Name: "B", Rate: 1, Status: "active"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	groups, err := st.Inventory(user.ID, site.ID)
	if err != nil || len(groups) != 2 {
		t.Fatalf("groups=%+v err=%v", groups, err)
	}
	source, err := st.CreateSource(Source{UserID: user.ID, Name: "upstream", BaseURL: "https://upstream.example.com", Platform: "sub2api", Secret: "encrypted", Fingerprint: "source", MonitorState: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.CreateTask(Task{UserID: user.ID, Name: "A rule", SourceIDs: []int64{source.ID}, SiteID: site.ID, GroupID: groups[0].ID, Adjustment: .1, MinUpstreamRate: .8, Enabled: true, LargeChangePct: 50})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateTask(Task{UserID: user.ID, Name: "B rule", SourceIDs: []int64{source.ID}, SiteID: site.ID, GroupID: groups[1].ID, Adjustment: .3, MinUpstreamRate: 1.2, Enabled: true, LargeChangePct: 50})
	if err != nil {
		t.Fatal(err)
	}
	if first.MinUpstreamRate != .8 || first.Adjustment != .1 || second.MinUpstreamRate != 1.2 || second.Adjustment != .3 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	second.MinUpstreamRate = 1.4
	second.Adjustment = -.1
	updated, err := st.UpdateTask(second)
	if err != nil || updated.MinUpstreamRate != 1.4 || updated.Adjustment != -.1 {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
}

func TestInventoryReadsSourceCheckTimestampFromCoalesce(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "inventory-time.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	user, err := st.CreateUser("inventory-time@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	site, err := st.CreateSite(Site{UserID: user.ID, Name: "target", BaseURL: "https://target.example.com", Platform: "sub2api", AdminSecret: "encrypted", AdminHeader: "x-api-key"})
	if err != nil {
		t.Fatal(err)
	}
	upstreamURL := "https://upstream.example.com"
	if err = st.ReplaceInventory(user.ID, site.ID,
		[]Group{{ExternalID: "group", Name: "group", Rate: 1, Status: "active"}},
		[]Account{{ExternalID: "account", Name: "account", Platform: "sub2api", BaseURL: upstreamURL, RelationGroups: []string{"group"}}},
	); err != nil {
		t.Fatal(err)
	}
	source, err := st.CreateSource(Source{UserID: user.ID, Name: "source", BaseURL: upstreamURL, Platform: "sub2api", Secret: "encrypted", Fingerprint: "timestamp", MonitorState: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.UpdateSourceCheck(user.ID, source.ID, "direct", nil, ""); err != nil {
		t.Fatal(err)
	}
	groups, err := st.Inventory(user.ID, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0].Accounts) != 1 || groups[0].Accounts[0].LastCheckedAt == nil {
		t.Fatalf("inventory=%+v", groups)
	}
}

func TestMigrationClearsSuccessMessagesStoredAsErrors(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "success-message.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	user, err := st.CreateUser("success-message@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	source, err := st.CreateSource(Source{UserID: user.ID, Name: "source", BaseURL: "https://upstream.example.com", Platform: "sub2api", Secret: "encrypted", Fingerprint: "success-message", MonitorState: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.UpdateSourceCheck(user.ID, source.ID, "direct", nil, "普通 Key 可直接读取生效倍率"); err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	source, err = st.Source(user.ID, source.ID)
	if err != nil || source.LastError != "" {
		t.Fatalf("source=%+v err=%v", source, err)
	}
}
