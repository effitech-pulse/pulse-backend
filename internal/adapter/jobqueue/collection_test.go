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

func TestCollectionQueueConfirmAndReconcile(t *testing.T) {
	msg := domain.CollectionMessage{Version: 1, Kind: "collection.ready.v1", EventID: strings.Repeat("a", 32), JobID: strings.Repeat("b", 32), Generation: 1}
	body, _ := json.Marshal(msg)
	for _, raw := range [][]byte{body, []byte(`{"event_id":"` + msg.EventID + `","raw":"canary"}`), []byte(strings.Replace(string(body), `"version":1`, `"version":1,"version":1`, 1))} {
		d := amqp.Delivery{Body: raw, MessageId: msg.EventID, ContentType: "application/json", Type: msg.Kind}
		_, valid := decodeCollectionDelivery(d)
		if valid != (string(raw) == string(body)) {
			t.Fatalf("delivery validity=%v for %s", valid, raw)
		}
	}
	confirms := make(chan amqp.Confirmation, 1)
	returns := make(chan amqp.Return, 1)
	confirms <- amqp.Confirmation{DeliveryTag: 1, Ack: true}
	returns <- amqp.Return{MessageId: msg.EventID}
	if got := collectConfirmation(context.Background(), msg.EventID, 1, confirms, returns, make(chan *amqp.Error)); got != "returned" {
		t.Fatal(got)
	}
}

type collectionAck struct {
	order   *[]string
	requeue bool
}

func (a *collectionAck) Ack(uint64, bool) error { *a.order = append(*a.order, "ack"); return nil }
func (a *collectionAck) Nack(_ uint64, _ bool, requeue bool) error {
	a.requeue = requeue
	*a.order = append(*a.order, "nack")
	return nil
}
func (a *collectionAck) Reject(uint64, bool) error { return nil }

type collectionHandler func(context.Context, domain.CollectionMessage) error

func (f collectionHandler) HandleDelivery(ctx context.Context, m domain.CollectionMessage) error {
	return f(ctx, m)
}
func TestCollectionDeliveryAcknowledgesOnlyDurableDisposition(t *testing.T) {
	msg := domain.CollectionMessage{Version: 1, Kind: "collection.ready.v1", EventID: strings.Repeat("a", 32), JobID: strings.Repeat("b", 32), Generation: 1}
	body, _ := json.Marshal(msg)
	for _, scenario := range []string{"durable", "uncertain", "malformed"} {
		order := []string{}
		ack := &collectionAck{order: &order}
		delivery := amqp.Delivery{Body: body, MessageId: msg.EventID, ContentType: "application/json", Type: msg.Kind, Acknowledger: ack}
		if scenario == "malformed" {
			delivery.Body = []byte(`{"raw":"secret"}`)
		}
		handler := collectionHandler(func(context.Context, domain.CollectionMessage) error {
			if scenario == "uncertain" {
				return errors.New("commit_unknown")
			}
			order = append(order, "persisted")
			return nil
		})
		e := handleCollectionDelivery(context.Background(), delivery, handler)
		switch scenario {
		case "durable":
			if e != nil || strings.Join(order, ",") != "persisted,ack" {
				t.Fatal(order, e)
			}
		case "uncertain":
			if e == nil || len(order) != 0 {
				t.Fatal("uncertain delivery acknowledged", order, e)
			}
		case "malformed":
			if e != nil || strings.Join(order, ",") != "nack" || ack.requeue {
				t.Fatal("poison requeued or reached app", order, e)
			}
		}
	}
}
