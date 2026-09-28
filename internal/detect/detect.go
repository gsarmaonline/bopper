// Package detect answers one question: which services does this workspace change?
//
// It hashes each service's build inputs rather than comparing built image
// digests. Digests would cost a full build of every service on every bop up,
// which is the slow step Bopper exists to remove, and they move whenever a build
// stamps a timestamp even though nothing meaningful changed. Input hashes need
// no build to decide what to build, and they stay stable across rebuilds.
//
// See vision.md, mechanism 2.
package detect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/gsarmaonline/bopper/internal/envfile"
	"github.com/moby/patternmatcher"
)

// Fingerprint is everything that decides whether a service must be rebuilt.
type Fingerprint struct {
	Service string
	Build   string // hash of the build context, Dockerfile, args and base images
	Config  string // hash of the normalized runtime configuration
	Files   int    // context files hashed, for reporting
}

// Sum combines both halves. Two services with equal Sum are interchangeable.
func (f Fingerprint) Sum() string {
	h := sha256.New()
	fmt.Fprintf(h, "build:%s\nconfig:%s\n", f.Build, f.Config)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Project is a loaded Compose project plus the directory it came from.
type Project struct {
	Dir     string
	Compose *types.Project
}

// Load reads the Compose project rooted at dir.
//
// The project name is forced to a constant. A real project name derives from the
// directory, which differs per workspace, and it appears throughout the resolved
// model - in container names and network references - so leaving it alone would
// make every service look changed in every workspace.
func Load(ctx context.Context, dir string, files []string) (*Project, error) {
	if len(files) == 0 {
		found, err := findComposeFile(dir)
		if err != nil {
			return nil, err
		}
		files = []string{found}
	}
	cfgs := make([]types.ConfigFile, 0, len(files))
	for _, f := range files {
		if !filepath.IsAbs(f) {
			f = filepath.Join(dir, f)
		}
		cfgs = append(cfgs, types.ConfigFile{Filename: f})
	}

	env := environment(dir)
	p, err := loader.LoadWithContext(ctx, types.ConfigDetails{
		WorkingDir:  dir,
		ConfigFiles: cfgs,
		Environment: env,
	}, func(o *loader.Options) {
		o.SetProjectName("bopper", true)
		o.ResolvePaths = true
		o.SkipValidation = false
	})
	if err != nil {
		return nil, fmt.Errorf("loading compose in %s: %w", dir, err)
	}
	_ = env
	return &Project{Dir: dir, Compose: p}, nil
}

var composeNames = []string{
	"compose.yaml", "compose.yml",
	"docker-compose.yaml", "docker-compose.yml",
}

func findComposeFile(dir string) (string, error) {
	for _, n := range composeNames {
		p := filepath.Join(dir, n)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no compose file in %s", dir)
}

// Sentinels replace the variables that identify a workspace. Both the baseline
// and the workspace are loaded with these exact values, so a Compose file that
// interpolates them produces identical text on both sides.
//
// Normalizing after the fact does not work. A file that writes
// "${BOPPER_WORKSPACE:-baseline}" resolves to the workspace ID in a workspace
// and to the literal "baseline" in the baseline; rewriting the ID afterwards
// leaves two different strings. The substitution has to happen before
// interpolation, on both sides.
const (
	sentinelWorkspace = "__bopper_ws__"
	sentinelHost      = "__bopper_ws__.localhost"
	sentinelProject   = "bopper"
)

// environment reads .env so interpolation resolves the same way docker compose
// would, then neutralizes the workspace-identity variables.
func environment(dir string) types.Mapping {
	m := types.Mapping{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		neutralize(m)
		return m
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, '='); i > 0 {
			m[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
		}
	}
	neutralize(m)
	return m
}

func neutralize(m types.Mapping) {
	m["BOPPER_WORKSPACE"] = sentinelWorkspace
	m["BOPPER_HOST"] = sentinelHost
	m["COMPOSE_PROJECT_NAME"] = sentinelProject
}

// Fingerprints hashes every service in the project.
func (p *Project) Fingerprints() (map[string]Fingerprint, error) {
	out := map[string]Fingerprint{}
	for name, svc := range p.Compose.Services {
		f := Fingerprint{Service: name}

		cfg, err := p.configHash(svc)
		if err != nil {
			return nil, err
		}
		f.Config = cfg

		if svc.Build != nil {
			b, n, err := p.buildHash(svc)
			if err != nil {
				return nil, fmt.Errorf("service %s: %w", name, err)
			}
			f.Build, f.Files = b, n
		} else {
			// An image-only service has no build inputs. Its image reference is
			// part of the configuration hash already.
			f.Build = "no-build"
		}
		out[name] = f
	}
	return out, nil
}

// buildHash covers the build context, the Dockerfile, the build args and the
// base images named by FROM.
//
// Base images are hashed as written, not resolved to a registry digest. A moved
// tag therefore goes unnoticed. That is the "uncaptured build inputs" hard edge
// in vision.md, and the mitigation is to pin base images by digest.
func (p *Project) buildHash(svc types.ServiceConfig) (string, int, error) {
	b := svc.Build
	ctxDir := b.Context
	if !filepath.IsAbs(ctxDir) {
		ctxDir = filepath.Join(p.Dir, ctxDir)
	}

	dockerfile := b.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	dfPath := dockerfile
	if !filepath.IsAbs(dfPath) {
		dfPath = filepath.Join(ctxDir, dockerfile)
	}

	h := sha256.New()

	dfData, err := os.ReadFile(dfPath)
	if err != nil {
		return "", 0, fmt.Errorf("reading %s: %w", dfPath, err)
	}
	fmt.Fprintf(h, "dockerfile:%s\n", sha256Bytes(dfData))
	for _, from := range fromLines(dfData) {
		fmt.Fprintf(h, "from:%s\n", from)
	}
	fmt.Fprintf(h, "target:%s\n", b.Target)

	keys := make([]string, 0, len(b.Args))
	for k := range b.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := ""
		if b.Args[k] != nil {
			v = *b.Args[k]
		}
		fmt.Fprintf(h, "arg:%s=%s\n", k, v)
	}

	matcher, err := loadDockerignore(ctxDir)
	if err != nil {
		return "", 0, err
	}

	var files []string
	err = filepath.WalkDir(ctxDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(ctxDir, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		// .git is always skipped. Docker would send it unless .dockerignore says
		// otherwise, but its contents change on every commit, so hashing it would
		// mark every service changed in every workspace.
		if d.IsDir() && (d.Name() == ".git") {
			return filepath.SkipDir
		}
		if matcher != nil {
			skip, merr := matcher.MatchesOrParentMatches(rel)
			if merr != nil {
				return merr
			}
			if skip {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", 0, err
	}

	sort.Strings(files)
	for _, rel := range files {
		full := filepath.Join(ctxDir, rel)
		info, err := os.Lstat(full)
		if err != nil {
			return "", 0, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(full)
			if err != nil {
				return "", 0, err
			}
			fmt.Fprintf(h, "link:%s->%s\n", rel, target)
			continue
		}
		sum, err := hashContextFile(full, rel)
		if err != nil {
			return "", 0, err
		}
		// The executable bit is part of the input; the rest of the mode is not,
		// because a clone can legitimately differ in it.
		x := 0
		if info.Mode()&0o111 != 0 {
			x = 1
		}
		fmt.Fprintf(h, "file:%s:%d:%s\n", rel, x, sum)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], len(files), nil
}

func loadDockerignore(dir string) (*patternmatcher.PatternMatcher, error) {
	b, err := os.ReadFile(filepath.Join(dir, ".dockerignore"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var patterns []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	if len(patterns) == 0 {
		return nil, nil
	}
	return patternmatcher.New(patterns)
}

var fromRe = regexp.MustCompile(`(?im)^\s*FROM\s+(\S+)`)

func fromLines(dockerfile []byte) []string {
	var out []string
	for _, m := range fromRe.FindAllSubmatch(dockerfile, -1) {
		out = append(out, string(m[1]))
	}
	return out
}

// volatile names values that differ between workspaces by construction. They
// must not contribute to a fingerprint, or every service would look changed.
var volatileEnv = []string{"BOPPER_", "COMPOSE_PROJECT_NAME"}

// configHash covers the service's runtime configuration, so a branch that
// changes only a command or a port is still detected.
//
// Two normalizations are required. Absolute paths are rewritten relative to the
// project directory, because a workspace lives at a different path and its
// volume sources would otherwise all differ. Workspace-naming environment
// variables are dropped for the same reason.
func (p *Project) configHash(svc types.ServiceConfig) (string, error) {
	svc.Name = ""
	svc.ContainerName = ""

	env := types.MappingWithEquals{}
	for k, v := range svc.Environment {
		if isVolatile(k) {
			continue
		}
		env[k] = v
	}
	svc.Environment = env
	svc.Build = nil // hashed separately

	b, err := json.Marshal(svc)
	if err != nil {
		return "", err
	}
	s := string(b)
	// Rewrite this project's directory, and the real path behind it, so two
	// checkouts of the same commit agree.
	for _, dir := range projectPaths(p.Dir) {
		s = strings.ReplaceAll(s, dir, "${PROJECT_DIR}")
	}
	return sha256Bytes([]byte(s)), nil
}

func projectPaths(dir string) []string {
	paths := []string{dir}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		paths = append(paths, real)
	}
	// Longest first, so a prefix never shadows a longer match.
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	return paths
}

func isVolatile(k string) bool {
	for _, v := range volatileEnv {
		if strings.HasPrefix(k, v) || k == v {
			return true
		}
	}
	return false
}

// Change is one service that differs between the baseline and a workspace.
type Change struct {
	Service  string
	Reason   string // "build", "config", "both", "added", "removed"
	Baseline Fingerprint
	Current  Fingerprint
}

// Diff compares two sets of fingerprints.
func Diff(baseline, current map[string]Fingerprint) []Change {
	var out []Change
	for name, cur := range current {
		base, ok := baseline[name]
		if !ok {
			out = append(out, Change{Service: name, Reason: "added", Current: cur})
			continue
		}
		bd, cd := base.Build != cur.Build, base.Config != cur.Config
		switch {
		case bd && cd:
			out = append(out, Change{name, "both", base, cur})
		case bd:
			out = append(out, Change{name, "build", base, cur})
		case cd:
			out = append(out, Change{name, "config", base, cur})
		}
	}
	for name, base := range baseline {
		if _, ok := current[name]; !ok {
			out = append(out, Change{Service: name, Reason: "removed", Baseline: base})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}

func sha256Bytes(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])[:16]
}

// hashContextFile hashes one build-context file. A .env is hashed with Bopper's
// own managed block removed: Bopper writes that block itself, so counting it
// would report every service in every workspace as changed.
func hashContextFile(path, rel string) (string, error) {
	if filepath.Base(rel) == ".env" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return sha256Bytes(envfile.StripManaged(b)), nil
	}
	return sha256File(path)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}
