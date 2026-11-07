package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/store/storetest"
)

func TestBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storetest.New(t)
	if _, err := db.Exec(ctx, "create table batched (n int primary key)"); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		t.Helper()
		var n int
		if err := db.QueryRow(ctx, "select count(*) from batched").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	var batch store.Batch
	if _, err := batch.Exec(ctx, "insert into batched values ($1)", 1); err != nil {
		t.Fatal(err)
	}
	var unread int
	if err := batch.QueryRow(ctx, "insert into batched values ($1) returning n", 2).Scan(&unread); err != nil || unread != 0 {
		t.Fatalf("QueryRow scanned %d, %v; want nothing", unread, err)
	}
	if _, err := batch.Query(ctx, "select n from batched"); err == nil {
		t.Error("Query succeeded, want an error")
	}
	if err := batch.Send(ctx, db); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Fatalf("got %d rows, want 2", n)
	}

	// The statements run in a transaction: if one fails, none take effect.
	batch = store.Batch{}
	if _, err := batch.Exec(ctx, "insert into batched values ($1)", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := batch.Exec(ctx, "insert into batched values ($1)", 1); err != nil {
		t.Fatal(err)
	}
	if err := batch.Send(ctx, db); err == nil {
		t.Fatal("sent a batch with a duplicate key, want an error")
	}
	if n := count(); n != 2 {
		t.Errorf("got %d rows after a failed batch, want 2", n)
	}
}

func TestBatchRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storetest.New(t)
	if _, err := db.Exec(ctx, "create table batched (n int primary key)"); err != nil {
		t.Fatal(err)
	}

	var batch store.Batch
	if _, err := batch.Exec(ctx, "insert into batched values (1), (2)"); err != nil {
		t.Fatal(err)
	}
	var inserted int64
	batch.Read(func(db store.Querier) error {
		ct, err := db.Exec(ctx, "insert into batched values (3), (4)")
		inserted = ct.RowsAffected()
		return err
	})
	var ns []int
	batch.Read(func(db store.Querier) error {
		ns = nil
		rows, err := db.Query(ctx, "select n from batched where n > $1 order by n", 1)
		if err != nil {
			return err
		}
		ns, err = pgx.CollectRows(rows, pgx.RowTo[int])
		return err
	})
	var max int
	batch.Read(func(db store.Querier) error {
		return db.QueryRow(ctx, "select max(n) from batched").Scan(&max)
	})
	if err := batch.Send(ctx, db); err != nil {
		t.Fatal(err)
	}
	if inserted != 2 {
		t.Errorf("inserted %d rows, want 2", inserted)
	}
	if want := []int{2, 3, 4}; !slices.Equal(ns, want) {
		t.Errorf("read %v, want %v", ns, want)
	}
	if max != 4 {
		t.Errorf("read max %d, want 4", max)
	}

	// An error reading a result is returned.
	batch = store.Batch{}
	batch.Read(func(db store.Querier) error {
		var n int
		return db.QueryRow(ctx, "select n from batched where n = 5").Scan(&n)
	})
	if err := batch.Send(ctx, db); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("sent a batch reading a missing row: got %v, want %v", err, pgx.ErrNoRows)
	}
}
