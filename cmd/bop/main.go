// Command bop manages Bopper workspaces.
//
// Phases 1 and 2 are implemented: the workspace layer (git and the filesystem)
// and change detection (input hashing). The environment layer - starting
// containers, routing and databases - is Phase 3 and is not here yet.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/gsarmaonline/bopper/internal/data"
	"github.com/gsarmaonline/bopper/internal/detect"
	"github.com/gsarmaonline/bopper/internal/envfile"
	"github.com/gsarmaonline/bopper/internal/environment"
	"github.com/gsarmaonline/bopper/internal/workspace"
)

const usage = `bop - copy-on-write Docker Compose for git worktrees

Usage:
  bop up <name> [-base <branch>] [-no-env] [-share-db|-isolate-db]
                                            create a workspace and start its overlay
  bop down <name> [-delete-branch]          remove a workspace and its containers
  bop ls                                    list workspaces
  bop status [<name>] [-v]                  show which services a workspace changes
  bop ps [<name>]                           show a workspace's running containers
  bop clean [-idle <dur>] [-remove-after <dur>] [-dry-run]
                                            reclaim idle overlays and orphans
  bop headers [on|off]                      route baseline->overlay calls by header

bop up starts the baseline stack once, then runs only the services the branch
changed. Everything else falls through to the baseline.

Not yet implemented: header routing, database clones (Phase 4+).
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "up":
		err = cmdUp(os.Args[2:])
	case "down":
		err = cmdDown(os.Args[2:])
	case "ls", "list":
		err = cmdList(os.Args[2:])
	case "status":
		err = cmdStatus(os.Args[2:])
	case "ps":
		err = cmdPs(os.Args[2:])
	case "clean":
		err = cmdClean(os.Args[2:])
	case "headers":
		err = cmdHeaders(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "bop:", err)
		os.Exit(1)
	}
}

// parseArgs parses flags that may appear after positional arguments.
//
// The standard flag package stops at the first non-flag argument, so
// "bop down feat -delete-branch" would silently treat -delete-branch as a
// second positional. That is the form the help text documents and the form
// people type, so permute the arguments before parsing.
func parseArgs(fs *flag.FlagSet, args []string) error {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			// "-name value" consumes the next argument; "-bool" and
			// "-name=value" do not.
			if !strings.Contains(a, "=") {
				if f := fs.Lookup(strings.TrimLeft(a, "-")); f != nil {
					if b, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !b.IsBoolFlag() {
						if i+1 < len(args) {
							i++
							flags = append(flags, args[i])
						}
					}
				}
			}
			continue
		}
		positional = append(positional, a)
	}
	// Re-insert the terminator so anything collected as positional stays
	// positional, including a literal argument that begins with a dash.
	return fs.Parse(append(append(flags, "--"), positional...))
}

func manager() (*workspace.Manager, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return workspace.Open(cwd)
}

func cmdUp(args []string) error {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	base := fs.String("base", "", "branch to fork from (default: the current branch)")
	noEnv := fs.Bool("no-env", false, "create the workspace only; do not start containers")
	shareDB := fs.Bool("share-db", false, "share the baseline database with writes, accepting the risk")
	isolateDB := fs.Bool("isolate-db", false, "always give this workspace its own database")
	verbose := fs.Bool("v", false, "stream docker output")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: bop up <name> [-base <branch>] [-no-env]")
	}
	m, err := manager()
	if err != nil {
		return err
	}
	// bop up on an existing workspace re-applies it. That is the command a
	// developer reaches for after editing code, and erroring here would leave
	// them with no way to restart an overlay.
	id := workspace.NormalizeID(fs.Arg(0))
	w, existing, err := m.Get(id)
	if err != nil {
		return err
	}
	if existing {
		fmt.Printf("workspace %s (existing)\n", w.ID)
		fmt.Printf("  dir    %s\n", w.Dir)
	} else {
		w, err = m.Up(fs.Arg(0), workspace.UpOptions{Base: *base})
		if err != nil {
			return err
		}
		fmt.Printf("workspace %s\n", w.ID)
		fmt.Printf("  dir    %s\n", w.Dir)
		fmt.Printf("  branch %s (from %s)\n", w.Branch, w.Base)
	}

	if *noEnv {
		fmt.Printf("  env    skipped (-no-env)\n")
		return nil
	}

	ctx := context.Background()
	if err := environment.Available(ctx); err != nil {
		// The workspace is real and useful without Docker. Say what is missing
		// rather than unwinding work the user asked for.
		fmt.Printf("  env    not started: %v\n", err)
		fmt.Printf("         the worktree is ready; run bop up again once docker is up\n")
		return nil
	}
	mode := data.Auto
	switch {
	case *shareDB && *isolateDB:
		return fmt.Errorf("-share-db and -isolate-db contradict each other")
	case *shareDB:
		mode = data.Share
	case *isolateDB:
		mode = data.Isolate
	}
	return startEnv(ctx, m, w, *verbose, mode)
}

// startEnv compares the workspace against the baseline and runs what changed.
//
// The data step runs BEFORE the overlay, and that ordering is load-bearing:
// it rewrites .env, and compose reads .env when the overlay starts. Doing it
// afterwards would start the overlay pointed at the wrong database.
func startEnv(ctx context.Context, m *workspace.Manager, w workspace.Workspace,
	verbose bool, dbMode data.Mode) error {

	r, err := environment.NewRunner()
	if err != nil {
		return err
	}
	r.Verbose = verbose

	// The baseline must be up before anything can be asked of its database.
	basePrj, err := detect.LoadForRun(ctx, m.Root, nil)
	if err != nil {
		return err
	}
	if err := r.EnsureBaseline(ctx, m.Root, basePrj.Compose); err != nil {
		return fmt.Errorf("starting the baseline: %w", err)
	}
	if err := applyData(ctx, r, m.Root, w, basePrj.Compose, dbMode); err != nil {
		return err
	}

	changed, project, err := changedServices(ctx, m.Root, w.Dir)
	if err != nil {
		return err
	}

	fmt.Printf("  env    starting %d of %d services...\n", len(changed), len(project.Services))
	res, err := r.Up(ctx, environment.Target{ID: w.ID, Dir: w.Dir, Host: w.Host()},
		m.Root, project, changed)
	if err != nil {
		return err
	}

	if len(res.Changed) == 0 {
		fmt.Printf("  env    nothing changed; the baseline serves all %d services\n", res.Total)
		return nil
	}
	fmt.Printf("  overlay %s\n", strings.Join(res.Changed, ", "))
	if res.URL != "" {
		fmt.Printf("  url    %s\n", res.URL)
	} else {
		fmt.Printf("  url    none: no changed service publishes a port\n")
	}
	if res.HeaderRouting {
		fmt.Printf("  routing header %s: %s\n", environment.Header, w.ID)
	}
	if res.FullStack {
		// Spike C measured this at one branch in five. It is not an edge case,
		// and it should not be a silent disappointment.
		fmt.Printf("\nthis branch changes every service, so the overlay saves nothing here\n")
	}
	return nil
}

// dropClone removes a workspace's database clone, if it has one.
//
// A shared database is never touched: other workspaces and the baseline are
// still using it, and the read-only role is shared too.
func dropClone(ctx context.Context, r *environment.Runner, baseDir string, w workspace.Workspace) error {
	prj, err := detect.LoadForRun(ctx, baseDir, nil)
	if err != nil {
		return nil // no compose project; nothing to drop
	}
	dbs := data.Detect(prj.Compose)
	if len(dbs) == 0 || dbs[0].Engine != data.Postgres {
		return nil
	}
	db := dbs[0]
	container, err := r.ContainerFor(ctx, db.Service)
	if err != nil {
		return nil // the baseline is not running; nothing to drop against
	}
	target := data.Decide(db, w.ID, true, "", data.Isolate).Target
	if !data.Exists(ctx, r.Exec, container, db, target) {
		return nil
	}
	if err := data.Drop(ctx, r.Exec, container, db, target); err != nil {
		return fmt.Errorf("dropping the database clone %s: %w", target, err)
	}
	fmt.Printf("dropped database %s\n", target)
	return nil
}

// applyData decides whether the workspace shares the baseline's database or
// gets its own, then rewires .env to match.
func applyData(ctx context.Context, r *environment.Runner, baseDir string,
	w workspace.Workspace, project *types.Project, mode data.Mode) error {

	dbs := data.Detect(project)
	if len(dbs) == 0 {
		return nil
	}
	if len(dbs) > 1 {
		fmt.Printf("  data   %d database services found; using %q\n", len(dbs), dbs[0].Service)
	}
	db := dbs[0]
	if db.Engine != data.Postgres {
		fmt.Printf("  data   %s is not supported yet; the workspace shares it as-is\n", db.Engine)
		return nil
	}

	differ, where, err := data.MigrationsDiffer(baseDir, w.Dir)
	if err != nil {
		return err
	}
	plan := data.Decide(db, w.ID, differ, where, mode)

	container, err := r.ContainerFor(ctx, db.Service)
	if err != nil {
		return err
	}
	if err := r.WaitHealthy(ctx, container, 40); err != nil {
		return err
	}
	// A running container is not a ready database.
	if err := data.WaitReady(ctx, r.Exec, container, db, 60); err != nil {
		return err
	}

	// The read-only role's password is derived rather than random, so a second
	// bop up produces the same .env and does not look like a change.
	roPassword := "bopper-" + w.ID

	switch {
	case plan.Clone:
		if data.Exists(ctx, r.Exec, container, db, plan.Target) {
			fmt.Printf("  data   own database %s (existing)\n", plan.Target)
		} else {
			how, err := data.Clone(ctx, r.Exec, container, db, plan.Target)
			if err != nil {
				return fmt.Errorf("cloning the database: %w", err)
			}
			fmt.Printf("  data   own database %s (%s) - %s\n", plan.Target, how, plan.Reason)
		}
	case plan.ReadOnly:
		if err := data.EnsureReadOnly(ctx, r.Exec, container, db, roPassword); err != nil {
			return fmt.Errorf("creating the read-only role: %w", err)
		}
		fmt.Printf("  data   shared, read-only - %s\n", plan.Reason)
	default:
		// Shared WITH writes, which only happens on an explicit -share-db. This
		// is the one configuration where a workspace can corrupt the baseline's
		// data and every other workspace's view of it, so it says so.
		fmt.Printf("  data   shared, WRITABLE - %s\n", plan.Reason)
		fmt.Printf("         writes and migrations from this workspace affect the\n")
		fmt.Printf("         baseline and every other worktree\n")
	}

	// The database's hostname on the container network is its service name.
	vars := data.EnvFor(plan, db.Service, envfile.Read(filepath.Join(w.Dir, ".env")), roPassword)
	return envfile.Patch(filepath.Join(w.Dir, ".env"), append([][2]string{
		{"BOPPER_WORKSPACE", w.ID},
		{"BOPPER_HOST", w.Host()},
		{"COMPOSE_PROJECT_NAME", "bopper-ws-" + w.ID},
	}, vars...))
}

// changedServices loads both projects and diffs them.
func changedServices(ctx context.Context, baseDir, wsDir string) ([]string, *types.Project, error) {
	basePrj, err := detect.Load(ctx, baseDir, nil)
	if err != nil {
		return nil, nil, err
	}
	curPrj, err := detect.Load(ctx, wsDir, nil)
	if err != nil {
		return nil, nil, err
	}
	baseFP, err := basePrj.Fingerprints()
	if err != nil {
		return nil, nil, err
	}
	curFP, err := curPrj.Fingerprints()
	if err != nil {
		return nil, nil, err
	}
	var changed []string
	for _, c := range detect.Diff(baseFP, curFP) {
		if c.Reason != "removed" {
			changed = append(changed, c.Service)
		}
	}
	// Containers need the real environment, not the sentinels comparison uses.
	runPrj, err := detect.LoadForRun(ctx, wsDir, nil)
	if err != nil {
		return nil, nil, err
	}
	return changed, runPrj.Compose, nil
}

func cmdDown(args []string) error {
	fs := flag.NewFlagSet("down", flag.ExitOnError)
	del := fs.Bool("delete-branch", false, "also delete the workspace branch")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: bop down <name> [-delete-branch]")
	}
	m, err := manager()
	if err != nil {
		return err
	}
	id := workspace.NormalizeID(fs.Arg(0))
	w, found, err := m.Get(id)
	if err != nil {
		return err
	}

	// Containers first: once the worktree is gone, the compose project
	// directory it referenced no longer exists.
	if found {
		ctx := context.Background()
		if environment.Available(ctx) == nil {
			r, err := environment.NewRunner()
			if err != nil {
				return err
			}
			// The database clone goes before the containers, because dropping it
			// needs the baseline's database container, and reading the project
			// needs the worktree.
			if err := dropClone(ctx, r, m.Root, w); err != nil {
				fmt.Fprintf(os.Stderr, "bop: %v\n", err)
			}
			if err := r.Down(ctx, environment.Target{ID: w.ID, Dir: w.Dir}); err != nil {
				return fmt.Errorf("stopping the overlay: %w", err)
			}
		}
	}
	if err := m.Down(id, workspace.DownOptions{DeleteBranch: *del}); err != nil {
		return err
	}
	fmt.Printf("removed workspace %s\n", id)
	return nil
}

// cmdHeaders turns header routing on or off.
//
// It is deliberately a separate command rather than a flag on bop up: the mode
// changes how the BASELINE is named and run, so it is an installation-wide
// choice, not a per-workspace one.
func cmdHeaders(args []string) error {
	fs := flag.NewFlagSet("headers", flag.ExitOnError)
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	r, err := environment.NewRunner()
	if err != nil {
		return err
	}
	if fs.NArg() == 0 {
		if r.HeaderRouting() {
			fmt.Println("header routing: on")
			fmt.Printf("  a baseline service that forwards %s reaches the overlay\n",
				environment.Header)
		} else {
			fmt.Println("header routing: off")
			fmt.Println("  a changed service must sit at the edge of the call graph")
		}
		return nil
	}
	switch fs.Arg(0) {
	case "on":
		if err := r.SetHeaderRouting(true); err != nil {
			return err
		}
		fmt.Println("header routing: on")
		fmt.Println()
		fmt.Println("Every internal call in the baseline now crosses the proxy, which costs")
		fmt.Printf("latency and adds a single point of failure. Your services must FORWARD\n")
		fmt.Printf("%s for it to work past the first hop; where they do not, the\n", environment.Header)
		fmt.Println("request reaches the baseline's version and nothing appears to be wrong.")
		fmt.Println()
		fmt.Println("Run bop up again to restart the baseline in this mode.")
	case "off":
		if err := r.SetHeaderRouting(false); err != nil {
			return err
		}
		fmt.Println("header routing: off")
		fmt.Println("Run bop up again to restart the baseline in the default mode.")
	default:
		return fmt.Errorf("usage: bop headers [on|off]")
	}
	return nil
}

// cmdClean reclaims what Bopper created. Docker cannot do this usefully: it
// records no last-access time, `prune --filter until=` filters on creation time
// rather than use, and that filter does not apply to volumes at all. Bopper
// labels everything it creates, so it can be precise.
func cmdClean(args []string) error {
	fs := flag.NewFlagSet("clean", flag.ExitOnError)
	idle := fs.Duration("idle", 3*time.Hour, "stop overlays unused for this long")
	rm := fs.Duration("remove-after", 72*time.Hour, "remove overlays unused for this long")
	dry := fs.Bool("dry-run", false, "report without changing anything")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	m, err := manager()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err := environment.Available(ctx); err != nil {
		return err
	}
	r, err := environment.NewRunner()
	if err != nil {
		return err
	}

	// Anything labelled for a workspace that no longer exists is an orphan,
	// whatever its age. This is the common case: a worktree removed by hand.
	ws, err := m.List()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	host := map[string]string{}
	for _, w := range ws {
		known[w.ID] = true
		host[w.ID] = w.Host()
	}

	actions, err := r.Clean(ctx, environment.Policy{
		StopIdle: *idle, RemoveIdle: *rm, Known: known, DryRun: *dry,
	}, func(id string) string {
		if h, ok := host[id]; ok {
			return h
		}
		return id + ".localhost"
	})
	if err != nil {
		return err
	}
	if len(actions) == 0 {
		fmt.Println("nothing to clean")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if *dry {
		fmt.Println("dry run; nothing was changed")
	}
	fmt.Fprintln(tw, "ACTION\tKIND\tNAME\tWHY")
	for _, a := range actions {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.Verb, a.Resource.Kind, a.Resource.Name, a.Reason)
	}
	return tw.Flush()
}

func cmdPs(args []string) error {
	fs := flag.NewFlagSet("ps", flag.ExitOnError)
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	m, err := manager()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err := environment.Available(ctx); err != nil {
		return err
	}
	r, err := environment.NewRunner()
	if err != nil {
		return err
	}

	ws, err := m.List()
	if err != nil {
		return err
	}
	if fs.NArg() == 1 {
		id := workspace.NormalizeID(fs.Arg(0))
		w, found, err := m.Get(id)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("no workspace %q", fs.Arg(0))
		}
		ws = []workspace.Workspace{w}
	}

	baseline := "stopped"
	if r.BaselineRunning(ctx) {
		baseline = "running"
	}
	fmt.Printf("baseline  %s  (%s)\n", baseline, environment.BaselineNetwork)

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "\nWORKSPACE\tCONTAINERS\tURL")
	for _, w := range ws {
		cs, err := r.OverlayContainers(ctx, w.ID)
		if err != nil {
			return err
		}
		names := "-"
		if len(cs) > 0 {
			names = strings.Join(cs, " ")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", w.ID, names, environment.URL(w.Host()))
	}
	return tw.Flush()
}

func cmdList(args []string) error {
	m, err := manager()
	if err != nil {
		return err
	}
	ws, err := m.List()
	if err != nil {
		return err
	}
	if len(ws) == 0 {
		fmt.Println("no workspaces")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tBRANCH\tBASE\tDIR")
	for _, w := range ws {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", w.ID, w.Branch, w.Base, w.Dir)
	}
	return tw.Flush()
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	verbose := fs.Bool("v", false, "show every service, not only the changed ones")
	if err := parseArgs(fs, args); err != nil {
		return err
	}
	m, err := manager()
	if err != nil {
		return err
	}

	// Which directory are we comparing? A named workspace, or the worktree the
	// command was run from.
	target, label := m.Here, filepath.Base(m.Here)
	// Prefer the workspace ID over the directory basename, so status run from
	// inside a workspace names it the same way bop ls does.
	if all, err := m.List(); err == nil {
		for _, w := range all {
			if w.Dir == m.Here {
				label = w.ID
				break
			}
		}
	}
	if fs.NArg() == 1 {
		w, found, err := m.Get(workspace.NormalizeID(fs.Arg(0)))
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("no workspace %q", fs.Arg(0))
		}
		target, label = w.Dir, w.ID
	}
	if target == m.Root {
		return fmt.Errorf("%s is the baseline itself; run this from a workspace, "+
			"or name one: bop status <name>", label)
	}

	ctx := context.Background()
	basePrj, err := detect.Load(ctx, m.Root, nil)
	if err != nil {
		return err
	}
	curPrj, err := detect.Load(ctx, target, nil)
	if err != nil {
		return err
	}
	baseFP, err := basePrj.Fingerprints()
	if err != nil {
		return err
	}
	curFP, err := curPrj.Fingerprints()
	if err != nil {
		return err
	}

	changes := detect.Diff(baseFP, curFP)
	fmt.Printf("%s vs baseline (%s)\n\n", label, filepath.Base(m.Root))

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if len(changes) == 0 {
		fmt.Fprintln(tw, "no services changed - the baseline serves all of them")
	} else {
		fmt.Fprintln(tw, "SERVICE\tCHANGED\tBASELINE\tWORKSPACE")
		for _, c := range changes {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Service, c.Reason,
				short(c.Baseline), short(c.Current))
		}
	}
	if *verbose {
		fmt.Fprintln(tw, "\nALL SERVICES\tBUILD\tCONFIG\tFILES")
		for name, f := range curFP {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", name, f.Build, f.Config, f.Files)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	// The tail the history spike found: one branch in five changes three or more
	// services, almost always through shared code or a lockfile. Say so plainly
	// rather than letting it be discovered.
	if len(changes) >= 3 && len(changes) >= len(curFP) {
		fmt.Printf("\nthis branch changes every service; the overlay saves nothing here\n")
	}
	return nil
}

func short(f detect.Fingerprint) string {
	if f.Build == "" && f.Config == "" {
		return "-"
	}
	return f.Sum()
}
