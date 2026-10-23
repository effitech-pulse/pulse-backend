package jobqueue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

const collectionExchange = "pulse.collection.v1"
const collectionQueue = "pulse.collection.v1.ready"
const collectionKind = "collection.ready.v1"

var errCollectionQueue = errors.New("collection_job_queue_unavailable")

func collectionTopology(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(collectionExchange, "direct", true, false, false, false, nil); err != nil {
		return errCollectionQueue
	}
	if err := ch.ExchangeDeclare(collectionExchange+".dlx", "direct", true, false, false, false, nil); err != nil {
		return errCollectionQueue
	}
	dlq := amqp.Table{"x-queue-type": "quorum", "x-quorum-initial-group-size": int32(1), "x-max-length": int32(8), "x-max-length-bytes": int32(8192), "x-overflow": "reject-publish", "x-message-ttl": int32(3600000)}
	if _, err := ch.QueueDeclare(collectionExchange+".quarantine", true, false, false, false, dlq); err != nil {
		return errCollectionQueue
	}
	if err := ch.QueueBind(collectionExchange+".quarantine", "quarantine", collectionExchange+".dlx", false, nil); err != nil {
		return errCollectionQueue
	}
	args := amqp.Table{"x-queue-type": "quorum", "x-quorum-initial-group-size": int32(1), "x-max-length": int32(8), "x-max-length-bytes": int32(8192), "x-overflow": "reject-publish", "x-message-ttl": int32(300000), "x-delivery-limit": int32(5), "x-dead-letter-exchange": collectionExchange + ".dlx", "x-dead-letter-routing-key": "quarantine"}
	if _, err := ch.QueueDeclare(collectionQueue, true, false, false, false, args); err != nil {
		return errCollectionQueue
	}
	if err := ch.QueueBind(collectionQueue, "ready", collectionExchange, false, nil); err != nil {
		return errCollectionQueue
	}
	return nil
}

func connectCollection(ctx context.Context, uri string) (*amqp.Connection, *amqp.Channel, error) {
	conn, err := amqp.DialConfig(uri, amqp.Config{Heartbeat: 10 * time.Second, ChannelMax: 16, Recovery: nil, Dial: func(network, addr string) (net.Conn, error) {
		c, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
		if e != nil {
			return nil, e
		}
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		return deadlineConn{c}, nil
	}})
	if err != nil {
		return nil, nil, errCollectionQueue
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, nil, errCollectionQueue
	}
	if err = collectionTopology(ch); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, errCollectionQueue
	}
	return conn, ch, nil
}

// Publisher использует один inflight и явно пересоздаёт channel после uncertainty.
type CollectionPublisher struct {
	mu       sync.Mutex
	uri      string
	conn     *amqp.Connection
	ch       *amqp.Channel
	confirms <-chan amqp.Confirmation
	returns  <-chan amqp.Return
	closed   <-chan *amqp.Error
}

