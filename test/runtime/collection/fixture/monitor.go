package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// monitorRow не содержит connection strings, SQL text, payload или credentials.
type monitorRow struct {
	At       int64  `json:"at"`
	Role     string `json:"role"`
	Current  int64  `json:"current"`
	Peak     int64  `json:"peak"`
	Database int64  `json:"database,omitempty"`
	PGData   int64  `json:"pgdata,omitempty"`
	WAL      int64  `json:"wal,omitempty"`
	WALLSN   int64  `json:"wal_lsn,omitempty"`
	Temp     int64  `json:"temp,omitempty"`
	Disk     int64  `json:"disk,omitempty"`
	Error    bool   `json:"error,omitempty"`
}

func readCounter(path string) (int64, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
}
func directoryBytes(root string) (int64, error) {
	var bytes int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		bytes += info.Size()
		return nil
	})
	return bytes, err
}
func monitor(role string) int {
	var connection *pgx.Conn
	if role == "postgres" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var err error
		connection, err = pgx.Connect(ctx, "host=/var/run/postgresql user=collection_test dbname=collection_test sslmode=disable application_name=q2c2_budget_monitor")
		cancel()
		if err != nil {
			return 2
		}
		defer connection.Close(context.Background())
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	encoder := json.NewEncoder(os.Stdout)
	for {
		row := monitorRow{At: time.Now().UnixNano(), Role: role}
		var err error
		row.Current, err = readCounter("/sys/fs/cgroup/memory.current")
		if err != nil {
			return 2
		}
		row.Peak, err = readCounter("/sys/fs/cgroup/memory.peak")
		if err != nil {
			return 2
		}
		if role == "postgres" {
			ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
			err = connection.QueryRow(ctx, "SELECT pg_database_size(current_database()), pg_wal_lsn_diff(pg_current_wal_lsn(),'0/0')::bigint, coalesce((SELECT sum(temp_bytes) FROM pg_stat_database),0)::bigint").Scan(&row.Database, &row.WALLSN, &row.Temp)
			cancel()
			if err != nil {
				row.Error = true
			}
			row.PGData, err = directoryBytes("/var/lib/postgresql/data")
			if err != nil {
				row.Error = true
			}
			row.WAL, err = directoryBytes("/var/lib/postgresql/data/pg_wal")
			if err != nil {
				row.Error = true
			}
		}
		if role == "rabbit" {
			row.Disk, err = directoryBytes("/var/lib/rabbitmq")
			if err != nil {
				row.Error = true
			}
		}
		if encoder.Encode(row) != nil {
			return 2
		}
		if row.Error {
			return 2
		}
		<-ticker.C
	}
}
func runMonitorArgs() bool {
	if len(os.Args) != 3 || os.Args[1] != "monitor" {
		return false
	}
	role := os.Args[2]
	switch role {
	case "postgres", "rabbit", "api", "worker1", "worker2", "fixture":
	default:
		fmt.Fprintln(os.Stderr, "unknown monitor role")
		os.Exit(2)
	}
	os.Exit(monitor(role))
	return true
}
