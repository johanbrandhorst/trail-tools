package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/store/storetest"
)

func TestReadSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storetest.New(t)
	if _, err := db.Exec(ctx, "create table snapshotted (n int primary key)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "insert into snapshotted values (1)"); err != nil {
		t.Fatal(err)
	}
	count := func(b *store.Batch, n *int) {
		b.Read(func(db store.Querier) error {
			return db.QueryRow(ctx, "select count(*) from snapshotted").Scan(n)
		})
	}

	var before, after int
	err := db.ReadSnapshot(ctx, func(b *store.Batch) error {
		count(b, &before)
		return nil
	}, func(b *store.Batch) error {
		// A row inserted after the first batch isn't in the snapshot.
		if _, err := db.Exec(ctx, "insert into snapshotted values (2)"); err != nil {
			return err
		}
		count(b, &after)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if before != 1 || after != 1 {
		t.Errorf("counted %d then %d rows, want 1 both times", before, after)
	}

	// The transaction is read only.
	err = db.ReadSnapshot(ctx, func(b *store.Batch) error {
		_, err := b.Exec(ctx, "insert into snapshotted values (3)")
		return err
	})
	if err == nil {
		t.Error("wrote in a snapshot, want an error")
	}
}

func TestTransact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storetest.New(t)
	if _, err := db.Exec(ctx, "create table transacted (n int primary key)"); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		t.Helper()
		var n int
		if err := db.QueryRow(ctx, "select count(*) from transacted").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	insert := func(b *store.Batch, n int) {
		if _, err := b.Exec(ctx, "insert into transacted values ($1)", n); err != nil {
			t.Fatal(err)
		}
	}

	var inserted int
	err := db.Transact(ctx, func(b *store.Batch) error {
		insert(b, 1)
		b.Read(func(db store.Querier) error {
			return db.QueryRow(ctx, "select count(*) from transacted").Scan(&inserted)
		})
		return nil
	}, func(b *store.Batch) error {
		insert(b, inserted+1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Fatalf("got %d rows, want 2", n)
	}

	// The transaction is rolled back if a batch can't be queued, or fails.
	conns := db.Stat().NewConnsCount()
	errStop := errors.New("stop")
	err = db.Transact(ctx, func(b *store.Batch) error {
		insert(b, 3)
		return nil
	}, func(*store.Batch) error {
		return errStop
	})
	if !errors.Is(err, errStop) {
		t.Errorf("got %v, want %v", err, errStop)
	}
	err = db.Transact(ctx, func(b *store.Batch) error {
		insert(b, 3)
		return nil
	}, func(b *store.Batch) error {
		insert(b, 1)
		return nil
	})
	if err == nil {
		t.Error("sent a batch with a duplicate key, want an error")
	}
	if n := count(); n != 2 {
		t.Errorf("got %d rows after failed transactions, want 2", n)
	}

	// The connections of the failed transactions were rolled back and
	// reused, rather than closed.
	if n := db.Stat().NewConnsCount() - conns; n != 0 {
		t.Errorf("opened %d more connections, want none", n)
	}
}
