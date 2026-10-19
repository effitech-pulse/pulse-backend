package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// ExternalTelemetry читает только заданные таблицы, без произвольного SQL.
type ExternalTelemetry struct{}

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func quotedIdentifier(raw, quote string) (string, error) {
	parts := strings.Split(raw, ".")
	for i, p := range parts {
		if !identifier.MatchString(p) {
			return "", fmt.Errorf("invalid table or column name")
		}
		parts[i] = quote + p + quote
	}
	return strings.Join(parts, "."), nil
}
func externalQuery(s domain.TelemetrySource, since time.Time, quote string) (sq.SelectBuilder, error) {
	table, err := quotedIdentifier(s.TableName, quote)
	if err != nil {
		return sq.SelectBuilder{}, err
	}
	field, err := quotedIdentifier(s.TimeField, quote)
	if err != nil {
		return sq.SelectBuilder{}, err
	}
	return sq.Select("*").From(table).Where(sq.GtOrEq{field: since}).OrderBy(field).Limit(10001), nil
}
func (ExternalTelemetry) ReadExternal(ctx context.Context, s domain.TelemetrySource, secret string) ([]map[string]any, error) {
	u, err := url.Parse(s.Endpoint)
	if err != nil {
		return nil, err
	}
	since := time.Now().Add(-24 * time.Hour)
	if s.LastMeasured != nil {
		since = s.LastMeasured.Add(-time.Minute)
	}
	if s.Kind == "postgres" {
		if u.Scheme != "postgres" && u.Scheme != "postgresql" {
			return nil, fmt.Errorf("postgres URL required")
		}
		user := ""
		if u.User != nil {
			user = u.User.Username()
		}
		u.User = url.UserPassword(user, secret)
		conn, err := pgx.Connect(ctx, u.String())
		if err != nil {
			return nil, fmt.Errorf("database unavailable")
		}
		defer conn.Close(ctx)
		tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx)
		query, err := externalQuery(s, since, `"`)
		if err != nil {
			return nil, err
		}
		q, args, err := build(query)
		if err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("cannot read configured table")
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			values, e := rows.Values()
			if e != nil {
				return nil, e
			}
			record := map[string]any{}
			for i, f := range rows.FieldDescriptions() {
				v := values[i]
				if valuer, ok := v.(driver.Valuer); ok {
					var e error
					v, e = valuer.Value()
					if e != nil {
						return nil, e
					}
				}
				record[f.Name] = v
			}
			out = append(out, record)
			if len(out) > 10000 {
				return nil, fmt.Errorf("source window exceeds 10000 records; narrow source view")
			}
		}
		return out, rows.Err()
	}
	if s.Kind == "mysql" {
		if u.Scheme != "mysql" {
			return nil, fmt.Errorf("mysql URL required")
		}
		cfg := mysql.NewConfig()
		cfg.Net = "tcp"
		cfg.Addr = u.Host
		if u.Port() == "" {
			cfg.Addr = u.Hostname() + ":3306"
		}
		if u.User != nil {
			cfg.User = u.User.Username()
		}
		cfg.Passwd = secret
		cfg.DBName = strings.TrimPrefix(u.Path, "/")
		cfg.ParseTime = true
		cfg.Timeout = 10 * time.Second
		cfg.ReadTimeout = 20 * time.Second
		cfg.TLSConfig = "true"
		if u.Query().Get("tls") == "false" {
			cfg.TLSConfig = "false"
		}
		db, err := sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			return nil, err
		}
		defer db.Close()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, fmt.Errorf("database unavailable")
		}
		defer tx.Rollback()
		query, err := externalQuery(s, since, "`")
		if err != nil {
			return nil, err
		}
		q, args, err := query.ToSql()
		if err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("cannot read configured table")
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for rows.Next() {
			values := make([]any, len(cols))
			refs := make([]any, len(cols))
			for i := range refs {
				refs[i] = &values[i]
			}
			if err = rows.Scan(refs...); err != nil {
				return nil, err
			}
			record := map[string]any{}
			for i, v := range values {
				if b, ok := v.([]byte); ok {
					v = string(b)
				}
				record[cols[i]] = v
			}
			out = append(out, record)
			if len(out) > 10000 {
				return nil, fmt.Errorf("source window exceeds 10000 records; narrow source view")
			}
		}
		return out, rows.Err()
	}
	if s.Kind == "clickhouse" {
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("ClickHouse HTTP address required")
		}
		query, err := externalQuery(s, since, "`")
		if err != nil {
			return nil, err
		}
		q, _, err := query.ToSql()
		if err != nil {
			return nil, err
		}
		q = strings.Replace(q, "?", "{since:DateTime64(3, 'UTC')}", 1) + " FORMAT JSONEachRow"
		params := u.Query()
		params.Set("param_since", since.UTC().Format("2006-01-02 15:04:05.000"))
		params.Set("readonly", "1")
		params.Set("date_time_output_format", "iso")
		u.RawQuery = params.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(q))
		if err != nil {
			return nil, err
		}
		if u.User != nil {
			req.SetBasicAuth(u.User.Username(), secret)
			req.URL.User = nil
		}
		client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("database unavailable")
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("cannot read configured table")
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
		if err != nil {
			return nil, err
		}
		if len(raw) > 8*1024*1024 {
			return nil, fmt.Errorf("source response exceeds 8 MiB")
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.UseNumber()
		out := []map[string]any{}
		for {
			var record map[string]any
			err = dec.Decode(&record)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			out = append(out, record)
			if len(out) > 10000 {
				return nil, fmt.Errorf("source window exceeds 10000 records; narrow source view")
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported source")
}
