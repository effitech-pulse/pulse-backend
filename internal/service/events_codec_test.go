package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"strings"
	"testing"
)

func TestEventsLocatorRoundTrip(t *testing.T) {
	for _, l := range []domain.EventLocator{{Source: "mc", Target: "q1_исторический/Период 01?x=0 +%#"}, {Source: "review.check", Target: "same", Revision: 2}, {Source: "review.alert", Target: "9223372036854775807", Revision: 2}, {Source: "well", Target: "9223372036854775807", Revision: 3}} {
		id, err := EncodeEventID(l)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeEventID(id)
		if err != nil || got != l {
			t.Fatalf("%+v %v", got, err)
		}
	}
	for _, id := range []string{"", "e1.WyJmaWxlX2NoZWNrIiwieCJd", "e1.WyJ3ZWxsX2V2ZW50IiwiOTIyMzM3MjAzNjg1NDc3NTgwOCIsIjEiXQ", strings.Repeat("a", 2049)} {
		if _, err := DecodeEventID(id); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
}

func TestEventsLocatorCanonicalBoundaries(t *testing.T) {
	// Compact ["manual_check",target] is19 bytes overhead: target1514 -> token2047,
	// target1515 -> token2049 (2048 cannot be a canonical e1 base64url length).
	for _, tc := range []struct {
		n  int
		ok bool
	}{{1514, true}, {1515, false}} {
		_, e := EncodeEventID(domain.EventLocator{Source: "mc", Target: strings.Repeat("x", tc.n)})
		if (e == nil) != tc.ok {
			t.Fatalf("target%d => %v", tc.n, e)
		}
	}
	for _, raw := range []string{`["manual_check","x"] `, `["manual_check","x",null]`, `["review_event","alert","01","1"]`, `["review_event","check","x","0"]`, `["well_event","1","01"]`} {
		id := "e1." + base64.RawURLEncoding.EncodeToString([]byte(raw))
		if _, e := DecodeEventID(id); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestEventsCursorRejectsAuthenticatedInvalidPayloads(t *testing.T) {
	f := &eventsStore{records: []domain.EventRecord{eventsFixture(), eventsFixture()}}
	s, e := NewEvents(f, nil, EventsOptions{Mode: "research", Prefix: "/api/v1"})
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.List(eventsContext(), domain.EventFilter{Limit: 1}, "")
	if e != nil {
		t.Fatal(e)
	}
	token := *p.NextCursor
	binary, _ := base64.RawURLEncoding.DecodeString(token)
	n := s.aead.NonceSize()
	plain, e := s.aead.Open(nil, binary[:n], binary[n:], []byte("pulse.events.cursor.v1"))
	if e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*eventCursor){
		func(v *eventCursor) { v.Version = 2 },
		func(v *eventCursor) { v.Last.SourceRank = 5 },
		func(v *eventCursor) { v.Last.OccurredAt = "2026-10-22T12:00:00.1234567Z" },
		func(v *eventCursor) { v.Last.Revision = "9223372036854775808" },
	} {
		var payload eventCursor
		if e = json.Unmarshal(plain, &payload); e != nil {
			t.Fatal(e)
		}
		mutate(&payload)
		raw, _ := eventJSON(payload)
		bad := base64.RawURLEncoding.EncodeToString(s.aead.Seal(binary[:n:n], binary[:n], raw, []byte("pulse.events.cursor.v1")))
		_, err := s.List(eventsContext(), domain.EventFilter{Limit: 1}, bad)
		var de *domain.EventReadError
		if !errors.As(err, &de) || de.Detail != "cursor_restart_required" {
			t.Fatalf("invalid authenticated cursor %v", err)
		}
	}
	for _, ctx := range []context.Context{domain.WithPrincipal(context.Background(), domain.TrustedCompatibilityPrincipal(domain.Principal{Role: "viewer", AccountID: eventPtr(int64(42)), Authenticated: true, Mechanism: "session"})), domain.WithPrincipal(context.Background(), domain.TrustedCompatibilityPrincipal(domain.Principal{Role: "viewer", Mechanism: "legacy", Scope: domain.AccessScope{Funds: []string{}}}))} {
		_, err := s.List(ctx, domain.EventFilter{Limit: 1}, token)
		var de *domain.EventReadError
		if !errors.As(err, &de) {
			t.Fatal("scope binding ignored", err)
		}
	}
}
func TestEventsEntropyFailureIsStartupFailure(t *testing.T) {
	if _, e := NewEvents(&eventsStore{}, nil, EventsOptions{Mode: "research", Prefix: "/api/v1", Random: strings.NewReader("")}); e == nil {
		t.Fatal("entropy failure admitted")
	}
}
