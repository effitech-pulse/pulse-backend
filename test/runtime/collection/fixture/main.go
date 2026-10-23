// Command fixture обслуживает только собственный synthetic стенд и считает forbidden RPC.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type control struct {
	Revision        string `json:"revision"`
	Duplicate       bool   `json:"duplicate"`
	PadBytes        int    `json:"pad_bytes"`
	ExtraRows       int    `json:"extra_rows"`
	DelayMS         int    `json:"delay_ms"`
	Status          int    `json:"status"`
	Failures        int    `json:"failures"`
	Removed         bool   `json:"removed"`
	Empty           bool   `json:"empty"`
	Hole            bool   `json:"hole"`
	Reset           bool   `json:"reset"`
	HoldACK         bool   `json:"hold_ack"`
	HoldAPIResponse bool   `json:"hold_api_response"`
	DropAPIResponse bool   `json:"drop_api_response"`
}
type fixture struct {
	mu                         sync.Mutex
	control                    control
	gets, rpcCalls             int
	heldACKs, heldAPIResponses int
	methods                    map[string]int
}

func newFixture() *fixture {
	return &fixture{control: control{Revision: "generation-a"}, methods: map[string]int{}}
}

// grpcServer считает именно вызванный method, включая unknown service; connect не RPC.
func (s *fixture) grpcServer() *grpc.Server {
	return grpc.NewServer(grpc.MaxRecvMsgSize(1024), grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		method, _ := grpc.MethodFromServerStream(stream)
		s.mu.Lock()
		s.rpcCalls++
		s.methods[method]++
		s.mu.Unlock()
		return status.Error(codes.PermissionDenied, "ML forbidden in collection proof")
	}))
}
func (s *fixture) capture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	canary := os.Getenv("ML_SERVICE_API_KEY")
	if canary != "" {
		w.Header().Set("X-Synthetic-Diagnostic", canary)
	}
	s.mu.Lock()
	s.gets++
	c := s.control
	if c.Failures > 0 {
		s.control.Failures--
		c.Status = 503
	}
	s.mu.Unlock()
	if c.DelayMS > 0 {
		select {
		case <-time.After(time.Duration(c.DelayMS) * time.Millisecond):
		case <-r.Context().Done():
			return
		}
	}
	if c.Reset {
		if h, ok := w.(http.Hijacker); ok {
			conn, _, err := h.Hijack()
			if err == nil {
				conn.Close()
			}
		}
		return
	}
	if c.Removed {
		w.WriteHeader(404)
		return
	}
	if c.Status != 0 && c.Status != 200 {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(c.Status)
		if canary != "" {
			_, _ = w.Write([]byte(canary))
		}
		return
	}
	q := r.URL.Query()
	start, err := time.Parse(time.RFC3339, q.Get("start"))
	if err != nil {
		w.WriteHeader(400)
		return
	}
	end, err := time.Parse(time.RFC3339, q.Get("end"))
	if err != nil {
		w.WriteHeader(400)
		return
	}
	minutes := int(end.Sub(start) / time.Minute)
	if minutes < 1 || minutes > 1440 || q.Get("protocol") != "collection-fixture-v1" || !strings.HasPrefix(q.Get("well_id"), "-") {
		w.WriteHeader(400)
		return
	}
	type record struct {
		WellID    string         `json:"well_id"`
		Timestamp string         `json:"timestamp"`
		Values    map[string]any `json:"values"`
	}
	records := make([]record, 0, 2881)
	if !c.Empty {
		for i := 0; i < minutes; i++ {
			if c.Hole && i == minutes/2 {
				continue
			}
			values := map[string]any{}
			for field := 0; field < 16; field++ {
				values["f"+strconv.Itoa(field)] = float64(0)
			}
			for field := 0; field < 4; field++ {
				values["s"+strconv.Itoa(field)] = float64(0)
			}
			values["f0"] = float64(50)
			values["f1"] = nil
			values["f2"] = float64(9999)
			row := record{q.Get("well_id"), start.Add(time.Duration(i) * time.Minute).UTC().Format(time.RFC3339), values}
			records = append(records, row)
			if c.Duplicate {
				records = append(records, row)
			}
		}
		for i := 0; i < c.ExtraRows; i++ {
			records = append(records, records[0])
		}
	}
	expected := minutes
	if c.Empty {
		expected = 0
	}
	envelope := struct {
		Protocol      string   `json:"protocol"`
		FixtureID     string   `json:"fixture_id"`
		WellID        string   `json:"well_id"`
		Start         string   `json:"start"`
		End           string   `json:"end"`
		DataRevision  string   `json:"data_revision"`
		Complete      bool     `json:"complete"`
		Empty         bool     `json:"empty"`
		ExpectedSlots int      `json:"expected_slots"`
		Records       []record `json:"records"`
	}{"collection-fixture-v1", q.Get("fixture_id"), q.Get("well_id"), q.Get("start"), q.Get("end"), c.Revision, true, c.Empty, expected, records}
	body, err := json.Marshal(envelope)
	if err != nil {
		w.WriteHeader(500)
		return
	}
	if c.PadBytes > len(body) {
		body = append(body, []byte(strings.Repeat(" ", c.PadBytes-len(body)))...)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}
func (s *fixture) serveControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var c control
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&c) != nil || len(c.Revision) > 64 || c.PadBytes < 0 || c.PadBytes > 2097153 || c.ExtraRows < 0 || c.ExtraRows > 1 || c.DelayMS < 0 || c.DelayMS > 45000 || c.Failures < 0 || c.Failures > 3 || c.Status < 0 || c.Status > 599 {
		w.WriteHeader(400)
		return
	}
	if c.Revision == "" {
		c.Revision = "generation-a"
	}
	s.mu.Lock()
	s.control = c
	s.mu.Unlock()
	w.WriteHeader(204)
}
func (s *fixture) counters(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		GETs             int            `json:"source_gets"`
		Calls            int            `json:"ml_rpc_calls"`
		Methods          map[string]int `json:"methods"`
		HeldACKs         int            `json:"held_acks"`
		HeldAPIResponses int            `json:"held_api_responses"`
	}{s.gets, s.rpcCalls, s.methods, s.heldACKs, s.heldAPIResponses})
}
func main() {
	if runTunnelArgs() {
		return
	}
	if runMonitorArgs() {
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "cgroup" {
		for _, path := range []string{"/sys/fs/cgroup/memory.current", "/sys/fs/cgroup/memory.peak"} {
			value, err := os.ReadFile(path)
			if err != nil {
				os.Exit(2)
			}
			fmt.Print(string(value))
		}
		return
	}
	s := newFixture()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/collection-capture", s.capture)
	mux.HandleFunc("/control", s.serveControl)
	mux.HandleFunc("/counters", s.counters)
	mux.HandleFunc("/gate/", s.apiGate)
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 50 * time.Second, MaxHeaderBytes: 8192}
	listener, err := net.Listen("tcp", ":50051")
	if err != nil {
		slog.Error("fixture listener unavailable")
		os.Exit(1)
	}
	grpcServer := s.grpcServer()
	go func() { _ = grpcServer.Serve(listener) }()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	amqpListener, err := net.Listen("tcp", ":5673")
	if err != nil {
		slog.Error("fixture AMQP listener unavailable")
		os.Exit(1)
	}
	go s.serveAMQP(ctx, amqpListener)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		grpcServer.Stop()
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("fixture HTTP unavailable")
		os.Exit(1)
	}
}
