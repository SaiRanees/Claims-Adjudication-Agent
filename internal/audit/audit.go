package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ActorType says what kind of actor caused an event. The database's CHECK list mirrors it.
type ActorType string

// The actor types.
const (
	ActorUser    ActorType = "user"
	ActorService ActorType = "service"
	ActorModel   ActorType = "model"
)

// The actions written so far. An action is lower-case words joined by dots, as the
// database's CHECK requires.
const (
	ActionClaimCreated       = "claim.created"
	ActionDocumentRegistered = "document.registered"
	ActionDocumentUploaded   = "document.uploaded"
	ActionDocumentRejected   = "document.rejected"
	ActionDocumentAccepted   = "document.accepted"
	ActionClaimSubmitted     = "claim.submitted"
)

// Actor is who caused an event: a user's subject, a service's name or a model call's ID.
type Actor struct {
	Type ActorType
	ID   string
}

// Details are an event's facts: IDs, codes, sizes and amounts, never names, file names or
// document text. Each value is a string or an integer (int, int32 or int64), so that the
// canonical encoding has exactly one form for it.
type Details map[string]any

// Event is one entry in a claim's chain.
type Event struct {
	ClaimID uuid.UUID
	// Seq is the event's place in the claim's chain, from 1.
	Seq int32
	// OccurredAt is kept to the microsecond, as Postgres stores it.
	OccurredAt time.Time
	Actor      Actor
	Action     string
	Details    Details
}

// Record is an event as stored: with the previous event's hash, and its own.
type Record struct {
	Event
	// PrevHash is nil on a claim's first event.
	PrevHash []byte
	Hash     []byte
}

// Entry is what a caller appends. Append gives it its sequence number and hashes.
type Entry struct {
	ClaimID uuid.UUID
	At      time.Time
	Actor   Actor
	Action  string
	Details Details
}

