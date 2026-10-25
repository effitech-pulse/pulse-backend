package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func passportEnrollmentFlags(enroll bool, login, name string, activate bool, args int) error {
	if enroll && (login != "" || name != "" || activate || args != 0) {
		return errors.New("enrollment mode cannot be combined with bootstrap/activation/positional flags")
	}
	return nil
}
func decodePassportEnrollment(r io.Reader) (domain.PassportEnrollmentInput, error) {
	var in domain.PassportEnrollmentInput
	raw, e := io.ReadAll(io.LimitReader(r, 16385))
	if e != nil {
		return in, e
	}
	if len(raw) > 16384 {
		return in, errors.New("enrollment stdin exceeds 16 KiB")
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return in, errors.New("enrollment requires one JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&in); e != nil {
		return in, e
	}
	var extra json.RawMessage
	if e = dec.Decode(&extra); e != io.EOF {
		return in, errors.New("enrollment requires one JSON object")
	}
	return in, nil
}
func runPassportEnrollment() error {
	info, e := os.Stdin.Stat()
	if e != nil {
		return e
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return errors.New("enrollment requires protected noninteractive stdin")
	}
	in, e := decodePassportEnrollment(os.Stdin)
	if e != nil {
		return e
	}
	cfg, e := config.Load()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, e := postgres.Open(ctx, cfg)
	if e != nil {
		return e
	}
	defer pool.Close()
	receipt, e := postgres.New(pool).EnrollEquipmentPassportPermissions(ctx, in)
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(receipt)
}
