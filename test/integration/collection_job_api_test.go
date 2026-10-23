package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestCollectionApplicationOwnerReplayCancel(t *testing.T) {
	_, ctx, r, in := collectionSetup(t)
	id := int64(1)
	ctx = domain.WithPrincipal(ctx, domain.Principal{AccountID: &id, Authenticated: true, Role: "admin", Mechanism: "session"})
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
	foreign := int64(1234)
	other := domain.WithPrincipal(context.Background(), domain.Principal{AccountID: &foreign, Authenticated: true, Role: "admin", Mechanism: "session"})
	for _, jobID := range []string{s.ID, strings.Repeat("f", 32)} {
		if _, e = jobs.Status(other, jobID); e == nil || e.Error() != "collection_job_missing" {
			t.Fatal("foreign/missing leak", e)
		}
	}
	if _, e = jobs.Status(context.Background(), s.ID); e == nil || e.Error() != "session_required" {
		t.Fatal("session boundary", e)
	}
}