// Append adds an event to the end of its claim's chain, in tx, and returns it as stored.
//
// The caller must already hold the claim's row lock (SELECT ... FOR UPDATE), taken before
// it read anything it checked. The lock is what gives the event the next sequence number
// without a race; without it, two writers would take the same number and one would fail
// on the chain's unique key. The time is truncated to the microsecond, as Postgres keeps
// it, so the hash computed here is the hash of the row as stored.
func Append(ctx context.Context, tx pgx.Tx, e Entry) (Record, error) {
	details, err := normalize(e.Details)
	if err != nil {
		return Record{}, fmt.Errorf("audit event %s: %w", e.Action, err)
	}
	ev := Event{
		ClaimID:    e.ClaimID,
		OccurredAt: e.At.UTC().Truncate(time.Microsecond),
		Actor:      e.Actor,
		Action:     e.Action,
		Details:    details,
	}
	var (
		lastSeq int32
		prev    []byte
	)
	err = tx.QueryRow(ctx, `
		SELECT seq, hash FROM audit_events WHERE claim_id = $1 ORDER BY seq DESC LIMIT 1`,
		e.ClaimID).Scan(&lastSeq, &prev)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		prev = nil
	case err != nil:
		return Record{}, fmt.Errorf("reading the end of claim %s's audit chain: %w", e.ClaimID, err)
	}
	ev.Seq = lastSeq + 1

	hash, err := Hash(prev, ev)
	if err != nil {
		return Record{}, fmt.Errorf("audit event %s: %w", e.Action, err)
	}
	// jsonb keeps the values and reorders the keys; the hash never depends on this text.
	stored, err := json.Marshal(details)
	if err != nil {
		return Record{}, fmt.Errorf("encoding the details of audit event %s: %w", e.Action, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (claim_id, seq, occurred_at, actor_type, actor_id, action,
		                          details, prev_hash, hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		ev.ClaimID, ev.Seq, ev.OccurredAt, string(ev.Actor.Type), ev.Actor.ID, ev.Action,
		stored, prev, hash[:]); err != nil {
		return Record{}, fmt.Errorf("appending audit event %s to claim %s: %w", e.Action, e.ClaimID, err)
	}
	return Record{Event: ev, PrevHash: prev, Hash: hash[:]}, nil
}

// zeroHash stands in for the previous hash of a claim's first event.
var zeroHash [sha256.Size]byte

// Hash returns an event's hash: SHA-256 over the previous event's hash, or 32 zero bytes
// for a claim's first event, followed by the event's canonical encoding (see Canonical).
func Hash(prev []byte, e Event) ([sha256.Size]byte, error) {
	switch {
	case prev == nil:
		prev = zeroHash[:]
	case len(prev) != sha256.Size:
		return [sha256.Size]byte{}, fmt.Errorf("the previous hash is %d bytes, not %d", len(prev), sha256.Size)
	}
	canonical, err := Canonical(e)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	h := sha256.New()
	h.Write(prev)
	h.Write(canonical)
	var sum [sha256.Size]byte
	h.Sum(sum[:0])
	return sum, nil
}

// Canonical returns the encoding an event's hash covers: compact JSON with every object's
// keys in byte order and no HTML escaping, otherwise as Go's encoding/json writes it.
//
//	{"action":"claim.created","actorId":"…","actorType":"user","claimId":"…",
//	 "details":{…},"occurredAt":"2026-11-24T06:41:21.468856Z","seq":1}
//
// claimId is the UUID in lower-case hex with hyphens; occurredAt is RFC 3339 in UTC with
// exactly six fractional digits, cut (not rounded) to the microsecond; seq and integer
// details are decimal. It is built from the values, never from text read back out of
// jsonb, which reorders keys.
func Canonical(e Event) ([]byte, error) {
	details, err := normalize(e.Details)
	if err != nil {
		return nil, err
	}
	for _, s := range []string{e.Actor.ID, string(e.Actor.Type), e.Action} {
		if err := checkString(s); err != nil {
			return nil, err
		}
	}
	// encoding/json writes a map's keys sorted, at every level.
	doc := map[string]any{
		"action":     e.Action,
		"actorId":    e.Actor.ID,
		"actorType":  string(e.Actor.Type),
		"claimId":    e.ClaimID.String(),
		"details":    map[string]any(details),
		"occurredAt": e.OccurredAt.UTC().Format("2006-01-02T15:04:05.000000Z07:00"),
		"seq":        e.Seq,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encoding the event: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// normalize checks that every value is a string or an integer, and returns a copy with
// every integer as an int64.
func normalize(d Details) (Details, error) {
	out := make(Details, len(d))
	for k, v := range d {
		if k == "" {
			return nil, errors.New("a detail has an empty key")
		}
		if err := checkString(k); err != nil {
			return nil, fmt.Errorf("detail key %q: %w", k, err)
		}
		switch v := v.(type) {
		case string:
			if err := checkString(v); err != nil {
				return nil, fmt.Errorf("detail %s: %w", k, err)
			}
			out[k] = v
		case int:
			out[k] = int64(v)
		case int32:
			out[k] = int64(v)
		case int64:
			out[k] = v
		default:
			return nil, fmt.Errorf("detail %s is a %T; details hold only strings and integers", k, v)
		}
	}
	return out, nil
}

// checkString refuses what encoding/json would rewrite (invalid UTF-8 becomes U+FFFD, so two
// different strings would hash alike) and what Postgres can't store (NUL).
func checkString(s string) error {
	if !utf8.ValidString(s) {
		return errors.New("not valid UTF-8")
	}
	if strings.ContainsRune(s, 0) {
		return errors.New("contains NUL")
	}
	return nil
}

// Querier is what List needs: a pgx pool, connection or transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// List returns a claim's events in chain order, oldest first, with their details decoded
// from jsonb into strings and int64s.
func List(ctx context.Context, q Querier, claimID uuid.UUID) ([]Record, error) {
	rows, err := q.Query(ctx, `
		SELECT claim_id, seq, occurred_at, actor_type, actor_id, action, details, prev_hash, hash
		FROM audit_events
		WHERE claim_id = $1
		ORDER BY seq`, claimID)
	if err != nil {
		return nil, fmt.Errorf("listing claim %s's audit events: %w", claimID, err)
	}
	records, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Record, error) {
		var (
			r         Record
			actorType string
			details   []byte
		)
		if err := row.Scan(&r.ClaimID, &r.Seq, &r.OccurredAt, &actorType, &r.Actor.ID, &r.Action,
			&details, &r.PrevHash, &r.Hash); err != nil {
			return Record{}, err
		}
		r.Actor.Type = ActorType(actorType)
		r.OccurredAt = r.OccurredAt.UTC()
		d, err := decodeDetails(details)
		if err != nil {
			return Record{}, fmt.Errorf("event %d: %w", r.Seq, err)
		}
		r.Details = d
		return r, nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading claim %s's audit events: %w", claimID, err)
	}
	return records, nil
}

// decodeDetails reads details back out of jsonb. Numbers are decoded as int64s from their
// decimal text, so no value passes through a float.
func decodeDetails(raw []byte) (Details, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var fields map[string]any
	if err := dec.Decode(&fields); err != nil {
		return nil, fmt.Errorf("decoding details: %w", err)
	}
	out := make(Details, len(fields))
	for k, v := range fields {
		switch v := v.(type) {
		case string:
			out[k] = v
		case json.Number:
			n, err := strconv.ParseInt(v.String(), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("detail %s is %s, not an integer", k, v)
			}
			out[k] = n
		default:
			return nil, fmt.Errorf("detail %s is a %T; details hold only strings and integers", k, v)
		}
	}
	return out, nil
}

// Verify checks one claim's chain as List returns it: the sequence runs 1, 2, 3 and so on,
// each event names the previous event's hash, and every hash recomputes from its event's
// content. It returns the first break it finds.
func Verify(records []Record) error {
	var prev []byte
	for i, r := range records {
		if int(r.Seq) != i+1 {
			return fmt.Errorf("event %d: sequence number is %d, want %d", i+1, r.Seq, i+1)
		}
		if i > 0 && r.ClaimID != records[0].ClaimID {
			return fmt.Errorf("event %d belongs to claim %s, not %s", r.Seq, r.ClaimID, records[0].ClaimID)
		}
		if !bytes.Equal(r.PrevHash, prev) {
			return fmt.Errorf("event %d: previous hash is %s, want %s", r.Seq, hex.EncodeToString(r.PrevHash), hex.EncodeToString(prev))
		}
		sum, err := Hash(r.PrevHash, r.Event)
		if err != nil {
			return fmt.Errorf("event %d: %w", r.Seq, err)
		}
		if !bytes.Equal(sum[:], r.Hash) {
			return fmt.Errorf("event %d: stored hash %s doesn't recompute (got %s)", r.Seq, hex.EncodeToString(r.Hash), hex.EncodeToString(sum[:]))
		}
		prev = r.Hash
	}
	return nil
}
