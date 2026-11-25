package datagen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// LoadSpecs reads every *.yaml in fsys's root as a spec, sorted by ID.
func LoadSpecs(fsys fs.FS) ([]Spec, error) {
	paths, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		return nil, err
	}
	specs := make([]Spec, 0, len(paths))
	for _, p := range paths { // fs.Glob sorts
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, err
		}
		s, err := ParseSpec(strings.TrimSuffix(path.Base(p), ".yaml"), src)
		if err != nil {
			return nil, err
		}
		specs = append(specs, s)
	}
	return specs, nil
}

// TruthJSON encodes truth as indented JSON with LF endings and a final
// newline, leaving ₹ and other non-ASCII text as UTF-8.
func TruthJSON(t Truth) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(t); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WritePacket writes a packet's files and truth.json into dir/<scenario>/,
// replacing what's there.
func WritePacket(dir string, p Packet) error {
	out := filepath.Join(dir, p.Truth.Scenario)
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o750); err != nil {
		return err
	}
	for _, f := range p.Files {
		if err := os.WriteFile(filepath.Join(out, f.Name), f.Data, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", f.Name, err)
		}
	}
	truth, err := TruthJSON(p.Truth)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "truth.json"), truth, 0o600)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
