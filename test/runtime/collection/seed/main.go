// Command seed явно мигрирует и заполняет только новый isolated synthetic schema.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/migrations"
)

func run() error {
	policyPath := flag.String("manifest", "", "own manifest file")
	urlPath := flag.String("database-url-file", "", "private own URL file")
	tokenPath := flag.String("session-file", "", "new private session token output")
	allow := flag.Bool("allow-isolated-mutation", false, "explicit isolated mutation opt-in")
	flag.Parse()
	if !*allow || *policyPath == "" || *urlPath == "" || *tokenPath == "" {
		return fmt.Errorf("explicit own files and mutation opt-in required")
	}
	raw, err := os.ReadFile(*policyPath)
	if err != nil || len(raw) > 65536 {
		return fmt.Errorf("manifest unavailable or too large")
	}
	var policy domain.CollectionPolicy
	if json.Unmarshal(raw, &policy) != nil || policy.ManifestSHA256 != "" {
		return fmt.Errorf("invalid raw manifest")
	}
	digest := sha256.Sum256(raw)
	policy.ManifestSHA256 = hex.EncodeToString(digest[:])
	if !regexp.MustCompile(`^q2c2_[a-z0-9_]{1,48}$`).MatchString(policy.TestSchema) {
		return fmt.Errorf("dedicated schema required")
	}
	if err = domain.ValidateCollectionPolicy(policy); err != nil {
		return fmt.Errorf("policy invalid")
	}
	if len(policy.Bindings) != 2 || len(policy.AllowedAccountIDs) != 2 {
		return fmt.Errorf("proof seed requires exactly two bindings/accounts")
	}
	urlInfo, err := os.Stat(*urlPath)
	if err != nil || urlInfo.Mode().Perm() != 0600 {
		return fmt.Errorf("private URL must be0600")
	}
	urlBytes, err := os.ReadFile(*urlPath)
	if err != nil {
		return fmt.Errorf("URL unavailable")
	}
	config, err := pgxpool.ParseConfig(strings.TrimSpace(string(urlBytes)))
	if err != nil {
		return fmt.Errorf("URL invalid")
	}
	if config.ConnConfig.Host != "127.0.0.1" || config.ConnConfig.Port != 55442 || config.ConnConfig.Database != "collection_test" {
		return fmt.Errorf("own loopback test database required")
	}
	config.MaxConns = 3
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("own PG unavailable")
	}
	defer admin.Close()
	schema := pgx.Identifier{policy.TestSchema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		return fmt.Errorf("new schema creation failed")
	}
	config = config.Copy()
	config.ConnConfig.RuntimeParams["search_path"] = policy.TestSchema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = migrations.Apply(ctx, pool); err != nil {
		return fmt.Errorf("explicit migration failed")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO department(id,name) OVERRIDING SYSTEM VALUE VALUES(-900,'Synthetic')`); err != nil {
		return err
	}
	scope := `{"funds":["presentation"],"ngdus":[-900],"departments":[-900]}`
	for _, account := range policy.AllowedAccountIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO auth_user(id,login,name,password_hash,role,scope) OVERRIDING SYSTEM VALUE VALUES($1,$2,'Synthetic','unused','admin',$3::jsonb)`, account, fmt.Sprintf("c2-synthetic-%d", account), scope); err != nil {
			return err
		}
	}
	for _, binding := range policy.Bindings {
		if binding.IdentityKind != "unitek" || !strings.HasPrefix(binding.ExternalID, "-") {
			return fmt.Errorf("synthetic unitek binding required")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO well(id,unitek_id,ngdu,department,active,fund_type) OVERRIDING SYSTEM VALUE VALUES($1,$2::bigint,-900,-900,true,'presentation')`, binding.WellID, binding.ExternalID); err != nil {
			return err
		}
		source := domain.TelemetrySource{ID: binding.SourceID, Name: "Synthetic", Kind: "json_pull", Status: "running", IntervalSeconds: 86400, Endpoint: policy.FixtureOrigin + "/v1/collection-capture", WellIdentity: "unitek", WellField: "well_id", TimeField: "timestamp", ProfileVersion: 1, Mappings: policy.Fixtures[0].Mappings}
		sourceRaw, _ := json.Marshal(source)
		if _, err = tx.Exec(ctx, `INSERT INTO telemetry_source(id,config,next_poll)VALUES($1,$2::jsonb,clock_timestamp()+interval '1 day')`, source.ID, sourceRaw); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO telemetry_source_profile(source_id,version,config)VALUES($1,1,$2::jsonb)`, source.ID, sourceRaw); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO collection_job_guard(id,run_id,manifest_sha256,test_schema,fixture_origin,synthetic_only,accepting,expires_at)VALUES(true,$1,$2,$3,$4,true,true,clock_timestamp()+interval '23 hours')`, policy.RunID, policy.ManifestSHA256, policy.TestSchema, policy.FixtureOrigin); err != nil {
		return err
	}
	tokenBytes := make([]byte, 32)
	if _, err = rand.Read(tokenBytes); err != nil {
		return err
	}
	token := hex.EncodeToString(tokenBytes)
	tokenSHA := sha256.Sum256([]byte(token))
	if _, err = tx.Exec(ctx, `INSERT INTO auth_session(token_hash,user_id,expires_at)VALUES($1,$2,clock_timestamp()+interval '8 hours')`, hex.EncodeToString(tokenSHA[:]), policy.AllowedAccountIDs[0]); err != nil {
		return err
	}
	file, err := os.OpenFile(*tokenPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("session output exists or unavailable")
	}
	if _, err = file.WriteString(token + "\n"); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("seed commit unknown; inspect own schema before retry")
	}
	fmt.Printf("{\"status\":\"SEEDED\",\"schema_version\":%d,\"manifest_sha256\":%q}\n", migrations.CurrentVersion, policy.ManifestSHA256)
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "isolated seed failed; inspect own schema privately")
		os.Exit(1)
	}
}
