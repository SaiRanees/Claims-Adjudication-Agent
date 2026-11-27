package prompts

import (
	"io/fs"
	"strings"
	"testing"
)

// TestEveryPromptLoads checks each embedded prompt is non-empty and LF-only,
// so its hash, and so its cassettes, are the same on every OS.
func TestEveryPromptLoads(t *testing.T) {
	paths, err := fs.Glob(files, "*/*/*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 2 {
		t.Fatalf("found %v", paths)
	}
	for _, p := range paths {
		parts := strings.Split(strings.TrimSuffix(p, ".md"), "/")
		b, err := Load(parts[0], parts[1], parts[2])
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		if len(b) < 100 || !strings.HasSuffix(string(b), "\n") {
			t.Errorf("%s: %d bytes, or no final newline", p, len(b))
		}
	}
	if _, err := Load("../x", "gemini", "v1"); err == nil {
		t.Error("a path outside the prompts: no error")
	}
	if _, err := Load("line_items", "gemini", "v9"); err == nil {
		t.Error("a missing version: no error")
	}
}
