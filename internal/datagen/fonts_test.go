package datagen

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestFontHashes pins the font files to the hashes fonts/SOURCES.md
// records. A changed font moves every packet's bytes, so it needs a new
// generator version, not a quiet swap.
func TestFontHashes(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"NotoSans-Regular.ttf", "f3961a9cde016d41a4879aecda1474d3a36d6bf54fa0e4643de029cc2248b0e8"},
		{"NotoSans-Bold.ttf", "87cb2d84472a7d66da659ee47b6cdb9552326e8c128245231f191b6ac72529d9"},
		{"OFL.txt", "cee9892f9f0cc8fe882c9e9537ee6a89621d86ee7ceaf70b02e2b2b1c25c061a"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("fonts", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			h := sha256.Sum256(b)
			if got := hex.EncodeToString(h[:]); got != tt.want {
				t.Errorf("sha256 %s, want %s", got, tt.want)
			}
		})
	}
	// The embedded copies are the files on disk.
	for name, b := range map[string][]byte{"NotoSans-Regular.ttf": notoRegular, "NotoSans-Bold.ttf": notoBold} {
		disk, err := os.ReadFile(filepath.Join("fonts", name))
		if err != nil {
			t.Fatal(err)
		}
		if string(disk) != string(b) {
			t.Errorf("embedded %s differs from the file", name)
		}
	}
}
