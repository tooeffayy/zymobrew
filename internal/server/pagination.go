package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"zymobrew/internal/cursor"
)

// Pagination is enforced server-side; per-endpoint limits are bounded by
// these values so a single client can't ask for an arbitrarily large
// page. defaultPageLimit reflects the size most clients want without
// scrolling more than a screen of results.
const (
	defaultPageLimit = 50
	maxPageLimit     = 100
)

// listPagination is what list handlers receive after parsing the query
// string. Pass CursorTs/CursorID directly into sqlc list params; an
// empty cursor leaves both Valid=false, which the queries' IS NULL
// guard treats as "first page".
type listPagination struct {
	CursorTs pgtype.Timestamptz
	CursorID uuid.NullUUID
	Limit    int32
}

// parseListPagination reads `cursor` and `limit` from the URL query and
// returns the values ready to pass into a keyset-paginated sqlc query.
// Writes a 400 and returns ok=false if either is malformed; handlers
// short-circuit on that.
func parseListPagination(w http.ResponseWriter, r *http.Request) (listPagination, bool) {
	p := listPagination{Limit: defaultPageLimit}

	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxPageLimit {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid limit"})
			return listPagination{}, false
		}
		p.Limit = int32(n)
	}

	if v := r.URL.Query().Get("cursor"); v != "" {
		t, id, err := cursor.Decode(v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid cursor"})
			return listPagination{}, false
		}
		p.CursorTs = pgtype.Timestamptz{Time: t, Valid: true}
		p.CursorID = uuid.NullUUID{UUID: id, Valid: true}
	}

	return p, true
}

// nextCursor returns the cursor for the next page given the size of the
// page we just served and a callback that produces the (sort_key, id)
// tuple of the last row. Returns "" when the page wasn't full — that's
// the signal there's nothing left to fetch.
func nextCursor(pageSize int, requestedLimit int32, last func() (time.Time, uuid.UUID)) string {
	if pageSize == 0 || int32(pageSize) < requestedLimit {
		return ""
	}
	t, id := last()
	return cursor.Encode(t, id)
}

// nullableCursor wraps a cursor string for JSON encoding so the empty
// string serialises as `null` rather than `""`. Clients then have a
// single end-of-list signal: `next_cursor === null`.
func nullableCursor(s string) any {
	if s == "" {
		return nil
	}
	return s
}
