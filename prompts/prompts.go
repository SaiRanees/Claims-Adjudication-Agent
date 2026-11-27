// Package prompts embeds each job's system prompts, versioned per provider
// (architecture.md): prompts/<job>/<provider>/<version>.md. A prompt's
// SHA-256 is part of the request hash, so editing one invalidates its
// cassettes; a change worth keeping apart gets a new version file instead.
// The files have LF endings on every OS (.gitattributes), and Load refuses
// one that doesn't.
package prompts

import (
	"bytes"
	"embed"
	"fmt"
	"path"
	"regexp"
)

//go:embed */*/*.md
var files embed.FS

var name = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)

// Load returns a prompt's text.
func Load(job, provider, version string) ([]byte, error) {
	for _, s := range []string{job, provider, version} {
		if !name.MatchString(s) {
			return nil, fmt.Errorf("prompts: bad name %q", s)
		}
	}
	b, err := files.ReadFile(path.Join(job, provider, version+".md"))
	if err != nil {
		return nil, fmt.Errorf("prompts: %s/%s/%s: %w", job, provider, version, err)
	}
	if bytes.ContainsRune(b, '\r') {
		return nil, fmt.Errorf("prompts: %s/%s/%s has CR line endings", job, provider, version)
	}
	return b, nil
}
