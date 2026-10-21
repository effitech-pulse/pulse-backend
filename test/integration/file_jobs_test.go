package integration

import (
	"sync"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestFileJobAcceptAtomicRollback(t *testing.T) {
	pool, ctx, store, _, v := fileJobSetup(t)
	if _, e := pool.Exec(ctx, `CREATE FUNCTION fail_q1_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected outbox boundary'; END $$;CREATE TRIGGER fail_q1 BEFORE INSERT ON file_job_outbox FOR EACH ROW EXECUTE FUNCTION fail_q1_outbox()`); e != nil {
		t.Fatal(e)
	}
	if _, e := store.AcceptFileJob(ctx, v); e == nil {
		t.Fatal("accept acknowledged without outbox")
	}
	for _, table := range []string{"file_input", "file_input_payload", "file_job", "file_job_outbox"} {
		var n int
		if e := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
	var n int
	if e := pool.QueryRow(ctx, `SELECT retained_count FROM file_input_budget WHERE budget_key='global'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("quota not rolled back", n, e)
	}
	if _, e := pool.Exec(ctx, `DROP TRIGGER fail_q1 ON file_job_outbox`); e != nil {
		t.Fatal(e)
	}
	if got, e := store.AcceptFileJob(ctx, v); e != nil || got.ID != v.Input.ID || got.State != domain.FileJobPendingDispatch {
		t.Fatal(got, e)
	}
}
func TestFileJobSameKeyRace(t *testing.T) {
	pool, ctx, store, _, v := fileJobSetup(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, e := store.AcceptFileJob(ctx, v)
			if e == nil && got.ID != v.Input.ID {
				t.Error("different job")
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var n int
	if e := pool.QueryRow(ctx, `SELECT retained_count FROM file_input_budget WHERE budget_key='global'`).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}
func TestFileJobPreparedDuplicateWithoutPayload(t *testing.T) {
	_, ctx, store, checks, v := fileJobSetup(t)
	first, e := store.AcceptFileJob(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	repeat := prepareSaved(t, checks, ctx, v.Input.RequestKey)
	if len(repeat.Canonical) != 0 {
		t.Fatal("expected metadata-only duplicate")
	}
	got, e := store.AcceptFileJob(ctx, domain.FileJobPrepared{Input: repeat})
	if e != nil || got.ID != first.ID {
		t.Fatal(got, e)
	}
	repeat.RequestCommitmentSHA256 = domain.FileSHA256([]byte("other"))
	if _, e = store.AcceptFileJob(ctx, domain.FileJobPrepared{Input: repeat}); e == nil {
		t.Fatal("commitment conflict accepted")
	}
}
