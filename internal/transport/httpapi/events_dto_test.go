package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func eventsNativeJSON(t *testing.T, raw json.RawMessage, numericID bool) []byte {
	t.Helper()
	var v any
	if e := json.Unmarshal(raw, &v); e != nil {
		t.Fatal(e)
	}
	var walk func(any)
	walk = func(x any) {
		switch x := x.(type) {
		case []any:
			for _, v := range x {
				walk(v)
			}
		case map[string]any:
			for k, v := range x {
				numeric := false
				switch k {
				case "revision", "well_id", "ngdu", "department", "unitek_id", "armits_id", "n_windows", "channels_used", "features_total", "windows_running", "features_unavailable", "windows_above_watch", "windows_above_alert", "span_days", "k_windows":
					numeric = true
				case "id":
					numeric = numericID
				}
				if s, ok := v.(string); ok && numeric {
					x[k] = json.Number(s)
				} else {
					walk(v)
				}
			}
		}
	}
	walk(v)
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestEventsDTOFrozenExamples(t *testing.T) {
	raw, e := os.ReadFile("testdata/events_examples.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixtures struct {
		Responses struct {
			Details []json.RawMessage `json:"details"`
			Manual  []json.RawMessage `json:"manual_variants"`
		} `json:"responses"`
	}
	if e = json.Unmarshal(raw, &fixtures); e != nil {
		t.Fatal(e)
	}
	examples := append(fixtures.Responses.Details, fixtures.Responses.Manual...)
	if len(examples) < 6 {
		t.Fatal("missing frozen fixtures")
	}
	for _, fixture := range examples {
		var example struct {
			Name     string `json:"name"`
			Response struct {
				Body struct {
					Event  eventSummaryDTO `json:"event"`
					Detail json.RawMessage `json:"detail"`
				} `json:"body"`
			} `json:"response"`
		}
		if e = json.Unmarshal(fixture, &example); e != nil {
			t.Fatal(e)
		}
		t.Run(example.Name, func(t *testing.T) {
			body := example.Response.Body
			w := body.Event
			l, e := service.DecodeEventID(w.EventID)
			if e != nil {
				t.Fatal(e)
			}
			at, e := time.Parse(time.RFC3339Nano, w.OccurredAt)
			if e != nil {
				t.Fatal(e)
			}
			wellID, e := strconv.ParseInt(w.Well.ID, 10, 64)
			if e != nil {
				t.Fatal(e)
			}
			r := domain.EventRecord{Locator: l, Key: domain.EventKey{OccurredAt: at}, Well: domain.Well{ID: wellID, WellNumber: w.Well.WellNumber, Active: w.Well.Active}, ActorSnapshot: &domain.EventActorSnapshot{ID: w.Actor.ID, Name: w.Actor.Name, Authenticated: w.Actor.Authenticated}, Verdict: w.Outcome.Verdict, ReviewStatus: w.Outcome.ReviewStatus, Conclusion: w.Outcome.Conclusion}
			if w.Well.ArchivedAt != nil {
				x, e := time.Parse(time.RFC3339Nano, *w.Well.ArchivedAt)
				if e != nil {
					t.Fatal(e)
				}
				r.Well.ArchivedAt = &x
			}
			switch l.Source {
			case "mc":
				// Snake-case header is decoded separately to native time/int64 fields.
				var header struct {
					Check struct {
						ID        string    `json:"id"`
						WellID    string    `json:"well_id"`
						CreatedAt time.Time `json:"created_at"`
						Start     time.Time `json:"start"`
						End       time.Time `json:"end"`
					} `json:"check"`
					Result json.RawMessage `json:"result"`
				}
				if e = json.Unmarshal(body.Detail, &header); e != nil {
					t.Fatal(e)
				}
				c := domain.ManualCheck{ID: header.Check.ID, WellID: wellID, CreatedAt: header.Check.CreatedAt, Start: header.Check.Start, End: header.Check.End}
				if e = json.Unmarshal(eventsNativeJSON(t, header.Result, false), &c.Result); e != nil {
					t.Fatal(e)
				}
				var verdict struct {
					Verdict *string `json:"verdict"`
				}
				_ = json.Unmarshal(header.Result, &verdict)
				r.Verdict = verdict.Verdict
				c.Result.Filename = "forbidden.csv"
				r.Check = &c
			case "review.check", "review.alert":
				var review domain.ReviewEvent
				if e = json.Unmarshal(eventsNativeJSON(t, body.Detail, false), &review); e != nil {
					t.Fatal(e)
				}
				r.Review = &review
			case "well":
				var d struct {
					Revision      int64     `json:"revision"`
					CreatedAt     time.Time `json:"created_at"`
					ChangeKind    string    `json:"change_kind"`
					Before, After domain.Well
					Reason        string
				}
				if e = json.Unmarshal(eventsNativeJSON(t, body.Detail, true), &d); e != nil {
					t.Fatal(e)
				}
				r.WellRevision = &domain.WellEvent{Revision: d.Revision, CreatedAt: d.CreatedAt, Kind: d.ChangeKind, Before: d.Before, After: d.After, Reason: d.Reason}
			}
			if r.WellRevision != nil {
				r.ChangeKind = r.WellRevision.Kind
			}
			// Exercise the real service projection: expected summary remains frozen data.
			events, e := service.NewEvents(eventHTTPStore{record: r, records: []domain.EventRecord{r}}, nil, service.EventsOptions{Mode: "research", Prefix: "/api/v1"})
			if e != nil {
				t.Fatal(e)
			}
			detail, e := events.Get(domain.WithPrincipal(context.Background(), domain.TrustedLegacyPrincipal("test")), w.EventID)
			if e != nil {
				t.Fatal(e)
			}
			page, e := events.List(domain.WithPrincipal(context.Background(), domain.TrustedLegacyPrincipal("test")), domain.EventFilter{Types: []string{w.EventType}}, "")
			if e != nil || len(page.Items) != 1 {
				t.Fatalf("frozen type filter/list: %+v %v", page, e)
			}
			if summary := toEventSummaryDTO(page.Items[0]); !reflect.DeepEqual(summary, w) {
				t.Fatalf("service-produced List summary differs: got %+v want %+v", summary, w)
			}

			got, e := toEventDetailDTO(detail)
			if e != nil {
				t.Fatal(e)
			}
			encoded, e := json.Marshal(got)
			if e != nil {
				t.Fatal(e)
			}
			var gotJSON, wantJSON any
			_ = json.Unmarshal(encoded, &gotJSON)
			wantBytes, _ := json.Marshal(body)
			_ = json.Unmarshal(wantBytes, &wantJSON)
			if !reflect.DeepEqual(gotJSON, wantJSON) {
				t.Fatalf("DTO differs\ngot %s\nwant %s", encoded, wantBytes)
			}
		})
	}
}
