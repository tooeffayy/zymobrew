package cursor

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRoundTrip(t *testing.T) {
	want := time.Date(2025, 4, 30, 10, 0, 0, 123456789, time.UTC)
	wantID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	enc := Encode(want, wantID)
	gotT, gotID, err := Decode(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !gotT.Equal(want) {
		t.Errorf("time round-trip: got %s, want %s", gotT, want)
	}
	if gotID != wantID {
		t.Errorf("id round-trip: got %s, want %s", gotID, wantID)
	}
}

func TestEncodeNanosPreserved(t *testing.T) {
	// Two rows separated by a single nanosecond — distinct cursors.
	id := uuid.New()
	a := Encode(time.Unix(0, 1_000_000_001), id)
	b := Encode(time.Unix(0, 1_000_000_002), id)
	if a == b {
		t.Errorf("nanosecond resolution lost: a == b == %q", a)
	}
}

func TestDecodeEmpty(t *testing.T) {
	gotT, gotID, err := Decode("")
	if err != nil {
		t.Fatalf("empty cursor: %v", err)
	}
	if !gotT.IsZero() || gotID != uuid.Nil {
		t.Errorf("empty cursor should be zero values, got %v / %v", gotT, gotID)
	}
}

func TestDecodeMalformed(t *testing.T) {
	cases := []string{
		"not-base64!!",
		"bm90LWpzb24",                               // valid b64 of "not-json"
		"eyJ0IjogImJhZC10aW1lc3RhbXAiLCAiaSI6ICJ4In0", // {"t":"bad-timestamp","i":"x"}
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			_, _, err := Decode(c)
			if err == nil {
				t.Errorf("expected error, got nil")
				return
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("expected ErrInvalid, got %v", err)
			}
		})
	}
}

func TestEncodeIsURLSafe(t *testing.T) {
	// Cursor must survive being a query parameter — no '+' or '/' allowed.
	enc := Encode(time.Now(), uuid.New())
	for _, ch := range enc {
		if ch == '+' || ch == '/' || ch == '=' {
			t.Errorf("non-URL-safe char %q in %q", ch, enc)
		}
	}
}
