package jobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestCohortEnvelopeIsStrictAndOpaque(t *testing.T) {
	valid := domain.CohortJobMessage{Version: 1, Kind: "cohort.ready.v1", EventID: strings.Repeat("a", 32), JobID: strings.Repeat("b", 32), Generation: 8}
	for _, name := range []string{"valid", "unknown_field", "wrong_kind", "generation", "message_id", "oversized"} {
		t.Run(name, func(t *testing.T) {
			msg := valid
			if name == "wrong_kind" {
				msg.Kind = "file_check.ready.v1"
			}
			if name == "generation" {
				msg.Generation = 9
			}
			raw, _ := json.Marshal(msg)
			if name == "unknown_field" {
				raw = append(raw[:len(raw)-1], []byte(`,"token":"private"}`)...)
			}
			if name == "oversized" {
				raw = []byte(strings.Repeat("x", 1025))
			}
			d := amqp.Delivery{Body: raw, MessageId: msg.EventID, Type: "cohort.ready.v1", ContentType: "application/json"}
			if name == "message_id" {
				d.MessageId = strings.Repeat("c", 32)
			}
			decoded, known := decodeCohortDelivery(d)
			if name == "valid" {
				if !known || decoded != valid {
					t.Fatalf("valid message lost: %+v", decoded)
				}
			} else if !known || decoded.EventID != d.MessageId || decoded.JobID != "" || decoded.Generation != 0 {
				t.Fatal("invalid payload forwarded")
			}
		})
	}
}

type cohortAck struct{ ack, nack int }

func (a *cohortAck) Ack(uint64, bool) error        { a.ack++; return nil }
func (a *cohortAck) Nack(uint64, bool, bool) error { a.nack++; return nil }
func (a *cohortAck) Reject(uint64, bool) error     { return nil }

type cohortDeliveryFake struct {
	err   error
	calls int
}

func (h *cohortDeliveryFake) HandleDelivery(context.Context, domain.CohortJobMessage) error {
	h.calls++
	return h.err
}
func TestCohortManualAckRequiresPersistedDisposition(t *testing.T) {
	for _, fail := range []bool{false, true} {
		ack := &cohortAck{}
		h := &cohortDeliveryFake{}
		if fail {
			h.err = errors.New("DB uncertainty")
		}
		raw, _ := json.Marshal(domain.CohortJobMessage{Version: 1, Kind: "cohort.ready.v1", EventID: strings.Repeat("a", 32), JobID: strings.Repeat("b", 32), Generation: 1})
		d := amqp.Delivery{Acknowledger: ack, Body: raw, MessageId: strings.Repeat("a", 32), Type: "cohort.ready.v1", ContentType: "application/json"}
		err := handleCohortDelivery(context.Background(), d, h)
		if fail {
			if err == nil || ack.ack != 0 || ack.nack != 0 {
				t.Fatal("unknown DB failure acknowledged")
			}
		} else if err != nil || ack.ack != 1 {
			t.Fatal("persisted disposition not acknowledged", err)
		}
	}
}
