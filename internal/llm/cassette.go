package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

// CassetteFormat is the version of the cassette file's layout.
const CassetteFormat = 1

// Cassette is one recorded answer (ADR 0014): what the request hash covers,
// the output exactly as returned, the stop reason, tokens, the model that
// answered, the cost and when it was recorded. A failed stop is recorded
// like any other answer, so replay fails the same way.
type Cassette struct {
	Format       int            `json:"format"`
	Hash         string         `json:"hash"`
	Request      RequestSummary `json:"request"`
	Output       string         `json:"output"`
	Stop         StopReason     `json:"stop"`
	StopDetail   string         `json:"stop_detail"`
	Usage        Usage          `json:"usage"`
	ModelVersion string         `json:"model_version"`
	CostMicros   int64          `json:"cost_usd_micros"`
	RecordedAt   string         `json:"recorded_at"` // RFC 3339, UTC, whole seconds
}

// Response returns the recorded answer.
func (c Cassette) Response() Response {
	return Response{Output: c.Output, Stop: c.Stop, StopDetail: c.StopDetail, Usage: c.Usage, ModelVersion: c.ModelVersion}
}

// CassetteStore keeps cassettes as <dir>/<job>/<hash>.json, one per request.
type CassetteStore struct{ dir string }

// NewCassetteStore returns a store rooted at dir.
func NewCassetteStore(dir string) *CassetteStore { return &CassetteStore{dir: dir} }

func (s *CassetteStore) path(job, hash string) string {
	return filepath.Join(s.dir, job, hash+".json")
}

// Load returns the cassette for a summarized request, or ErrCassetteMiss.
// A cassette whose contents don't match its name or the request is an
// error, never a miss: nobody edits cassettes by hand (CLAUDE.md, rule 9).
func (s *CassetteStore) Load(sum RequestSummary, hash string) (Cassette, error) {
	f, err := os.Open(s.path(sum.Job, hash))
	if errors.Is(err, fs.ErrNotExist) {
		return Cassette{}, fmt.Errorf("%w: job %s, request %s", ErrCassetteMiss, sum.Job, hash)
	}
	if err != nil {
		return Cassette{}, err
	}
	defer f.Close()
	var c Cassette
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Cassette{}, fmt.Errorf("llm: cassette %s/%s: %w", sum.Job, hash, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Cassette{}, fmt.Errorf("llm: cassette %s/%s: data after the cassette", sum.Job, hash)
	}
	if c.Format != CassetteFormat {
		return Cassette{}, fmt.Errorf("llm: cassette %s/%s: format %d, want %d", sum.Job, hash, c.Format, CassetteFormat)
	}
	got, err := c.Request.Hash()
	if err != nil {
		return Cassette{}, err
	}
	if c.Hash != hash || got != hash {
		return Cassette{}, fmt.Errorf("llm: cassette %s/%s: its request hashes to %s and it names %s", sum.Job, hash, got, c.Hash)
	}
	if !reflect.DeepEqual(c.Request, sum) {
		return Cassette{}, fmt.Errorf("llm: cassette %s/%s: its request differs from this one", sum.Job, hash)
	}
	return c, nil
}

// Save writes a cassette with LF endings, replacing the file atomically.
func (s *CassetteStore) Save(c Cassette) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	path := s.path(c.Request.Job, c.Hash)
	return writeFileAtomic(path, b)
}

// writeFileAtomic writes to a temporary file beside path, then renames it.
func writeFileAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := io.Copy(tmp, bytes.NewReader(b))
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// recordedAt formats a recording time.
func recordedAt(t time.Time) string { return t.UTC().Truncate(time.Second).Format(time.RFC3339) }
