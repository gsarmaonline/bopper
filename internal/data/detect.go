// Package data manages the database a workspace uses.
//
// The rule, from vision.md: share by default, copy only when a change would
// otherwise corrupt the shared resource, or when asked. For data that means a
// workspace reads the baseline's real database - which is what most branches
// want, and why nobody waits for a seed - and gets its own copy only when its
// migrations differ from the baseline's, because that migration would break
// every other worktree.
package data

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gsarmaonline/bopper/internal/stack"
)

// Engine is a supported database engine.
type Engine string

const (
	Postgres Engine = "postgres"
	MySQL    Engine = "mysql"
)

// DB is a database service found in the Compose project.
type DB struct {
	Service  string
	Engine   Engine
	User     string
	Password string
	Name     string
}

// Detect finds database services in a stack.
//
// Detection is by image name and the environment variables the official images
// define, because that is what any declaration of a stack actually carries -
// Compose, a Kubernetes manifest or anything else. A service that looks like a
// database but exposes no credentials is skipped rather than guessed at.
func Detect(st stack.Stack) []DB {
	var out []DB
	services := append([]stack.Service(nil), st.Services...)
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })

	for _, svc := range services {
		name := svc.Name
		img := strings.ToLower(svc.Image)
		env := func(k string) string { return svc.Env[k] }
		switch {
		case strings.Contains(img, "postgres"):
			db := DB{
				Service: name, Engine: Postgres,
				User:     firstNonEmpty(env("POSTGRES_USER"), "postgres"),
				Password: env("POSTGRES_PASSWORD"),
				Name:     firstNonEmpty(env("POSTGRES_DB"), firstNonEmpty(env("POSTGRES_USER"), "postgres")),
			}
			out = append(out, db)
		case strings.Contains(img, "mysql") || strings.Contains(img, "mariadb"):
			out = append(out, DB{
				Service: name, Engine: MySQL,
				User:     firstNonEmpty(env("MYSQL_USER"), "root"),
				Password: firstNonEmpty(env("MYSQL_PASSWORD"), env("MYSQL_ROOT_PASSWORD")),
				Name:     env("MYSQL_DATABASE"),
			})
		}
	}
	return out
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// MigrationDirs are the conventional locations, in the order they are checked.
// A project that keeps migrations elsewhere is not detected, and the branch then
// shares the baseline database - so the list is deliberately broad.
var MigrationDirs = []string{
	"migrations", "migrate", "db/migrate", "db/migrations",
	"sql/migrations", "database/migrations", "priv/repo/migrations",
}

// MigrationsDiffer reports whether a workspace's migrations differ from the
// baseline's, and which directory decided it.
//
// This is the trigger for a clone. It is a file comparison rather than anything
// cleverer on purpose: a migration that exists in one checkout and not the other
// is exactly the case that must not run against shared data.
func MigrationsDiffer(baselineDir, wsDir string) (bool, string, error) {
	for _, rel := range MigrationDirs {
		b := filepath.Join(baselineDir, rel)
		w := filepath.Join(wsDir, rel)
		bExists, wExists := isDir(b), isDir(w)
		if !bExists && !wExists {
			continue
		}
		bh, err := hashDir(b)
		if err != nil {
			return false, rel, err
		}
		wh, err := hashDir(w)
		if err != nil {
			return false, rel, err
		}
		if bh != wh {
			return true, rel, nil
		}
	}
	return false, "", nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// hashDir hashes a directory's file names and contents. A missing directory
// hashes to a stable empty value, so "the branch added migrations/" and "the
// branch deleted migrations/" both register as a difference.
func hashDir(dir string) (string, error) {
	h := sha256.New()
	if !isDir(dir) {
		return hex.EncodeToString(h.Sum(nil))[:16], nil
	}
	var files []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	for _, rel := range files {
		f, err := os.Open(filepath.Join(dir, rel))
		if err != nil {
			return "", err
		}
		fh := sha256.New()
		if _, err := io.Copy(fh, f); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
		fmt.Fprintf(h, "%s:%s\n", rel, hex.EncodeToString(fh.Sum(nil)))
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}
