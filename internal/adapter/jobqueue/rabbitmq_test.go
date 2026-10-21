package jobqueue

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestFileJobPublishReturnBeforeAckObservation(t *testing.T) {
	returns := make(chan amqp.Return, 2)
	confirms := make(chan amqp.Confirmation, 2)
	closed := make(chan *amqp.Error, 1)
	returns <- amqp.Return{MessageId: "11111111111111111111111111111111"}
	confirms <- amqp.Confirmation{DeliveryTag: 7, Ack: true}
	// Both subscriptions are ready: even observing ack first must drain the return.
	got := collectConfirmation(context.Background(), "11111111111111111111111111111111", 7, confirms, returns, closed)
	if got != "returned" {
		t.Fatalf("unroutable publish reported %q", got)
	}
}
func TestFileJobPublishConfirmUnknown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if got := collectConfirmation(ctx, "id", 1, make(chan amqp.Confirmation), make(chan amqp.Return), make(chan *amqp.Error)); got != "unknown" {
		t.Fatal(got)
	}
}
func TestFileJobPublishNackOverflow(t *testing.T) {
	confirms := make(chan amqp.Confirmation, 1)
	confirms <- amqp.Confirmation{DeliveryTag: 1, Ack: false}
	if got := collectConfirmation(context.Background(), "id", 1, confirms, make(chan amqp.Return), make(chan *amqp.Error)); got != "nack" {
		t.Fatal(got)
	}
}
func TestFileJobPublishWrongSequenceUnknown(t *testing.T) {
	confirms := make(chan amqp.Confirmation, 1)
	confirms <- amqp.Confirmation{DeliveryTag: 2, Ack: true}
	if got := collectConfirmation(context.Background(), "id", 1, confirms, make(chan amqp.Return), make(chan *amqp.Error)); got != "unknown" {
		t.Fatal(got)
	}
}

func TestFileJobRabbitActualUnroutable(t *testing.T) {
	uri := os.Getenv("Q1_PRODUCER_AMQP_URL")
	if uri == "" {
		t.Skip("Q1_PRODUCER_AMQP_URL isolated broker required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p := NewPublisher(uri)
	defer p.Close()
	msg := domain.FileJobMessage{Version: 1, EventID: "cccccccccccccccccccccccccccccccc", JobID: "dddddddddddddddddddddddddddddddd", Generation: 1}
	out, e := p.Publish(ctx, msg)
	if e != nil || out != "confirmed" {
		t.Fatalf("initial publish %s %v", out, e)
	}
	if e = p.ch.QueueUnbind(queue, "ready", exchange, nil); e != nil {
		t.Fatal("unbind failed")
	}
	defer p.ch.QueueBind(queue, "ready", exchange, false, nil)
	out, e = p.Publish(ctx, msg)
	if e != nil || out != "returned" {
		t.Fatalf("unroutable outcome %s %v", out, e)
	}
	// Remove only this fixture's known message; no private body retained.
	d, ok, e := p.ch.Get(queue, true)
	if e != nil || !ok || d.MessageId != msg.EventID {
		t.Fatal("known fixture cleanup failed")
	}
}

type testDeliveryHandler func(context.Context, domain.FileJobMessage) error

func (f testDeliveryHandler) HandleDelivery(ctx context.Context, m domain.FileJobMessage) error {
	return f(ctx, m)
}
func TestFileJobRabbitUnknownPoisonDiscard(t *testing.T) {
	producer, consumer := os.Getenv("Q1_PRODUCER_AMQP_URL"), os.Getenv("Q1_WORKER_AMQP_URL")
	if producer == "" || consumer == "" {
		t.Skip("isolated broker required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, ch, e := connect(ctx, producer)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	defer ch.Close()
	sentinel := domain.FileJobMessage{Version: 1, EventID: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", JobID: "ffffffffffffffffffffffffffffffff", Generation: 1}
	raw, _ := json.Marshal(sentinel)
	for _, msg := range []amqp.Publishing{{ContentType: "application/json", Type: kind, MessageId: "invalid", Body: []byte(`{"raw":"unknown poison"}`)}, {ContentType: "application/json", Type: kind, MessageId: sentinel.EventID, Body: raw}} {
		if e = ch.PublishWithContext(ctx, exchange, "ready", true, false, msg); e != nil {
			t.Fatal("fixture publish failed")
		}
	}
	seen := make(chan domain.FileJobMessage, 2)
	done := make(chan error, 1)
	go func() {
		done <- Consume(ctx, consumer, testDeliveryHandler(func(_ context.Context, m domain.FileJobMessage) error { seen <- m; return nil }))
	}()
	select {
	case m := <-seen:
		if m != sentinel {
			t.Fatal("unknown poison reached application", m)
		}
	case <-ctx.Done():
		t.Fatal("sentinel missing; poison not acked")
	}
	cancel()
	<-done
	select {
	case m := <-seen:
		t.Fatal("extra application message", m)
	default:
	}
}
