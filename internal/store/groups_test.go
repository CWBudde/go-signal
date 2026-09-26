//go:build cgo

package store_test

import (
	"io"
	"slices"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
)

const clubID = "id-club"

func TestGroupRecords(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := openAccount(t, openDir(t, io.Discard))

	updated := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	family := store.GroupRecord{ID: "id-family", Title: "Family", Revision: 3, UpdatedAt: updated}
	club := store.GroupRecord{ID: clubID, Title: "Club", Revision: 12, LeftAt: updated.Add(time.Hour), UpdatedAt: updated}

	for _, rec := range []store.GroupRecord{family, club} {
		err := data.PutGroup(ctx, rec)
		if err != nil {
			t.Fatalf("PutGroup: %v", err)
		}
	}

	rec, ok, err := data.Group(ctx, club.ID)
	if err != nil || !ok || rec != club {
		t.Errorf("Group = %+v, %v, %v; want %+v", rec, ok, err, club)
	}

	_, ok, err = data.Group(ctx, "missing")
	if err != nil || ok {
		t.Errorf("missing group = %v, %v", ok, err)
	}

	all, err := data.Groups(ctx)
	if err != nil || !slices.Equal(all, []store.GroupRecord{club, family}) {
		t.Errorf("Groups = %+v, %v; want club and family, sorted by ID", all, err)
	}
}

func TestGroupRecordReplaced(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := openDir(t, io.Discard)
	data := openAccount(t, dir)

	left := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)

	err := data.PutGroup(ctx, store.GroupRecord{ID: clubID, Title: "Club", LeftAt: left, UpdatedAt: left})
	if err != nil {
		t.Fatalf("PutGroup: %v", err)
	}

	// A later fetch replaces the record; a zero LeftAt clears it (we rejoined).
	rejoined := store.GroupRecord{ID: clubID, Title: "Club 2", Revision: 14, UpdatedAt: left.Add(time.Hour)}

	err = data.PutGroup(ctx, rejoined)
	if err != nil {
		t.Fatalf("PutGroup: %v", err)
	}

	err = data.Close()
	if err != nil {
		t.Fatal(err)
	}

	// The table survives reopening.
	all, err := openAccount(t, dir).Groups(ctx)
	if err != nil || !slices.Equal(all, []store.GroupRecord{rejoined}) {
		t.Errorf("Groups = %+v, %v; want %+v", all, err, rejoined)
	}
}
