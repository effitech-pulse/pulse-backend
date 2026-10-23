// Command legacy-probe вызывает текущие реальные fenced store ports старой попыткой.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func validateTarget(raw, schema string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "postgres" || parsed.Host != "127.0.0.1:55442" || parsed.Path != "/collection_test" || !regexp.MustCompile(`^q2c2_[a-z0-9_]{1,48}$`).MatchString(schema) {
		return fmt.Errorf("own isolated target required")
	}
	return nil
}
func run() error {
	path := flag.String("database-url-file", "", "private own database URL")
	schema := flag.String("schema", "", "own q2c2 schema")
	source := flag.String("source", "source-a", "own source-a/source-b")
	allow := flag.Bool("allow-isolated-mutation", false, "explicit isolated mutation")
	flag.Parse()
	if !*allow || (*source != "source-a" && *source != "source-b") {
		return fmt.Errorf("explicit synthetic scope required")
	}
	info, err := os.Stat(*path)
	if err != nil || info.Mode().Perm() != 0600 {
		return fmt.Errorf("private URL required")
	}
	raw, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(string(raw))
	if err = validateTarget(target, *schema); err != nil {
		return err
	}
	cfg, err := pgxpool.ParseConfig(target)
	if err != nil {
		return err
	}
	cfg.MaxConns = 3
	cfg.ConnConfig.RuntimeParams["search_path"] = *schema
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := postgres.New(pool)
	lease, err := store.ClaimSource(ctx, *source, false)
	if err != nil || lease == nil {
		return fmt.Errorf("legacy source claim failed")
	}
	encoder := json.NewEncoder(os.Stdout)
	if err = encoder.Encode(struct {
		Phase string    `json:"phase"`
		Fence int64     `json:"fence"`
		Until time.Time `json:"until"`
	}{"claimed", lease.Fence, lease.Until}); err != nil {
		return err
	}
	// Physical wall/DB-clock expiry, without any direct UPDATE lease/state shortcut.
	for {
		var expired bool
		if err = pool.QueryRow(ctx, "SELECT clock_timestamp()>$1::timestamptz", lease.Until).Scan(&expired); err != nil {
			return err
		}
		if expired {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	if err = encoder.Encode(struct {
		Phase string `json:"phase"`
	}{"expired"}); err != nil {
		return err
	}
	signal := make(chan bool, 1)
	go func() { scanner := bufio.NewScanner(os.Stdin); signal <- scanner.Scan() && scanner.Text() == "finish" }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case accepted := <-signal:
		if !accepted {
			return fmt.Errorf("explicit finish required")
		}
	}
	// The controller has now accepted a C2 job on the expired source, creating hold.
	var before, after string
	fingerprint := `SELECT md5(jsonb_build_array(writer_generation,writer_token_sha256,writer_profile_version,lease_until,last_received,last_measured,error,next_poll)::text) FROM telemetry_source WHERE id=$1`
	if err = pool.QueryRow(ctx, fingerprint, *source).Scan(&before); err != nil {
		return err
	}
	failure := store.FailCollection(ctx, *lease, "synthetic-expired-attempt")
	_, commit := store.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: *source, ProfileVersion: lease.ProfileVersion, Lease: lease, Release: true})
	_, ingest := store.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: *source, ProfileVersion: lease.ProfileVersion})
	blocked, claimError := store.ClaimSource(ctx, *source, false)
	if err = pool.QueryRow(ctx, fingerprint, *source).Scan(&after); err != nil {
		return err
	}
	if failure == nil || commit == nil || ingest == nil || claimError != nil || blocked != nil || before != after {
		return fmt.Errorf("expired legacy attempt crossed newer hold")
	}
	return encoder.Encode(struct {
		Phase             string `json:"phase"`
		OldFailRejected   bool   `json:"old_fail_rejected"`
		OldCommitRejected bool   `json:"old_commit_rejected"`
		HoldClaimRejected bool   `json:"hold_claim_rejected"`
		ControlsUnchanged bool   `json:"controls_unchanged"`
		IngestRejected    bool   `json:"ingest_write_rejected"`
	}{"verified", true, true, true, true, true})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "own legacy probe failed")
		os.Exit(1)
	}
}
