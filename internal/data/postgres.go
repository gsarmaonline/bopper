package data

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Exec runs a command inside a container, with env set on the process. The
// environment layer supplies this, so the data package never shells out to
// Docker itself.
//
// env exists so SQL never has to travel through a shell. It did once, and
// "DO $$ ... $$" came out as "DO 81 ..." because the shell expanded $$ to its
// own PID. Passing the password as a container environment variable removes
// the shell, and with it a whole class of quoting bugs.
type Exec func(ctx context.Context, container string, env []string, args ...string) (string, error)

// ReadOnlyRole is the role a workspace uses when it shares the baseline's data.
const ReadOnlyRole = "bopper_readonly"

// readOnlySQL creates the shared read-only role and grants it SELECT.
//
// This is the cheap safety valve, and it is what makes sharing the default
// defensible: a workspace physically cannot write to the baseline's data, so
// one worktree cannot corrupt another's by accident. No proxy, no parser, no
// statement inspection - a grant.
//
// DEFAULT PRIVILEGES matters as much as the grant. Without it a table created
// later is invisible to the role, and the workspace fails at runtime on exactly
// the tables a colleague just added.
func readOnlySQL(dbName, password string) string {
	return strings.Join([]string{
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s LOGIN PASSWORD '%s';
			ELSE
				ALTER ROLE %s LOGIN PASSWORD '%s';
			END IF;
		END $$;`, ReadOnlyRole, ReadOnlyRole, password, ReadOnlyRole, password),
		fmt.Sprintf(`GRANT CONNECT ON DATABASE %q TO %s;`, dbName, ReadOnlyRole),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s;`, ReadOnlyRole),
		fmt.Sprintf(`GRANT SELECT ON ALL TABLES IN SCHEMA public TO %s;`, ReadOnlyRole),
		fmt.Sprintf(`GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO %s;`, ReadOnlyRole),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO %s;`, ReadOnlyRole),
	}, "\n")
}

// WaitReady blocks until the database accepts connections.
//
// A running container is not a ready database. Postgres reports its container
// as running well before it finishes initialising, and SQL issued in that window
// fails with a missing socket - which reads like a configuration error and is
// not one. pg_isready is the actual question worth asking.
func WaitReady(ctx context.Context, exec Exec, container string, db DB, attempts int) error {
	var last error
	for i := 0; i < attempts; i++ {
		_, err := exec(ctx, container, nil, "pg_isready", "-U", db.User, "-d", db.Name, "-q")
		if err == nil {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("database in %s did not become ready: %w", container, last)
}

// EnsureReadOnly creates the read-only role on the baseline database.
func EnsureReadOnly(ctx context.Context, exec Exec, container string, db DB, password string) error {
	if db.Engine != Postgres {
		return fmt.Errorf("read-only sharing is implemented for postgres only, not %s", db.Engine)
	}
	_, err := psql(ctx, exec, container, db, db.Name, readOnlySQL(db.Name, password))
	return err
}

// Clone copies the baseline database for one workspace.
//
// CREATE DATABASE ... TEMPLATE is tried first: it is the fast path. It requires
// that NOTHING is connected to the template, which the running baseline usually
// violates, so a failure here is expected rather than exceptional and falls back
// to a dump and restore.
//
// Note that TEMPLATE is a full file copy, not a copy-on-write clone. Only a ZFS
// or btrfs snapshot of the data directory would be genuinely cheap; vision.md
// conflated the two and this is the honest position.
func Clone(ctx context.Context, exec Exec, container string, db DB, target string) (string, error) {
	if db.Engine != Postgres {
		return "", fmt.Errorf("cloning is implemented for postgres only, not %s", db.Engine)
	}
	if _, err := psql(ctx, exec, container, db, "postgres",
		fmt.Sprintf(`CREATE DATABASE %q TEMPLATE %q;`, target, db.Name)); err == nil {
		return "template", nil
	}

	// Fall back to a dump and restore, which tolerates live connections.
	if _, err := psql(ctx, exec, container, db, "postgres",
		fmt.Sprintf(`CREATE DATABASE %q;`, target)); err != nil {
		return "", err
	}
	// A pipe needs a shell, but this command contains no dollar signs.
	out, err := exec(ctx, container, []string{"PGPASSWORD=" + db.Password}, "sh", "-c",
		fmt.Sprintf("pg_dump -U %q -d %q | psql -q -v ON_ERROR_STOP=1 -U %q -d %q",
			db.User, db.Name, db.User, target))
	if err != nil {
		return "", fmt.Errorf("dump and restore failed: %w: %s", err, out)
	}
	return "dump", nil
}

// Drop removes a workspace's database clone.
func Drop(ctx context.Context, exec Exec, container string, db DB, target string) error {
	if db.Engine != Postgres {
		return nil
	}
	// Disconnect anything still attached, or the drop fails.
	_, _ = psql(ctx, exec, container, db, "postgres", fmt.Sprintf(
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s';`, target))
	_, err := psql(ctx, exec, container, db, "postgres",
		fmt.Sprintf(`DROP DATABASE IF EXISTS %q;`, target))
	return err
}

// Exists reports whether a database is present.
func Exists(ctx context.Context, exec Exec, container string, db DB, target string) bool {
	out, err := psql(ctx, exec, container, db, "postgres",
		fmt.Sprintf(`SELECT 1 FROM pg_database WHERE datname = '%s';`, target))
	return err == nil && strings.Contains(out, "1")
}

func psql(ctx context.Context, exec Exec, container string, db DB, dbName, sql string) (string, error) {
	return exec(ctx, container, []string{"PGPASSWORD=" + db.Password},
		"psql", "-v", "ON_ERROR_STOP=1", "-q", "-U", db.User, "-d", dbName, "-c", sql)
}

// URL builds a connection string.
func URL(db DB, host, user, password, dbName string) string {
	return fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable",
		user, password, host, dbName)
}
