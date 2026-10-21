// Package jobqueue переносит только ссылки заданий; PostgreSQL остаётся authority.
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
)

const exchange = "pulse.file-check.v1"
const queue = "pulse.file-check.v1.ready"
const kind = "file_check.ready.v1"

var errQueue = errors.New("file_job_queue_unavailable")

// collectConfirmation сериализует два subscriptions, включая buffered return перед ack.
func collectConfirmation(ctx context.Context, id string, seq uint64, confirms <-chan amqp.Confirmation, returns <-chan amqp.Return, closed <-chan *amqp.Error) domain.FileJobPublishOutcome {
	returned := false
	for {
		select {
		case <-ctx.Done():
			return "unknown"
		case <-closed:
			return "unknown"
		case r, ok := <-returns:
			if !ok || r.MessageId != id {
				return "unknown"
			}
			returned = true
		case c, ok := <-confirms:
			if !ok || c.DeliveryTag != seq {
				return "unknown"
			}
			// The pinned client dispatches basic.return synchronously before its confirm.
			// Notification channel select order does not preserve that wire order.
			for {
				select {
				case r, ok := <-returns:
					if !ok || r.MessageId != id {
						return "unknown"
					}
					returned = true
				default:
					select {
					case <-closed:
						return "unknown"
					default:
					}
					if returned {
						return "returned"
					}
					if !c.Ack {
						return "nack"
					}
					return "confirmed"
				}
			}
		}
	}
}

func topology(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(exchange, "direct", true, false, false, false, nil); err != nil {
		return errQueue
	}
	if err := ch.ExchangeDeclare(exchange+".dlx", "direct", true, false, false, false, nil); err != nil {
		return errQueue
	}
	dlq := amqp.Table{"x-queue-type": "quorum", "x-quorum-initial-group-size": int32(1), "x-max-length": int32(64), "x-max-length-bytes": int32(65536), "x-overflow": "reject-publish", "x-message-ttl": int32(3600000)}
	if _, err := ch.QueueDeclare(exchange+".quarantine", true, false, false, false, dlq); err != nil {
		return errQueue
	}
	if err := ch.QueueBind(exchange+".quarantine", "quarantine", exchange+".dlx", false, nil); err != nil {
		return errQueue
	}
	args := amqp.Table{"x-queue-type": "quorum", "x-quorum-initial-group-size": int32(1), "x-max-length": int32(64), "x-max-length-bytes": int32(65536), "x-overflow": "reject-publish", "x-message-ttl": int32(1200000), "x-delivery-limit": int32(5), "x-dead-letter-exchange": exchange + ".dlx", "x-dead-letter-routing-key": "quarantine"}
	if _, err := ch.QueueDeclare(queue, true, false, false, false, args); err != nil {
		return errQueue
	}
	if err := ch.QueueBind(queue, "ready", exchange, false, nil); err != nil {
		return errQueue
	}
	return nil
}

type deadlineConn struct{ net.Conn }

func (c deadlineConn) Write(p []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}
func connect(ctx context.Context, uri string) (*amqp.Connection, *amqp.Channel, error) {
	conn, err := amqp.DialConfig(uri, amqp.Config{Heartbeat: 10 * time.Second, ChannelMax: 16, Recovery: nil, Dial: func(network, addr string) (net.Conn, error) {
		c, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
		if e != nil {
			return nil, e
		}
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		return deadlineConn{c}, nil
	}})
	if err != nil {
		return nil, nil, errQueue
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, nil, errQueue
	}
	if err = topology(ch); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, errQueue
	}
	return conn, ch, nil
}

// Publisher использует один inflight и явно пересоздаёт channel после uncertainty.
type Publisher struct {
	mu       sync.Mutex
	uri      string
	conn     *amqp.Connection
	ch       *amqp.Channel
	confirms <-chan amqp.Confirmation
	returns  <-chan amqp.Return
	closed   <-chan *amqp.Error
}

func NewPublisher(uri string) *Publisher { return &Publisher{uri: uri} }
func (p *Publisher) reset() {
	if p.ch != nil {
		_ = p.ch.Close()
	}
	if p.conn != nil {
		_ = p.conn.Close()
	}
	p.ch = nil
	p.conn = nil
}
func (p *Publisher) Close() { p.mu.Lock(); defer p.mu.Unlock(); p.reset() }
func (p *Publisher) Publish(ctx context.Context, msg domain.FileJobMessage) (domain.FileJobPublishOutcome, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	body, err := json.Marshal(msg)
	if err != nil || len(body) > 1024 {
		return "unknown", errQueue
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if p.ch == nil {
		p.conn, p.ch, err = connect(ctx, p.uri)
		if err != nil {
			return "unknown", err
		}
		p.confirms = p.ch.NotifyPublish(make(chan amqp.Confirmation, 2))
		p.returns = p.ch.NotifyReturn(make(chan amqp.Return, 2))
		p.closed = p.ch.NotifyClose(make(chan *amqp.Error, 1))
		if err = p.ch.Confirm(false); err != nil {
			p.reset()
			return "unknown", errQueue
		}
	}
	seq := p.ch.GetNextPublishSeqNo()
	if err = p.ch.PublishWithContext(ctx, exchange, "ready", true, false, amqp.Publishing{ContentType: "application/json", Type: kind, DeliveryMode: 2, MessageId: msg.EventID, Body: body}); err != nil {
		p.reset()
		return "unknown", errQueue
	}
	out := collectConfirmation(ctx, msg.EventID, seq, p.confirms, p.returns, p.closed)
	if out == "unknown" {
		p.reset()
	}
	return out, nil
}

// DeliveryHandler возвращает nil лишь после сохранённого disposition в PG.
type DeliveryHandler interface {
	HandleDelivery(context.Context, domain.FileJobMessage) error
}

func opaque(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func decodeDelivery(d amqp.Delivery) (domain.FileJobMessage, bool) {
	var msg domain.FileJobMessage
	if len(d.Body) > 1024 {
		return msg, false
	}
	dec := json.NewDecoder(bytes.NewReader(d.Body))
	dec.DisallowUnknownFields()
	err := dec.Decode(&msg)
	if err != nil {
		return domain.FileJobMessage{EventID: d.MessageId}, opaque(d.MessageId)
	}
	var extra any
	valid := dec.Decode(&extra) == io.EOF && d.ContentType == "application/json" && d.Type == kind && d.MessageId == msg.EventID && msg.Version == 1 && opaque(msg.EventID) && opaque(msg.JobID) && msg.Generation >= 1 && msg.Generation <= 6
	if !valid {
		return domain.FileJobMessage{EventID: d.MessageId}, opaque(d.MessageId)
	}
	return msg, true
}

// Consume reconnect bounded, с manual ack; DB error закрывает channel без requeue loop.
func Consume(ctx context.Context, uri string, h DeliveryHandler) error {
	for attempt := 0; attempt < 8; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := consumeSession(ctx, uri, h)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			return nil
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
	return errQueue
}
func consumeSession(ctx context.Context, uri string, h DeliveryHandler) error {
	conn, ch, err := connect(ctx, uri)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer ch.Close()
	if err = ch.Qos(1, 0, false); err != nil {
		return errQueue
	}
	deliveries, err := ch.Consume(queue, "", false, false, false, false, nil)
	if err != nil {
		return errQueue
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return errQueue
			}
			msg, known := decodeDelivery(d)
			if known {
				if err = h.HandleDelivery(ctx, msg); err != nil {
					return errQueue
				}
			}
			if err = d.Ack(false); err != nil {
				return errQueue
			}
		}
	}
}
