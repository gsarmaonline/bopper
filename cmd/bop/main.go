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
	"text/tabwriter"

	"github.com/gsarmaonline/bopper/internal/detect"
	"github.com/gsarmaonline/bopper/internal/workspace"
)

const usage = `bop - copy-on-write Docker Compose for git worktrees

Usage:
  bop up <name> [-base <branch>]   create a workspace
  bop down <name> [-delete-branch] remove a workspace
  bop ls                           list workspaces
  bop status [<name>] [-v]         show which services a workspace changes

Not yet implemented: starting containers, routing, databases (Phase 3+).
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: bop up <name> [-base <branch>]")
	}
	m, err := manager()
	if err != nil {
		return err
	}
	w, err := m.Up(fs.Arg(0), workspace.UpOptions{Base: *base})
	if err != nil {
		return err
	}
	fmt.Printf("workspace %s\n", w.ID)
	fmt.Printf("  dir    %s\n", w.Dir)
	fmt.Printf("  branch %s (from %s)\n", w.Branch, w.Base)
	fmt.Printf("  host   %s  (not serving yet - Phase 3)\n", w.Host())
	return nil
}

func cmdDown(args []string) error {
	fs := flag.NewFlagSet("down", flag.ExitOnError)
	del := fs.Bool("delete-branch", false, "also delete the workspace branch")
	if err := fs.Parse(args); err != nil {
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
	if err := m.Down(id, workspace.DownOptions{DeleteBranch: *del}); err != nil {
		return err
	}
	fmt.Printf("removed workspace %s\n", id)
	return nil
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := manager()
	if err != nil {
		return err
	}

	// Which directory are we comparing? A named workspace, or the worktree the
	// command was run from.
	target, label := m.Here, filepath.Base(m.Here)
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
