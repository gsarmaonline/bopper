// Package envfile manages the block Bopper writes into a workspace's .env.
//
// Both halves of Bopper need to agree about this block. The workspace layer
// writes it; change detection must ignore it, because a build context often
// contains .env and Bopper's own edit would otherwise make every service in
// every workspace look changed.
package envfile

import (
	"bytes"
	"os"
	"strings"
)

// Marker opens the managed block. Everything from this line to the end of the
// file belongs to Bopper; everything above it belongs to the developer.
const Marker = "# --- bopper (managed) ---"

// StripManaged returns the file with the managed block removed, so two
// workspaces' .env files compare equal when their hand-written parts match.
//
// Trailing whitespace is trimmed whether or not a marker was found. Trimming
// only in the marker case would make a baseline's "FOO=bar\n" differ from a
// workspace's stripped "FOO=bar", and every service would look changed.
func StripManaged(content []byte) []byte {
	if idx := bytes.Index(content, []byte(Marker)); idx >= 0 {
		content = content[:idx]
	}
	return bytes.TrimRight(content, "\n \t")
}

// Patch rewrites path so the managed block holds exactly vars, in the given
// order. Anything the developer wrote above the marker survives, and repeated
// calls are idempotent.
func Patch(path string, vars [][2]string) error {
	var kept []byte
	if b, err := os.ReadFile(path); err == nil {
		kept = StripManaged(b)
	} else if !os.IsNotExist(err) {
		return err
	}

	var sb strings.Builder
	if len(kept) > 0 {
		sb.Write(kept)
		sb.WriteString("\n")
	}
	sb.WriteString("\n" + Marker + "\n")
	for _, kv := range vars {
		sb.WriteString(kv[0] + "=" + kv[1] + "\n")
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

// Read returns the developer's own variables from a .env, excluding Bopper's
// managed block. It is how the data layer tells which database variables a
// project actually uses, so Bopper overrides those rather than inventing
// variables the application never reads.
func Read(path string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(StripManaged(b)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, '='); i > 0 {
			out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
		}
	}
	return out
}
