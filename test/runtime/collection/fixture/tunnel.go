package main

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

const tunnelFrameMax = 32768
const tunnelConnectionsMax = 16
const tunnelOpen byte = 1
const tunnelData byte = 2
const tunnelClose byte = 3

type tunnel struct {
	mu          sync.Mutex
	output      sync.Mutex
	connections map[uint32]net.Conn
	writer      io.Writer
}

func (t *tunnel) send(id uint32, kind byte, body []byte) error {
	if len(body) > tunnelFrameMax {
		return io.ErrShortBuffer
	}
	t.output.Lock()
	defer t.output.Unlock()
	var header [9]byte
	binary.BigEndian.PutUint32(header[:4], id)
	header[4] = kind
	binary.BigEndian.PutUint32(header[5:], uint32(len(body)))
	if _, err := t.writer.Write(header[:]); err != nil {
		return err
	}
	_, err := t.writer.Write(body)
	return err
}
func (t *tunnel) close(id uint32) {
	t.mu.Lock()
	connection := t.connections[id]
	delete(t.connections, id)
	t.mu.Unlock()
	if connection != nil {
		connection.Close()
	}
}
func (t *tunnel) open(id uint32, body []byte) error {
	var address string
	switch string(body) {
	case "pg":
		address = "127.0.0.1:5432"
	case "rabbit":
		address = "rabbit:5672"
	case "api":
		address = "api:8000"
	case "fixture":
		address = "fixture:8080"
	case "management":
		address = "rabbit:15672"
	default:
		return io.ErrUnexpectedEOF
	}
	t.mu.Lock()
	if id == 0 || len(t.connections) >= tunnelConnectionsMax || t.connections[id] != nil {
		t.mu.Unlock()
		return io.ErrUnexpectedEOF
	}
	connection, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.mu.Unlock()
		return t.send(id, tunnelClose, nil)
	}
	t.connections[id] = connection
	t.mu.Unlock()
	go func() {
		defer t.close(id)
		buffer := make([]byte, tunnelFrameMax)
		for {
			count, err := connection.Read(buffer)
			if count > 0 {
				if t.send(id, tunnelData, buffer[:count]) != nil {
					return
				}
			}
			if err != nil {
				_ = t.send(id, tunnelClose, nil)
				return
			}
		}
	}()
	return nil
}
func runTunnel(input io.Reader, output io.Writer) error {
	t := &tunnel{connections: make(map[uint32]net.Conn), writer: output}
	defer func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		for _, connection := range t.connections {
			connection.Close()
		}
	}()
	for {
		var header [9]byte
		if _, err := io.ReadFull(input, header[:]); err != nil {
			return err
		}
		id := binary.BigEndian.Uint32(header[:4])
		kind := header[4]
		size := binary.BigEndian.Uint32(header[5:])
		if size > tunnelFrameMax {
			return io.ErrShortBuffer
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(input, body); err != nil {
			return err
		}
		switch kind {
		case tunnelOpen:
			if err := t.open(id, body); err != nil {
				return err
			}
		case tunnelData:
			t.mu.Lock()
			connection := t.connections[id]
			t.mu.Unlock()
			if connection != nil {
				if _, err := connection.Write(body); err != nil {
					t.close(id)
					_ = t.send(id, tunnelClose, nil)
				}
			}
		case tunnelClose:
			t.close(id)
		default:
			return io.ErrUnexpectedEOF
		}
	}
}
func runTunnelArgs() bool {
	if len(os.Args) != 2 || os.Args[1] != "tunnel" {
		return false
	}
	if runTunnel(os.Stdin, os.Stdout) != nil {
		os.Exit(2)
	}
	return true
}
