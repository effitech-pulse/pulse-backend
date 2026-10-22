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

const cohortExchange = "pulse.cohort.v1"
const cohortQueue = "pulse.cohort.v1.ready"
const cohortKind = "cohort.ready.v1"

var errCohortQueue = errors.New("cohort_job_queue_unavailable")

func cohortTopology(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(cohortExchange, "direct", true, false, false, false, nil); err != nil {
		return errCohortQueue
	}
	if err := ch.ExchangeDeclare(cohortExchange+".dlx", "direct", true, false, false, false, nil); err != nil {
		return errCohortQueue
	}
	dlq := amqp.Table{"x-queue-type": "quorum", "x-quorum-initial-group-size": int32(1), "x-max-length": int32(16), "x-max-length-bytes": int32(16384), "x-overflow": "reject-publish", "x-message-ttl": int32(3600000)}
	if _, err := ch.QueueDeclare(cohortExchange+".quarantine", true, false, false, false, dlq); err != nil {
		return errCohortQueue
	}
	if err := ch.QueueBind(cohortExchange+".quarantine", "quarantine", cohortExchange+".dlx", false, nil); err != nil {
		return errCohortQueue
	}
	args := amqp.Table{"x-queue-type": "quorum", "x-quorum-initial-group-size": int32(1), "x-max-length": int32(16), "x-max-length-bytes": int32(16384), "x-overflow": "reject-publish", "x-message-ttl": int32(1800000), "x-delivery-limit": int32(5), "x-dead-letter-exchange": cohortExchange + ".dlx", "x-dead-letter-routing-key": "quarantine"}
	if _, err := ch.QueueDeclare(cohortQueue, true, false, false, false, args); err != nil {
		return errCohortQueue
	}
	if err := ch.QueueBind(cohortQueue, "ready", cohortExchange, false, nil); err != nil {
		return errCohortQueue
	}
	return nil
}

func connectCohort(ctx context.Context, uri string) (*amqp.Connection, *amqp.Channel, error) {
	conn, err := amqp.DialConfig(uri, amqp.Config{Heartbeat: 10 * time.Second, ChannelMax: 16, Recovery: nil, Dial: func(network, addr string) (net.Conn, error) {
		c, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
		if e != nil {
			return nil, e
		}
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		return deadlineConn{c}, nil
	}})
	if err != nil {
		return nil, nil, errCohortQueue
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, nil, errCohortQueue
	}
	if err = cohortTopology(ch); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, nil, errCohortQueue
	}
	return conn, ch, nil
}

// Publisher использует один inflight и явно пересоздаёт channel после uncertainty.
type CohortPublisher struct {
	mu       sync.Mutex
	uri      string
	conn     *amqp.Connection
	ch       *amqp.Channel
	confirms <-chan amqp.Confirmation
	returns  <-chan amqp.Return
	closed   <-chan *amqp.Error
}

func NewCohortPublisher(uri string) *CohortPublisher { return &CohortPublisher{uri: uri} }
func (p *CohortPublisher) reset() {
	if p.ch != nil {
		_ = p.ch.Close()
	}
	if p.conn != nil {
		_ = p.conn.Close()
	}
	p.ch = nil
	p.conn = nil
}
func (p *CohortPublisher) Close() { p.mu.Lock(); defer p.mu.Unlock(); p.reset() }
func (p *CohortPublisher) Publish(ctx context.Context, msg domain.CohortJobMessage) (domain.CohortPublishOutcome, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	body, err := json.Marshal(msg)
	if msg.Version != 1 || msg.Kind != cohortKind || !opaque(msg.EventID) || !opaque(msg.JobID) || msg.Generation < 1 || msg.Generation > 8 {
		return "unknown", errCohortQueue
	}
	if err != nil || len(body) > 1024 {
		return "unknown", errCohortQueue
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if p.ch == nil {
		p.conn, p.ch, err = connectCohort(ctx, p.uri)
		if err != nil {
			return "unknown", err
		}
		p.confirms = p.ch.NotifyPublish(make(chan amqp.Confirmation, 2))
		p.returns = p.ch.NotifyReturn(make(chan amqp.Return, 2))
		p.closed = p.ch.NotifyClose(make(chan *amqp.Error, 1))
		if err = p.ch.Confirm(false); err != nil {
			p.reset()
			return "unknown", errCohortQueue
		}
	}
	seq := p.ch.GetNextPublishSeqNo()
	if err = p.ch.PublishWithContext(ctx, cohortExchange, "ready", true, false, amqp.Publishing{ContentType: "application/json", Type: cohortKind, DeliveryMode: 2, MessageId: msg.EventID, Body: body}); err != nil {
		p.reset()
		return "unknown", errCohortQueue
	}
	out := domain.CohortPublishOutcome(collectConfirmation(ctx, msg.EventID, seq, p.confirms, p.returns, p.closed))
	if out == "unknown" {
		p.reset()
	}
	return out, nil
}

// DeliveryHandler возвращает nil лишь после сохранённого disposition в PG.
type CohortDeliveryHandler interface {
	HandleDelivery(context.Context, domain.CohortJobMessage) error
}

func decodeCohortDelivery(d amqp.Delivery) (domain.CohortJobMessage, bool) {
	var msg domain.CohortJobMessage
	if len(d.Body) > 1024 {
		return domain.CohortJobMessage{EventID: d.MessageId}, opaque(d.MessageId)
	}
	dec := json.NewDecoder(bytes.NewReader(d.Body))
	dec.DisallowUnknownFields()
	err := dec.Decode(&msg)
	if err != nil {
		return domain.CohortJobMessage{EventID: d.MessageId}, opaque(d.MessageId)
	}
	var extra any
	valid := dec.Decode(&extra) == io.EOF && d.ContentType == "application/json" && d.Type == cohortKind && d.MessageId == msg.EventID && msg.Version == 1 && msg.Kind == cohortKind && opaque(msg.EventID) && opaque(msg.JobID) && msg.Generation >= 1 && msg.Generation <= 8
	if !valid {
		return domain.CohortJobMessage{EventID: d.MessageId}, opaque(d.MessageId)
	}
	return msg, true
}

// Consume reconnect bounded, с manual ack; DB error закрывает channel без requeue loop.
func ConsumeCohort(ctx context.Context, uri string, h CohortDeliveryHandler) error {
	for attempt := 0; attempt < 8; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := consumeCohortSession(ctx, uri, h)
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
	return errCohortQueue
}
func consumeCohortSession(ctx context.Context, uri string, h CohortDeliveryHandler) error {
	conn, ch, err := connectCohort(ctx, uri)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer ch.Close()
	if err = ch.Qos(1, 0, false); err != nil {
		return errCohortQueue
	}
	deliveries, err := ch.Consume(cohortQueue, "", false, false, false, false, nil)
	if err != nil {
		return errCohortQueue
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return errCohortQueue
			}
			if err = handleCohortDelivery(ctx, d, h); err != nil {
				return err
			}

		}
	}
}

func handleCohortDelivery(ctx context.Context, d amqp.Delivery, h CohortDeliveryHandler) error {
	msg, known := decodeCohortDelivery(d)
	if !known {
		if err := d.Nack(false, false); err != nil {
			return errCohortQueue
		}
		return nil
	}
	if err := h.HandleDelivery(ctx, msg); err != nil {
		return errCohortQueue
	}
	if err := d.Ack(false); err != nil {
		return errCohortQueue
	}
	return nil
}
