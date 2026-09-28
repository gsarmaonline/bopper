package main

import (
	"flag"
	"testing"
)

// The standard flag package stops parsing at the first positional argument, so
// "bop down feat -delete-branch" - the form the help text documents - silently
// treated the flag as a second positional and failed the usage check.
func TestParseArgsAcceptsFlagsAfterPositionals(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantBool bool
		wantStr  string
		wantArg  string
	}{
		{"flag after positional", []string{"feat", "-del"}, true, "", "feat"},
		{"flag before positional", []string{"-del", "feat"}, true, "", "feat"},
		{"value flag after positional", []string{"feat", "-base", "main"}, false, "main", "feat"},
		{"value flag with equals", []string{"feat", "-base=main"}, false, "main", "feat"},
		{"positional only", []string{"feat"}, false, "", "feat"},
		{"after a double dash", []string{"--", "-literal"}, false, "", "-literal"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			del := fs.Bool("del", false, "")
			base := fs.String("base", "", "")
			if err := parseArgs(fs, c.args); err != nil {
				t.Fatalf("parseArgs(%v): %v", c.args, err)
			}
			if *del != c.wantBool {
				t.Errorf("bool flag = %v, want %v", *del, c.wantBool)
			}
			if *base != c.wantStr {
				t.Errorf("string flag = %q, want %q", *base, c.wantStr)
			}
			if fs.NArg() != 1 || fs.Arg(0) != c.wantArg {
				t.Errorf("positionals = %v, want [%s]", fs.Args(), c.wantArg)
			}
		})
	}
}
