// SPDX-License-Identifier: AGPL-3.0-or-later
// Package recovery rehearses recovery on synthetic, disposable databases only.
package recovery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/migrations"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/postgres"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed testdata/*.sql
var fixtures embed.FS

type table struct {
	Name    string
	Columns []string
}
type manifest struct {
	Tables []table
	Rows   map[string][]string
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func openDB(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	must(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
func script(t *testing.T, db *sql.DB, raw string) {
	t.Helper()
	// Fixtures and embedded migrations contain no procedural SQL / literal semicolons.
	var clean []string
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			clean = append(clean, strings.SplitN(line, "--", 2)[0])
		}
	}
	for _, stmt := range strings.Split(strings.Join(clean, "\n"), ";") {
		if strings.TrimSpace(stmt) != "" {
			_, err := db.Exec(stmt)
			must(t, err)
		}
	}
}
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, e := fixtures.ReadFile("testdata/" + name)
	must(t, e)
	return string(b)
}
func inventory(t *testing.T, db *sql.DB, pg bool) []table {
	t.Helper()
	q := `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`
	if pg {
		q = `SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename`
	}
	rows, e := db.Query(q)
	must(t, e)
	var names []string
	for rows.Next() {
		var name string
		must(t, rows.Scan(&name))
		names = append(names, name)
	}
	must(t, rows.Err())
	must(t, rows.Close())
	var out []table
	for _, name := range names {
		r, e := db.Query(`SELECT * FROM "` + name + `" LIMIT 0`)
		must(t, e)
		cols, e := r.Columns()
		must(t, e)
		must(t, r.Close())
		out = append(out, table{name, cols})
	}
	return out
}
func snapshot(t *testing.T, db *sql.DB, tables []table) manifest {
	t.Helper()
	m := manifest{tables, map[string][]string{}}
	for _, tab := range tables {
		cols := make([]string, len(tab.Columns))
		for i, c := range tab.Columns {
			cols[i] = `"` + c + `"`
		}
		rows, e := db.Query(`SELECT ` + strings.Join(cols, ",") + ` FROM "` + tab.Name + `"`)
		must(t, e)
		records := []string{}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			must(t, rows.Scan(ptrs...))
			for i, v := range vals {
				if b, ok := v.([]byte); ok {
					vals[i] = string(b)
				}
			}
			b, e := json.Marshal(vals)
			must(t, e)
			records = append(records, string(b))
		}
		must(t, rows.Err())
		must(t, rows.Close())
		sort.Strings(records)
		m.Rows[tab.Name] = records
	}
	return m
}
func equal(t *testing.T, want, got manifest) {
	t.Helper()
	a, e := json.Marshal(want)
	must(t, e)
	b, e := json.Marshal(got)
	must(t, e)
	if string(a) != string(b) {
		t.Fatal("logical recovery manifest differs (contents withheld; inspect synthetic fixture)")
	}
	t.Logf("verified %d tables; logical SHA256 %x", len(want.Tables), sha256.Sum256(a))
}
func verifyDomain(t *testing.T, s store.Store, legacy bool) {
	t.Helper()
	ctx := context.Background()
	r, e := s.GetRosterEntry(ctx, "learner")
	must(t, e)
	want := "200"
	if legacy {
		want = ""
	}
	if r.HostUserID != want {
		t.Fatalf("identity changed: got %q want %q", r.HostUserID, want)
	}
	g, e := s.LatestGradeForSubmission(ctx, "submission")
	must(t, e)
	if g.Score != 8 || g.MaxScore != 10 || g.ExportConfirmedAt == nil {
		t.Fatal("grade/export receipt not preserved")
	}
	c, e := s.GetCalibration(ctx, "calibration", "teacher")
	must(t, e)
	if c.Status != "ready" || c.Revision != 2 {
		t.Fatal("calibration changed")
	}
	a, e := s.GetAssessment(ctx, "assessment")
	must(t, e)
	if a.Status != "approved" || a.GradeID != "grade" {
		t.Fatal("reviewed assessment changed")
	}
	if !legacy {
		audit, e := s.(store.ReconciliationStore).ListIdentityReconciliations(ctx)
		must(t, e)
		if len(audit) != 1 || audit[0].PlanID != "fixture-plan" || audit[0].ProviderID != "200" {
			t.Fatal("identity reconciliation audit lost")
		}
		for _, id := range []string{"link", "binding", "delivery"} {
			r, e := s.GetLTIRecord(ctx, id)
			must(t, e)
			if r.Revision != 1 {
				t.Fatal("LTI receipt revision changed")
			}
		}
	}
}
func currentRecords(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	r, e := s.GetRosterEntry(ctx, "learner")
	must(t, e)
	reconciler := s.(store.ReconciliationStore)
	_, e = reconciler.BindLegacyIdentity(ctx, store.IdentityReconciliation{PlanID: "fixture-plan", RosterID: r.ID, Snapshot: store.RosterSnapshot(r), ProviderID: "200", Operator: "teacher", EvidenceSHA256: strings.Repeat("a", 64), AppliedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)})
	must(t, e)
	for _, r := range []*store.LTIRecord{
		{ID: "link", Kind: "link", UniqueKey: "fixture-line", AssignmentID: "assignment", Revision: 1, Document: []byte(`{"line_item":"https://lms.example.invalid/line"}`)},
		{ID: "binding", Kind: "binding", UniqueKey: "fixture-subject", AssignmentID: "assignment", RosterEntryID: "learner", Revision: 1, Document: []byte(`{"subject":"opaque-learner"}`)},
		{ID: "delivery", Kind: "delivery", UniqueKey: "fixture-delivery", AssignmentID: "assignment", RosterEntryID: "learner", GradeID: "grade", Revision: 1, Document: []byte(`{"status":"uncertain","attempts":1,"timestamp":"2026-10-01T12:00:00Z"}`)},
	} {
		must(t, s.SaveLTIRecord(context.Background(), r, 0))
	}
}
func sqliteBackup(t *testing.T, db *sql.DB, path string) {
	t.Helper()
	_, e := db.Exec(`VACUUM INTO ?`, path)
	must(t, e)
}
func sqliteRestore(t *testing.T, backup, target string) {
	t.Helper()
	b, e := os.ReadFile(backup)
	must(t, e)
	f, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	must(t, e)
	_, e = f.Write(b)
	must(t, e)
	must(t, f.Close())
}
func checkSQLite(t *testing.T, db *sql.DB) {
	t.Helper()
	var result string
	must(t, db.QueryRow("PRAGMA integrity_check").Scan(&result))
	if result != "ok" {
		t.Fatal(result)
	}
	rows, e := db.Query("PRAGMA foreign_key_check")
	must(t, e)
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation after restore")
	}
	must(t, rows.Err())
}
func TestSQLiteRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.db")
	db := openDB(t, "sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	script(t, db, fixture(t, "sqlite-pre-brightspace.sql"))
	script(t, db, fixture(t, "coursework.sql"))
	legacy := snapshot(t, db, inventory(t, db, false))
	oldBackup := filepath.Join(dir, "pre-upgrade.db")
	sqliteBackup(t, db, oldBackup)
	s, e := sqlite.Open(path)
	must(t, e)
	verifyDomain(t, s, true)
	equal(t, legacy, snapshot(t, db, legacy.Tables))
	must(t, s.Close())
	// A second startup must preserve populated data (idempotent migrations).
	s, e = sqlite.Open(path)
	must(t, e)
	defer s.Close()
	verifyDomain(t, s, true)
	rollback := filepath.Join(dir, "rollback.db")
	sqliteRestore(t, oldBackup, rollback)
	rdb := openDB(t, "sqlite", rollback)
	equal(t, legacy, snapshot(t, rdb, inventory(t, rdb, false)))
	checkSQLite(t, rdb)
	rs, e := sqlite.Open(rollback)
	must(t, e)
	verifyDomain(t, rs, true)
	must(t, rs.Close())
	currentRecords(t, s)
	want := snapshot(t, db, inventory(t, db, false))
	backup := filepath.Join(dir, "current.db")
	sqliteBackup(t, db, backup)
	// Source advances after snapshot. The backup must remain at the captured point.
	_, e = db.Exec(`UPDATE grades SET score=9 WHERE id='grade'`)
	must(t, e)
	restored := filepath.Join(dir, "restored.db")
	sqliteRestore(t, backup, restored)
	fresh, e := sqlite.Open(restored)
	must(t, e)
	defer fresh.Close()
	fdb := openDB(t, "sqlite", restored)
	equal(t, want, snapshot(t, fdb, inventory(t, fdb, false)))
	verifyDomain(t, fresh, false)
	checkSQLite(t, fdb)
	// A failed backup never destroys an existing recovery point.
	before, e := os.ReadFile(backup)
	must(t, e)
	_, e = db.Exec(`VACUUM INTO ?`, backup)
	if e == nil {
		t.Fatal("backup unexpectedly overwrote existing destination")
	}
	after, e := os.ReadFile(backup)
	must(t, e)
	if string(before) != string(after) {
		t.Fatal("failed backup changed recovery point")
	}
	// An incomplete artifact must fail integrity validation, never be promoted.
	corrupt := filepath.Join(dir, "truncated.db")
	must(t, os.WriteFile(corrupt, before[:len(before)/2], 0600))
	bad := openDB(t, "sqlite", corrupt)
	var result string
	e = bad.QueryRow("PRAGMA integrity_check").Scan(&result)
	if e == nil && result == "ok" {
		t.Fatal("truncated backup accepted")
	}
}

func docker(t *testing.T, input []byte, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	if input != nil {
		cmd.Stdin = strings.NewReader(string(input))
	}
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("docker %s: %v: %s", args[0], e, out)
	}
	return out
}
func TestPostgresRecovery(t *testing.T) {
	if os.Getenv("CAIRN_RECOVERY_POSTGRES") != "1" {
		t.Skip("set CAIRN_RECOVERY_POSTGRES=1 to start a disposable Docker PostgreSQL container")
	}
	name := fmt.Sprintf("cairn-recovery-%d", time.Now().UnixNano())
	docker(t, nil, "run", "--detach", "--rm", "--name", name, "--label", "org.educloud.cairn.recovery-drill=true", "-e", "POSTGRES_PASSWORD=synthetic-recovery-only", "-p", "127.0.0.1::5432", "postgres:17-alpine")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, e := exec.CommandContext(ctx, "docker", "rm", "-f", name).CombinedOutput()
		if e != nil {
			t.Errorf("cleanup %s: %v %s", name, e, out)
		}
	})
	address := strings.TrimSpace(string(docker(t, nil, "port", name, "5432/tcp")))
	dsn := "postgres://postgres:synthetic-recovery-only@" + address + "/postgres?sslmode=disable&default_query_exec_mode=simple_protocol"
	db := openDB(t, "pgx", dsn)
	deadline := time.Now().Add(45 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		e := db.PingContext(ctx)
		cancel()
		if e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disposable PostgreSQL did not become ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	files, e := migrations.FS.ReadDir(".")
	must(t, e)
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".up.sql") && file.Name() < "0009" {
			raw, e := migrations.FS.ReadFile(file.Name())
			must(t, e)
			script(t, db, string(raw))
		}
	}
	script(t, db, fixture(t, "coursework.sql"))
	legacy := snapshot(t, db, inventory(t, db, true))
	backup := docker(t, nil, "exec", name, "pg_dump", "-U", "postgres", "--format=custom", "--no-owner", "postgres")
	s := postgres.New(db)
	_, e = db.Exec(`CREATE TABLE lti_records (id TEXT)`)
	must(t, e)
	beforeFailure := snapshot(t, db, inventory(t, db, true))
	if e := s.Migrate(context.Background()); e == nil {
		t.Fatal("malformed PostgreSQL schema should reject upgrade")
	}
	equal(t, beforeFailure, snapshot(t, db, inventory(t, db, true)))
	_, e = db.Exec(`DROP TABLE lti_records`)
	must(t, e)
	must(t, s.Migrate(context.Background()))
	equal(t, legacy, snapshot(t, db, legacy.Tables))
	verifyDomain(t, s, true)
	must(t, s.Migrate(context.Background()))
	restore := func(database string, data []byte) *sql.DB {
		docker(t, nil, "exec", name, "createdb", "-U", "postgres", database)
		docker(t, data, "exec", "-i", name, "pg_restore", "-U", "postgres", "--exit-on-error", "--single-transaction", "--no-owner", "-d", database)
		return openDB(t, "pgx", strings.Replace(dsn, "/postgres?", "/"+database+"?", 1))
	}
	old := restore("rollback", backup)
	equal(t, legacy, snapshot(t, old, inventory(t, old, true)))
	oldStore := postgres.New(old)
	must(t, oldStore.Migrate(context.Background()))
	verifyDomain(t, oldStore, true)
	currentRecords(t, s)
	want := snapshot(t, db, inventory(t, db, true))
	current := docker(t, nil, "exec", name, "pg_dump", "-U", "postgres", "--format=custom", "--no-owner", "postgres")
	_, e = db.Exec(`UPDATE grades SET score=9 WHERE id='grade'`)
	must(t, e)
	fresh := restore("restored", current)
	freshStore := postgres.New(fresh)
	must(t, freshStore.Migrate(context.Background()))
	equal(t, want, snapshot(t, fresh, inventory(t, fresh, true)))
	verifyDomain(t, freshStore, false)
	// Failed restore is transactional: no partially restored data is promoted.
	docker(t, nil, "exec", name, "createdb", "-U", "postgres", "broken")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", name, "pg_restore", "-U", "postgres", "--exit-on-error", "--single-transaction", "--no-owner", "-d", "broken")
	cmd.Stdin = strings.NewReader(string(current[:len(current)/2]))
	if out, e := cmd.CombinedOutput(); e == nil {
		t.Fatalf("truncated dump accepted: %s", out)
	}
	broken := openDB(t, "pgx", strings.Replace(dsn, "/postgres?", "/broken?", 1))
	if tables := inventory(t, broken, true); len(tables) != 0 {
		t.Fatal("failed restore left partial tables")
	}
	// Cancelled migration fails without changing the restored logical state.
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if e := freshStore.Migrate(cancelled); e == nil {
		t.Fatal("cancelled migration succeeded")
	}
	equal(t, want, snapshot(t, fresh, want.Tables))
}

func TestInitialSQLiteUpgradeAndFailedMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "initial.db")
	db := openDB(t, "sqlite", path)
	script(t, db, fixture(t, "sqlite-initial.sql"))
	// The first released schema predates assessment/calibration and export receipts.
	base := strings.Split(fixture(t, "coursework.sql"), "INSERT INTO assessment_rubrics")[0]
	base = strings.Replace(base, ",export_confirmed_at", "", 1)
	base = strings.Replace(base, ",'2026-10-01T13:00:00Z'", "", 1)
	script(t, db, base)
	old := snapshot(t, db, inventory(t, db, false))
	backup := filepath.Join(t.TempDir(), "initial-backup.db")
	sqliteBackup(t, db, backup)
	// Force failure after earlier schema statements have run. New indexes cannot
	// index this deliberately malformed LTI table. Transaction must roll back.
	_, e := db.Exec(`CREATE TABLE lti_records (id TEXT)`)
	must(t, e)
	beforeFailure := snapshot(t, db, inventory(t, db, false))
	if s, e := sqlite.Open(path); e == nil {
		_ = s.Close()
		t.Fatal("malformed schema should reject upgrade")
	}
	equal(t, beforeFailure, snapshot(t, db, inventory(t, db, false)))
	_, e = db.Exec(`DROP TABLE lti_records`)
	must(t, e)
	s, e := sqlite.Open(path)
	must(t, e)
	defer s.Close()
	equal(t, old, snapshot(t, db, old.Tables))
	g, e := s.LatestGradeForSubmission(context.Background(), "submission")
	must(t, e)
	if g.Score != 8 || g.ExportConfirmedAt != nil {
		t.Fatal("legacy grade lost or marked exported")
	}
	r, e := s.GetRosterEntry(context.Background(), "learner")
	must(t, e)
	if r.HostUserID != "" {
		t.Fatal("legacy identity inferred during migration")
	}
	// Verify pre-upgrade backup can still start as a fresh current instance.
	restored := filepath.Join(t.TempDir(), "restored.db")
	sqliteRestore(t, backup, restored)
	rs, e := sqlite.Open(restored)
	must(t, e)
	defer rs.Close()
	rg, e := rs.LatestGradeForSubmission(context.Background(), "submission")
	must(t, e)
	if rg.Score != 8 || rg.ExportConfirmedAt != nil {
		t.Fatal("initial backup failed forward recovery")
	}
}
