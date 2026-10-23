package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const amqpFrameMax = 131072

// relayAMQP сохраняет broker→client confirms и stream-ит body ограниченным буфером.
func (s *fixture) relayAMQP(input io.Reader, output io.Writer, client bool) error {
	buffer := make([]byte, 32752)
	for {
		var header [7]byte
		if _, err := io.ReadFull(input, header[:]); err != nil {
			return err
		}
		size := binary.BigEndian.Uint32(header[3:])
		if size > amqpFrameMax-8 {
			return io.ErrShortBuffer
		}
		prefix := make([]byte, min(int(size), 4))
		if _, err := io.ReadFull(input, prefix); err != nil {
			return err
		}
		if header[0] == 1 && len(prefix) == 4 && (bytes.Equal(prefix, []byte{0, 10, 0, 30}) || bytes.Equal(prefix, []byte{0, 10, 0, 31})) {
			if size != 12 {
				return io.ErrUnexpectedEOF
			}
			extra := make([]byte, 8)
			if _, err := io.ReadFull(input, extra); err != nil {
				return err
			}
			negotiated := binary.BigEndian.Uint32(extra[2:6])
			if negotiated == 0 || negotiated > amqpFrameMax {
				return io.ErrShortBuffer
			}
			prefix = append(prefix, extra...)
		}
		ack := client && header[0] == 1 && len(prefix) == 4 && bytes.Equal(prefix, []byte{0, 60, 0, 80})
		s.mu.Lock()
		hold := s.control.HoldACK
		s.mu.Unlock()
		destination := output
		if ack && hold {
			destination = io.Discard
			s.mu.Lock()
			s.heldACKs++
			s.mu.Unlock()
		}
		if _, err := destination.Write(header[:]); err != nil {
			return err
		}
		if _, err := destination.Write(prefix); err != nil {
			return err
		}
		if _, err := io.CopyBuffer(destination, io.LimitReader(input, int64(size)-int64(len(prefix))), buffer); err != nil {
			return err
		}
		var end [1]byte
		if _, err := io.ReadFull(input, end[:]); err != nil {
			return err
		}
		if end[0] != 206 {
			return io.ErrUnexpectedEOF
		}
		if _, err := destination.Write(end[:]); err != nil {
			return err
		}
	}
}
func (s *fixture) serveAMQP(ctx context.Context, listener net.Listener) {
	go func() { <-ctx.Done(); listener.Close() }()
	for {
		client, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer client.Close()
			upstream, err := net.DialTimeout("tcp", "rabbit:5672", 2*time.Second)
			if err != nil {
				return
			}
			defer upstream.Close()
			// Own AMQP0-9-1 plaintext header; no arbitrary protocol/destination.
			var protocol [8]byte
			_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
			if _, err = io.ReadFull(client, protocol[:]); err != nil || string(protocol[:]) != "AMQP\x00\x00\x09\x01" {
				return
			}
			_ = client.SetReadDeadline(time.Time{})
			if _, err = upstream.Write(protocol[:]); err != nil {
				return
			}
			done := make(chan struct{}, 2)
			go func() { _ = s.relayAMQP(client, upstream, true); done <- struct{}{} }()
			go func() { _ = s.relayAMQP(upstream, client, false); done <- struct{}{} }()
			select {
			case <-ctx.Done():
			case <-done:
			}
		}()
	}
}

// apiGate держит bounded upstream response до явного release; API уже мог отправить его proxy.
func (s *fixture) apiGate(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/gate")
	if !strings.HasPrefix(path, "/api/v1/collection-jobs") || r.URL.RawQuery != "" {
		w.WriteHeader(404)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if err != nil {
		w.WriteHeader(413)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, r.Method, "http://api:8000"+path, bytes.NewReader(body))
	if err != nil {
		w.WriteHeader(400)
		return
	}
	for _, name := range []string{"Cookie", "Content-Type", "Origin", "Accept"} {
		request.Header.Set(name, r.Header.Get(name))
	}
	transport := &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext}
	client := http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect forbidden") }}
	response, err := client.Do(request)
	if err != nil {
		w.WriteHeader(502)
		return
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		w.WriteHeader(502)
		return
	}
	s.mu.Lock()
	hold := s.control.HoldAPIResponse
	if hold {
		s.heldAPIResponses++
	}
	s.mu.Unlock()
	for hold {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Millisecond):
		}
		s.mu.Lock()
		hold = s.control.HoldAPIResponse
		s.mu.Unlock()
	}
	s.mu.Lock()
	drop := s.control.DropAPIResponse
	s.mu.Unlock()
	if drop {
		if hijack, ok := w.(http.Hijacker); ok {
			connection, _, err := hijack.Hijack()
			if err == nil {
				connection.Close()
			}
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(data)
}
