package documents

import (
	"errors"
	"fmt"
	"strconv"
)

// PageID names a page in a claim: its file and its number from 1.
type PageID struct {
	File string `json:"file"`
	Page int    `json:"page"`
}

// String is "file#page".
func (id PageID) String() string { return id.File + "#" + strconv.Itoa(id.Page) }

// PageFingerprint is a page and its fingerprint.
type PageFingerprint struct {
	ID          PageID
	Fingerprint string
}

// ErrNoFingerprint: a page was given without its fingerprint.
var ErrNoFingerprint = errors.New("documents: a page has no fingerprint")

// Repeats takes a claim's pages in claim order (files in the order they
// were uploaded, pages ascending) and returns, for each, the earlier page
// it repeats: the first page with the same fingerprint, or nil. A repeat is
// recorded, never dropped; M3 decides what it means.
func Repeats(pages []PageFingerprint) ([]*PageID, error) {
	first := make(map[string]PageID, len(pages))
	out := make([]*PageID, len(pages))
	for i, p := range pages {
		if p.Fingerprint == "" {
			return nil, fmt.Errorf("%w: %s", ErrNoFingerprint, p.ID)
		}
		if f, ok := first[p.Fingerprint]; ok {
			out[i] = &f
			continue
		}
		first[p.Fingerprint] = p.ID
	}
	return out, nil
}
