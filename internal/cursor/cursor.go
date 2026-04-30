// Package cursor implements opaque keyset pagination cursors for list
// endpoints. A cursor is the (sort_key, id) tuple of the last row from
// the previous page; the next page is everything strictly after that
// tuple under the list's ordering.
//
// Wire format: base64-url(no-padding) of a small JSON object
// `{"t":"<RFC3339Nano>","i":"<uuid>"}`. Encoded length is ~80 chars,
// well within URL-param budgets. The format is deliberately not part
// of the API contract — clients should treat cursors as opaque blobs
// and only pass back what the server returned.
package cursor

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrInvalid is returned when the cursor string is malformed. Handlers
// should map this to 400.
var ErrInvalid = errors.New("invalid cursor")

type payload struct {
	T string `json:"t"`
	I string `json:"i"`
}

// Encode returns an opaque cursor for the given (timestamp, id) pair.
// The timestamp is serialized at nanosecond precision so adjacent rows
// with the same wall-clock millisecond don't collide.
func Encode(t time.Time, id uuid.UUID) string {
	b, _ := json.Marshal(payload{
		T: t.UTC().Format(time.RFC3339Nano),
		I: id.String(),
	})
	return base64.RawURLEncoding.EncodeToString(b)
}

// Decode parses a cursor produced by Encode. An empty string returns
// the zero values with no error — callers treat that as "start of list".
func Decode(s string) (time.Time, uuid.UUID, error) {
	if s == "" {
		return time.Time{}, uuid.Nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	t, err := time.Parse(time.RFC3339Nano, p.T)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: bad timestamp", ErrInvalid)
	}
	id, err := uuid.Parse(p.I)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: bad uuid", ErrInvalid)
	}
	return t, id, nil
}
