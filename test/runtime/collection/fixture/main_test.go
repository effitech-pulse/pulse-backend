package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/telemetry"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestFixtureDenseMaxControlAndMutation(t *testing.T) {
	s := newFixture()
	s.control = control{Revision: "generation-a", Duplicate: true, PadBytes: 2097152}
	req := httptest.NewRequest("GET", "/v1/collection-capture?fixture_id=dense-v1&well_id=-900000&start=2026-10-19T00:00:00Z&end=2026-10-20T00:00:00Z&protocol=collection-fixture-v1", nil)
	out := httptest.NewRecorder()
	s.capture(out, req)
	if out.Code != 200 || out.Body.Len() != 2097152 {
		t.Fatalf("valid max: code=%d bytes=%d", out.Code, out.Body.Len())
	}
	var body struct {
		Records      []json.RawMessage `json:"records"`
		DataRevision string            `json:"data_revision"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Records) != 2880 || body.DataRevision != "generation-a" {
		t.Fatalf("max rows/revision: %d %s", len(body.Records), body.DataRevision)
	}
	s.control.Revision = "generation-b"
	s.control.PadBytes = 2097153
	out = httptest.NewRecorder()
	s.capture(out, req)
	if out.Body.Len() != 2097153 {
		t.Fatalf("cap+1 bytes=%d", out.Body.Len())
	}
	if s.gets != 2 {
		t.Fatalf("GET count=%d", s.gets)
	}
}

func TestForbiddenCounterCountsRPCNotTransport(t *testing.T) {
	s := newFixture()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := s.grpcServer()
	go server.Serve(listener)
	defer server.Stop()
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn.Connect()
	time.Sleep(25 * time.Millisecond)
	s.mu.Lock()
	count := s.rpcCalls
	s.mu.Unlock()
	if count != 0 {
		t.Fatalf("transport counted as RPC: %d", count)
	}
	_ = conn.Invoke(ctx, "/pulse.Forbidden/Score", &emptypb.Empty{}, &emptypb.Empty{})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rpcCalls != 1 || s.methods["/pulse.Forbidden/Score"] != 1 {
		t.Fatalf("RPC not counted: %d %+v", s.rpcCalls, s.methods)
	}
}

func TestTunnelRejectsOversizeUnknownTargetsAndUnknownFrames(t *testing.T) {
	for _, body := range [][]byte{
		{0, 0, 0, 1, 2, 0, 0, 128, 1},
		append([]byte{0, 0, 0, 1, 1, 0, 0, 0, 8}, []byte("external")...),
		{0, 0, 0, 1, 4, 0, 0, 0, 0},
	} {
		if err := runTunnel(bytes.NewReader(body), io.Discard); err == nil {
			t.Fatal("unsafe frame accepted")
		}
	}
}

func TestFixturePrivateErrorCanaryIsNotReturnedBySourceAdapter(t *testing.T) {
	canary := "synthetic_fixture_canary_1234567890"
	t.Setenv("ML_SERVICE_API_KEY", canary)
	s := newFixture()
	s.control.Status = 503
	server := httptest.NewServer(http.HandlerFunc(s.capture))
	defer server.Close()
	source, err := telemetry.NewCollectionSource(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.FetchCollection(context.Background(), domain.CollectionIntent{FixtureOrigin: server.URL, Fixture: domain.CollectionFixture{ID: "dense-v1"}, Binding: domain.CollectionBinding{ExternalID: "-900000"}, Start: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 19, 0, 1, 0, 0, time.UTC)})
	if err == nil || strings.Contains(err.Error(), canary) {
		t.Fatal("private upstream error exposed")
	}
}