func NewCollectionPublisher(uri string) *CollectionPublisher { return &CollectionPublisher{uri: uri} }
func (p *CollectionPublisher) reset() {
	if p.ch != nil {
		_ = p.ch.Close()
	}
	if p.conn != nil {
		_ = p.conn.Close()
	}
	p.ch = nil
	p.conn = nil
}
func (p *CollectionPublisher) Close() { p.mu.Lock(); defer p.mu.Unlock(); p.reset() }
func (p *CollectionPublisher) Publish(ctx context.Context, msg domain.CollectionMessage) (domain.CollectionPublishOutcome, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	body, err := json.Marshal(msg)
	if msg.Version != 1 || msg.Kind != collectionKind || !opaque(msg.EventID) || !opaque(msg.JobID) || msg.Generation < 1 || msg.Generation > 8 {
		return "unknown", errCollectionQueue
	}
	if err != nil || len(body) > 1024 {
		return "unknown", errCollectionQueue
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if p.ch == nil {
		p.conn, p.ch, err = connectCollection(ctx, p.uri)
		if err != nil {
			return "unknown", err
		}
		p.confirms = p.ch.NotifyPublish(make(chan amqp.Confirmation, 2))
		p.returns = p.ch.NotifyReturn(make(chan amqp.Return, 2))
		p.closed = p.ch.NotifyClose(make(chan *amqp.Error, 1))
		if err = p.ch.Confirm(false); err != nil {
			p.reset()
			return "unknown", errCollectionQueue
		}
	}
	seq := p.ch.GetNextPublishSeqNo()
	if err = p.ch.PublishWithContext(ctx, collectionExchange, "ready", true, false, amqp.Publishing{ContentType: "application/json", Type: collectionKind, DeliveryMode: 2, MessageId: msg.EventID, Body: body}); err != nil {
		p.reset()
		return "unknown", errCollectionQueue
	}
	out := domain.CollectionPublishOutcome(collectConfirmation(ctx, msg.EventID, seq, p.confirms, p.returns, p.closed))
	if out == "unknown" {
		p.reset()
	}
	return out, nil
}

func decodeCollectionDelivery(d amqp.Delivery) (domain.CollectionMessage, bool) {
	var msg domain.CollectionMessage
	if len(d.Body) > 1024 {
		return domain.CollectionMessage{}, false
	}
	if !collectionEventKeys(d.Body) {
		return domain.CollectionMessage{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(d.Body))
	dec.DisallowUnknownFields()
	err := dec.Decode(&msg)
	if err != nil {
		return domain.CollectionMessage{}, false
	}
	var extra any
	valid := dec.Decode(&extra) == io.EOF && d.ContentType == "application/json" && d.Type == collectionKind && d.MessageId == msg.EventID && msg.Version == 1 && msg.Kind == collectionKind && opaque(msg.EventID) && opaque(msg.JobID) && msg.Generation >= 1 && msg.Generation <= 8
	if !valid {
		return domain.CollectionMessage{}, false
	}
	return msg, true
}

// Consume reconnect bounded, с manual ack; DB error закрывает channel без requeue loop.
func ConsumeCollection(ctx context.Context, uri string, h service.CollectionDeliveryHandler) error {
	for attempt := 0; attempt < 8; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := consumeCollectionSession(ctx, uri, h)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			return nil
		}
		if attempt == 7 {
			break
		}
		delay := time.Second << min(attempt, 4)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return errCollectionQueue
}
func consumeCollectionSession(ctx context.Context, uri string, h service.CollectionDeliveryHandler) error {
	conn, ch, err := connectCollection(ctx, uri)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer ch.Close()
	if err = ch.Qos(1, 0, false); err != nil {
		return errCollectionQueue
	}
	deliveries, err := ch.Consume(collectionQueue, "", false, false, false, false, nil)
	if err != nil {
		return errCollectionQueue
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return errCollectionQueue
			}
			if err = handleCollectionDelivery(ctx, d, h); err != nil {
				return err
			}

		}
	}
}

func handleCollectionDelivery(ctx context.Context, d amqp.Delivery, h service.CollectionDeliveryHandler) error {
	msg, known := decodeCollectionDelivery(d)
	if !known {
		if err := d.Nack(false, false); err != nil {
			return errCollectionQueue
		}
		return nil
	}
	if err := h.HandleDelivery(ctx, msg); err != nil {
		return errCollectionQueue
	}
	if err := d.Ack(false); err != nil {
		return errCollectionQueue
	}
	return nil
}

func collectionEventKeys(body []byte) bool {
	d := json.NewDecoder(bytes.NewReader(body))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		t, e = d.Token()
		if e != nil {
			return false
		}
		key, ok := t.(string)
		if !ok || seen[key] {
			return false
		}
		seen[key] = true
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return false
		}
	}
	_, e = d.Token()
	return e == nil
}
