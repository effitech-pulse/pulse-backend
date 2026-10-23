package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func frame(body []byte) []byte {
	header := []byte{1, 0, 1, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(header[3:], uint32(len(body)))
	return append(append(header, body...), 206)
}
func TestAMQPProxyDropsOnlyClientBasicAck(t *testing.T) {
	s := newFixture()
	s.control.HoldACK = true
	ack := frame(append([]byte{0, 60, 0, 80}, make([]byte, 9)...))
	for _, client := range []bool{true, false} {
		var output bytes.Buffer
		if err := s.relayAMQP(bytes.NewReader(ack), &output, client); err != io.EOF {
			t.Fatal(err)
		}
		if client && output.Len() != 0 {
			t.Fatal("client ACK forwarded")
		}
		if !client && !bytes.Equal(output.Bytes(), ack) {
			t.Fatal("broker publisher confirm blocked")
		}
	}
	if s.heldACKs != 1 {
		t.Fatal("actual held ACK count", s.heldACKs)
	}
}
func TestAMQPProxyStreamsBoundedFrames(t *testing.T) {
	s := newFixture()
	body := bytes.Repeat([]byte{7}, 100000)
	body[0] = 0
	body[1] = 60
	body[2] = 0
	body[3] = 40
	input := frame(body)
	var output bytes.Buffer
	if err := s.relayAMQP(bytes.NewReader(input), &output, true); err != io.EOF {
		t.Fatal(err)
	}
	if !bytes.Equal(input, output.Bytes()) {
		t.Fatal("stream changed")
	}
	header := []byte{1, 0, 1, 0, 2, 0, 1}
	if err := s.relayAMQP(bytes.NewReader(header), io.Discard, true); err == nil {
		t.Fatal("frame_max+1 accepted")
	}
}

func TestAMQPProxyRejectsUnboundedFrameNegotiation(t *testing.T) {
	s := newFixture()
	body := make([]byte, 12)
	copy(body, []byte{0, 10, 0, 30})
	binary.BigEndian.PutUint32(body[6:], 262144)
	if err := s.relayAMQP(bytes.NewReader(frame(body)), io.Discard, false); err == io.EOF {
		t.Fatal("oversized frame negotiation accepted")
	}
}
