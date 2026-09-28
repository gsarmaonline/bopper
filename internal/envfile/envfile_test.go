package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte("FOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vars := [][2]string{{"BOPPER_WORKSPACE", "x"}, {"BOPPER_HOST", "x.localhost"}}
	for i := 0; i < 3; i++ {
		if err := Patch(p, vars); err != nil {
			t.Fatal(err)
		}
	}
	got := read(t, p)
	if n := strings.Count(got, Marker); n != 1 {
		t.Fatalf("marker appears %d times after 3 patches:\n%s", n, got)
	}
	if !strings.Contains(got, "FOO=bar") {
		t.Fatal("the developer's own line was lost")
	}
}

func TestPatchKeepsHandWrittenLinesAndReplacesBlock(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte("FOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Patch(p, [][2]string{{"BOPPER_WORKSPACE", "old"}}); err != nil {
		t.Fatal(err)
	}
	if err := Patch(p, [][2]string{{"BOPPER_WORKSPACE", "new"}}); err != nil {
		t.Fatal(err)
	}
	got := read(t, p)
	if strings.Contains(got, "old") {
		t.Fatalf("stale managed value survived:\n%s", got)
	}
	if !strings.Contains(got, "BOPPER_WORKSPACE=new") {
		t.Fatalf("new value missing:\n%s", got)
	}
}

// A baseline .env has no marker; a workspace .env does. Stripping must make them
// compare equal, or change detection reports every service as changed.
func TestStripManagedNormalizesBothForms(t *testing.T) {
	baseline := []byte("FOO=bar\n")
	workspace := []byte("FOO=bar\n\n" + Marker + "\nBOPPER_WORKSPACE=x\n")
	if a, b := string(StripManaged(baseline)), string(StripManaged(workspace)); a != b {
		t.Fatalf("stripped forms differ: %q vs %q", a, b)
	}
}

func TestStripManagedWithoutMarker(t *testing.T) {
	if got := string(StripManaged([]byte("A=1\nB=2"))); got != "A=1\nB=2" {
		t.Fatalf("got %q", got)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
