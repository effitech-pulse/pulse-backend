package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestCollectionApplicationOwnerReplayCancel(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	jobs, e := service.NewCollectionJobs(r, true)
	if e != nil {
		t.Fatal(e)
	}
	s, e := jobs.Submit(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = jobs.Cancel(ctx, s.ID, s.Revision+1); e == nil || e.Error() != "revision_conflict" {
		t.Fatal("revision conflict", e)
	}
	s, e = jobs.Cancel(ctx, s.ID, s.Revision)
	if e != nil || s.State != domain.CollectionCancelled {
		t.Fatal(s, e)
	}
	again, e := jobs.Cancel(ctx, s.ID, 1)
	if e != nil || again.Revision != s.Revision {
		t.Fatal("terminal cancel", again, e)
	}
	foreign := rbacReadUser(t, pool, "valid-foreign-owner", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "collectionjobs.read", "wells.read", "integrations.read")
	other := reviewActor(t, r, foreign)
	for _, jobID := range []string{s.ID, strings.Repeat("f", 32)} {
		if _, e = jobs.Status(other, jobID); e == nil || e.Error() != "collection_job_missing" {
			t.Fatal("foreign/missing leak", e)
		}
	}
	if _, e = jobs.Status(context.Background(), s.ID); e == nil || e.Error() != "session_required" {
		t.Fatal("session boundary", e)
	}
}
