package data

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsarmaonline/bopper/internal/stack"
)

func TestDetectFindsPostgresAndItsCredentials(t *testing.T) {
	st := stack.Stack{Services: []stack.Service{
		{Name: "web", Image: "nginx"},
		{Name: "db", Image: "postgres:16-alpine", Env: map[string]string{
			"POSTGRES_USER": "app", "POSTGRES_PASSWORD": "s3cret", "POSTGRES_DB": "shop",
		}},
	}}
	got := Detect(st)
	if len(got) != 1 {
		t.Fatalf("detected %d databases, want 1", len(got))
	}
	d := got[0]
	if d.Engine != Postgres || d.User != "app" || d.Password != "s3cret" || d.Name != "shop" {
		t.Fatalf("detected %+v", d)
	}
}

// The official image defaults matter: a compose file often sets only a password.
func TestDetectAppliesPostgresDefaults(t *testing.T) {
	st := stack.Stack{Services: []stack.Service{
		{Name: "db", Image: "postgres:16", Env: map[string]string{"POSTGRES_PASSWORD": "dev"}},
	}}
	d := Detect(st)[0]
	if d.User != "postgres" || d.Name != "postgres" {
		t.Fatalf("defaults wrong: %+v", d)
	}
}

func TestDetectIgnoresServicesThatAreNotDatabases(t *testing.T) {
	st := stack.Stack{Services: []stack.Service{
		{Name: "cache", Image: "redis:7-alpine"},
		{Name: "web", Image: "nginx"},
	}}
	if got := Detect(st); len(got) != 0 {
		t.Fatalf("detected %+v", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationsDifferDetectsEveryShapeOfChange(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(base, ws string)
		wantDiff bool
	}{
		{"identical", func(b, w string) {}, false},
		{"added file", func(b, w string) {
			writeFile(t, filepath.Join(w, "migrations", "002.sql"), "ALTER TABLE x ADD y int;")
		}, true},
		{"edited file", func(b, w string) {
			writeFile(t, filepath.Join(w, "migrations", "001.sql"), "CREATE TABLE z ();")
		}, true},
		{"deleted file", func(b, w string) {
			os.Remove(filepath.Join(w, "migrations", "001.sql"))
		}, true},
		{"unrelated source change", func(b, w string) {
			writeFile(t, filepath.Join(w, "app", "main.go"), "package main // v2")
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			base, ws := filepath.Join(root, "base"), filepath.Join(root, "ws")
			for _, d := range []string{base, ws} {
				writeFile(t, filepath.Join(d, "migrations", "001.sql"), "CREATE TABLE x ();")
				writeFile(t, filepath.Join(d, "app", "main.go"), "package main")
			}
			c.mutate(base, ws)
			got, _, err := MigrationsDiffer(base, ws)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.wantDiff {
				t.Fatalf("differ = %v, want %v", got, c.wantDiff)
			}
		})
	}
}

// A project that keeps no migrations at all must not be reported as differing,
// or every workspace would clone for no reason.
func TestNoMigrationsMeansNoDifference(t *testing.T) {
	root := t.TempDir()
	base, ws := filepath.Join(root, "base"), filepath.Join(root, "ws")
	writeFile(t, filepath.Join(base, "app.go"), "x")
	writeFile(t, filepath.Join(ws, "app.go"), "y")
	got, _, err := MigrationsDiffer(base, ws)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("a project with no migrations must not report a difference")
	}
}

func TestDecideSharesByDefaultAndClonesOnMigrationChange(t *testing.T) {
	db := DB{Service: "db", Engine: Postgres, User: "app", Password: "p", Name: "shop"}

	share := Decide(db, "feat", false, "", Auto)
	if share.Clone || !share.ReadOnly {
		t.Fatalf("matching migrations should share read-only, got %+v", share)
	}
	clone := Decide(db, "feat", true, "migrations", Auto)
	if !clone.Clone || clone.Target != "bopper_feat" {
		t.Fatalf("differing migrations should clone, got %+v", clone)
	}
	// A hyphen is legal in a workspace ID and not in an unquoted identifier.
	if got := Decide(db, "feature-x", true, "m", Auto).Target; got != "bopper_feature_x" {
		t.Fatalf("target = %q, want bopper_feature_x", got)
	}
}

func TestDecideHonoursOverrides(t *testing.T) {
	db := DB{Engine: Postgres, Name: "shop"}
	if p := Decide(db, "f", false, "", Isolate); !p.Clone {
		t.Fatal("-isolate-db must clone even when migrations match")
	}
	// The dangerous one: writes on shared data, only on an explicit request.
	p := Decide(db, "f", true, "m", Share)
	if p.Clone || p.ReadOnly || !p.Writable {
		t.Fatalf("-share-db must share WITH writes even when migrations differ, got %+v", p)
	}
}

// Bopper does not invent variables an application never reads. It always
// exports its own, and additionally rewrites a name the project already uses.
func TestEnvForOnlyOverridesVariablesTheProjectUses(t *testing.T) {
	db := DB{Engine: Postgres, User: "app", Password: "p", Name: "shop"}
	plan := Decide(db, "feat", false, "", Auto)

	bare := EnvFor(plan, "db", map[string]string{"UNRELATED": "1"}, "ro-pass")
	for _, kv := range bare {
		if !strings.HasPrefix(kv[0], "BOPPER_") {
			t.Fatalf("invented variable %q for a project that does not use it", kv[0])
		}
	}

	with := EnvFor(plan, "db", map[string]string{
		"DATABASE_URL": "postgres://app:p@db:5432/shop?sslmode=disable",
	}, "ro-pass")
	var got string
	for _, kv := range with {
		if kv[0] == "DATABASE_URL" {
			got = kv[1]
		}
	}
	if !strings.Contains(got, ReadOnlyRole) || !strings.Contains(got, "/shop") {
		t.Fatalf("DATABASE_URL = %q, want the read-only role against shop", got)
	}
	if !strings.Contains(got, "sslmode=disable") {
		t.Fatalf("query options were dropped: %q", got)
	}
}

func TestEnvForPointsAtTheCloneWhenCloning(t *testing.T) {
	db := DB{Engine: Postgres, User: "app", Password: "p", Name: "shop"}
	plan := Decide(db, "feat", true, "migrations", Auto)
	vars := EnvFor(plan, "db", map[string]string{"DB_NAME": "shop"}, "ro")
	found := map[string]string{}
	for _, kv := range vars {
		found[kv[0]] = kv[1]
	}
	if found["BOPPER_DB_NAME"] != "bopper_feat" || found["DB_NAME"] != "bopper_feat" {
		t.Fatalf("clone name not wired: %v", found)
	}
	// A clone is the workspace's own, so it keeps full credentials.
	if strings.Contains(found["BOPPER_DB_URL"], ReadOnlyRole) {
		t.Fatalf("a clone must not be read-only: %q", found["BOPPER_DB_URL"])
	}
}

// SQL must never travel through a shell: "DO $$" once became "DO 81" because
// the shell expanded $$ to its PID.
func TestSQLIsPassedWithoutAShell(t *testing.T) {
	var gotArgs []string
	var gotEnv []string
	exec := func(ctx context.Context, container string, env []string, args ...string) (string, error) {
		gotArgs, gotEnv = args, env
		return "", nil
	}
	db := DB{Engine: Postgres, User: "app", Password: "p", Name: "shop"}
	if err := EnsureReadOnly(context.Background(), exec, "c", db, "ro"); err != nil {
		t.Fatal(err)
	}
	if gotArgs[0] == "sh" {
		t.Fatal("SQL was routed through a shell")
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "DO $$") {
		t.Fatalf("the DO block did not survive: %s", joined)
	}
	if !strings.Contains(strings.Join(gotEnv, " "), "PGPASSWORD=p") {
		t.Fatalf("password was not passed as env: %v", gotEnv)
	}
}

// Without DEFAULT PRIVILEGES a table created later is invisible to the role,
// and the workspace fails at runtime on exactly the tables a colleague added.
func TestReadOnlyGrantCoversFutureTables(t *testing.T) {
	sql := readOnlySQL("shop", "pw")
	for _, want := range []string{
		"ALTER DEFAULT PRIVILEGES", "GRANT SELECT ON ALL TABLES",
		"GRANT USAGE ON SCHEMA public", "GRANT CONNECT ON DATABASE",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("read-only grant is missing %q", want)
		}
	}
}

func TestMySQLIsDeclinedRatherThanHalfDone(t *testing.T) {
	db := DB{Engine: MySQL, Name: "shop"}
	exec := func(context.Context, string, []string, ...string) (string, error) {
		return "", fmt.Errorf("should not be called")
	}
	if err := EnsureReadOnly(context.Background(), exec, "c", db, "x"); err == nil {
		t.Fatal("mysql should be declined, not attempted")
	}
	if _, err := Clone(context.Background(), exec, "c", db, "t"); err == nil {
		t.Fatal("mysql cloning should be declined")
	}
}
