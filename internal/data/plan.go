package data

import (
	"fmt"
	"net/url"
	"strings"
)

// Mode is how a workspace should treat the database.
type Mode string

const (
	// Auto shares unless the branch's migrations differ from the baseline's.
	Auto Mode = "auto"
	// Share forces sharing, accepting the risk of a differing migration.
	Share Mode = "share"
	// Isolate forces a clone, for a branch that wants to throw data away freely.
	Isolate Mode = "isolate"
)

// Plan is what Bopper decided to do about data for one workspace.
type Plan struct {
	DB       DB
	Clone    bool
	Target   string // the clone's database name, when Clone is true
	ReadOnly bool   // sharing through the read-only role
	Reason   string
	Writable bool // whether the workspace can write
}

// Decide chooses between sharing and cloning.
//
// Sharing is the default because it is what most branches want: real data, no
// seed wait, no setup. It is safe because the workspace connects through a
// read-only role. A clone happens in exactly two cases - a differing migration,
// which would otherwise break every other worktree, and an explicit request.
func Decide(db DB, id string, migrationsDiffer bool, where string, mode Mode) Plan {
	target := "bopper_" + strings.ReplaceAll(id, "-", "_")
	switch mode {
	case Isolate:
		return Plan{DB: db, Clone: true, Target: target, Writable: true,
			Reason: "you asked for an isolated database"}
	case Share:
		return Plan{DB: db, Writable: true,
			Reason: "you asked to share the baseline database, with writes"}
	}
	if migrationsDiffer {
		return Plan{DB: db, Clone: true, Target: target, Writable: true,
			Reason: fmt.Sprintf("migrations in %s differ from the baseline", where)}
	}
	return Plan{DB: db, ReadOnly: true,
		Reason: "migrations match the baseline, so the data is shared read-only"}
}

// EnvKeys are the variable names Bopper will override when the developer's own
// .env already defines them.
//
// Bopper does not invent variables the application never reads. It always
// exports BOPPER_DB_NAME and BOPPER_DB_URL, which a Compose file can reference
// explicitly, and additionally rewrites a name the project already uses so the
// common case needs no change at all.
var EnvKeys = []string{
	"DATABASE_URL", "DATABASE_NAME", "DB_NAME", "DB_DATABASE",
	"POSTGRES_DB", "PGDATABASE", "MYSQL_DATABASE",
}

// EnvFor builds the managed .env entries for a plan.
//
// existing is the developer's own .env, so Bopper can tell which variables the
// project actually uses. host is the database's hostname on the container
// network, which is the compose service name.
func EnvFor(p Plan, host string, existing map[string]string, roPassword string) [][2]string {
	name := p.DB.Name
	user, pass := p.DB.User, p.DB.Password
	if p.Clone {
		name = p.Target
	}
	if p.ReadOnly {
		user, pass = ReadOnlyRole, roPassword
	}
	dsn := URL(p.DB, host, user, pass, name)

	out := [][2]string{
		{"BOPPER_DB_NAME", name},
		{"BOPPER_DB_URL", dsn},
	}
	for _, k := range EnvKeys {
		v, ok := existing[k]
		if !ok {
			continue
		}
		switch k {
		case "DATABASE_URL":
			out = append(out, [2]string{k, rewriteURL(v, host, user, pass, name)})
		default:
			out = append(out, [2]string{k, name})
		}
	}
	return out
}

// rewriteURL replaces the credentials and database in an existing connection
// string, keeping its scheme and query options so a project's own sslmode or
// pool settings survive.
func rewriteURL(raw, host, user, pass, name string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		// Not a URL Bopper understands; build a plain one rather than corrupt it.
		return fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable", user, pass, host, name)
	}
	u.User = url.UserPassword(user, pass)
	if u.Port() != "" {
		u.Host = host + ":" + u.Port()
	} else {
		u.Host = host
	}
	u.Path = "/" + name
	return u.String()
}
