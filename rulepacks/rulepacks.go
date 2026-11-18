// Package rulepacks embeds the rule packs, one YAML file per version of a
// policy wording, and parses them into a registry. It sits outside
// internal/rules because the engine does no I/O and can't import io/fs
// (ADR 0005); the engine's parser takes the bytes this package hands it.
//
// A published pack never changes. A change to one means a new version in a
// new file.
package rulepacks

import (
	"embed"
	"fmt"
	"io/fs"
	"sync"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

//go:embed */*.yaml
var files embed.FS

// Paths lists the embedded pack files, such as
// "arogya-sanjeevani/godigit-2024.yaml", in lexical order.
func Paths() ([]string, error) {
	return fs.Glob(files, "*/*.yaml")
}

// Source returns the bytes of one embedded pack file.
func Source(path string) ([]byte, error) {
	return files.ReadFile(path)
}

var registry = sync.OnceValues(func() (*rules.Registry, error) {
	paths, err := Paths()
	if err != nil {
		return nil, err
	}
	packs := make([]*rules.Pack, 0, len(paths))
	for _, path := range paths {
		src, err := Source(path)
		if err != nil {
			return nil, err
		}
		p, err := rules.ParsePack(src)
		if err != nil {
			return nil, fmt.Errorf("rulepacks: %s: %w", path, err)
		}
		packs = append(packs, p)
	}
	return rules.NewRegistry(packs...)
})

// Registry parses every embedded pack, once, and returns them as a registry.
func Registry() (*rules.Registry, error) {
	return registry()
}
