package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type tableField struct {
	kind string
	ops  string
}

var tableFields = map[string]map[string]tableField{
	"equipmenthistory":          {"recorded_at": {"time", "range"}, "action": {"text", "in"}, "slot": {"text", "in"}, "source": {"text", "in"}, "actor_name": {"text", "contains"}, "reason": {"text", "contains"}, "changes": {"text", "contains"}},
	"access_ngdu_options":       {"name": {"text", "contains"}},
	"access_department_options": {"name": {"text", "contains"}},
	"access_role_options":       {"name": {"text", "contains"}},
	"access_group_role_options": {"name": {"text", "contains"}},
	"access_group_options":      {"name": {"text", "contains"}},
	"events":                    {"event_type": {"text", "in"}, "well_id": {"decimal_text", "contains"}, "well_number": {"text", "contains"}, "archive": {"text", "in"}, "occurred_at": {"time", "range"}, "actor_name": {"text", "contains"}, "actor_kind": {"text", "in"}, "verdict": {"text", "in"}, "review_status": {"text", "in"}, "conclusion": {"text", "in"}},
	"people":                    {"name": {"text", "contains"}, "job_title": {"text", "contains"}, "workplace_organization": {"text", "contains"}, "workplace_division": {"text", "contains"}, "workplace_department": {"text", "contains"}, "manager_name": {"text", "contains"}, "active": {"bool", "in"}},
	"audit":                     {"ts": {"naive", "range"}, "actor": {"text", "contains in"}, "method": {"text", "in"}, "path": {"text", "contains"}, "status_code": {"number", "range"}},
	"ingestlogs":                {"timestamp": {"time", "range"}, "source_id": {"text", "in"}, "accepted": {"number", "range"}, "duplicates": {"number", "range"}, "rejected": {"number", "range"}, "message": {"text", "contains"}},
	"manualchecks":              {"created_at": {"time", "range"}, "period": {"time", "overlap"}, "verdict": {"text", "in"}, "model_name": {"text", "contains"}},
	"filejobs":                  {"created_at": {"time", "range"}, "state": {"text", "in"}, "model_name": {"text", "contains"}},
	"wellhistory":               {"created_at": {"time", "range"}, "kind": {"text", "in"}, "actor_name": {"text", "contains"}, "changes": {"text", "contains"}, "reason": {"text", "contains"}},
	"access_users":              {"name": {"text", "contains"}, "login": {"text", "contains"}, "role_ids": {"decimal", "in"}, "group_ids": {"decimal", "in"}, "organization_kind": {"text", "in"}, "ngdu_id": {"decimal", "in"}, "department_id": {"decimal", "in"}, "active": {"bool", "in"}},
	"access_groups":             {"name": {"text", "contains"}, "description": {"text", "contains"}, "role_ids": {"decimal", "in"}},
	"access_roles":              {"name": {"text", "contains"}, "description": {"text", "contains"}, "permission_keys": {"text", "in"}, "permission_count": {"number", "range"}},
	"access_preview":            {"user_name": {"text", "contains"}, "before_organization": {"text", "contains"}, "after_organization": {"text", "contains"}, "before_active": {"bool", "in"}, "after_active": {"bool", "in"}, "added_permissions": {"text", "in"}, "removed_permissions": {"text", "in"}, "source_role_ids": {"decimal", "in"}, "source_group_ids": {"decimal", "in"}},
	"access_members":            {"name": {"text", "contains"}, "login": {"text", "contains"}, "role_ids": {"decimal", "in"}, "group_ids": {"decimal", "in"}, "organization_kind": {"text", "in"}, "ngdu_id": {"decimal", "in"}, "department_id": {"decimal", "in"}, "active": {"bool", "in"}},
}
var canonicalTableDecimal = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

var tableCursorKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return key
}()

func tableInvalid() error  { return domain.NewError(domain.Invalid, "invalid_table_query") }
func cursorInvalid() error { return domain.NewError(domain.Invalid, "invalid_table_cursor") }

func tableValue(raw json.RawMessage, kind string) (json.RawMessage, error) {
	if bytes.Equal(raw, []byte("null")) {
		return raw, nil
	}
	var v any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&v) != nil {
		return nil, tableInvalid()
	}
	switch kind {
	case "bool":
		if _, ok := v.(bool); !ok {
			return nil, tableInvalid()
		}
	case "decimal":
		var text string
		switch n := v.(type) {
		case string:
			text = n
		case json.Number:
			text = string(n)
		default:
			return nil, tableInvalid()
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != text {
			return nil, tableInvalid()
		}
		v = text
	case "naive":
		text, ok := v.(string)
		if !ok || len(text) < 19 || len(text) > 26 || strings.ContainsAny(text, "Zz+, ") {
			return nil, tableInvalid()
		}
		stamp, err := time.Parse("2006-01-02T15:04:05.999999", text)
		if err != nil {
			return nil, tableInvalid()
		}
		v = stamp.Format("2006-01-02T15:04:05.999999")

	case "text":
		s, ok := v.(string)
		if !ok || utf8.RuneCountInString(s) > 120 {
			return nil, tableInvalid()
		}
	case "number":
		text := ""
		switch n := v.(type) {
		case json.Number:
			text = string(n)
		case string:
			if !canonicalTableDecimal.MatchString(n) {
				return nil, tableInvalid()
			}
			text = n
		default:
			return nil, tableInvalid()
		}
		if len(text) > 120 {
			return nil, tableInvalid()
		}
		if i := strings.IndexAny(text, "eE"); i >= 0 {
			exp, e := strconv.Atoi(text[i+1:])
			if e != nil || exp > 10000 || exp < -10000 {
				return nil, tableInvalid()
			}
		}
		if _, ok := new(big.Rat).SetString(text); !ok {
			return nil, tableInvalid()
		}
		v = json.Number(text)
	case "time":
		s, ok := v.(string)
		if !ok {
			return nil, tableInvalid()
		}
		if strings.Contains(s, ",") {
			return nil, tableInvalid()
		}
		if dot := strings.IndexByte(s, '.'); dot >= 0 {
			for i := dot + 1; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
				if i > dot+6 && s[i] != '0' {
					return nil, tableInvalid()
				}
			}
		}
		stamp, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return nil, tableInvalid()
		}
		v = stamp.UTC().Format(time.RFC3339Nano)
	}
	out, err := json.Marshal(v)
	return out, err
}
func parseTableRequest(ctx context.Context, dataset string, parent int64, raw string, size int, cursor string, bindings ...string) (domain.TableRequest, error) {
	var req domain.TableRequest
	fields, ok := tableFields[dataset]
	if !ok || len(raw) > 16384 || size < 1 || size > 100 {
		return req, tableInvalid()
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&req.Query) != nil {
		return req, tableInvalid()
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return req, tableInvalid()
	}
	q := &req.Query
	if q.Version != 1 || len(q.Filters) > 64 || len(q.Sort) > 64 {
		return req, tableInvalid()
	}
	if q.Filters == nil {
		q.Filters = []domain.TableFilter{}
	}
	if q.Sort == nil {
		q.Sort = []domain.TableSort{}
	}
	seen := map[string]bool{}
	for _, s := range q.Sort {
		if _, ok := fields[s.Field]; !ok || seen[s.Field] || s.Direction != "asc" && s.Direction != "desc" {
			return req, tableInvalid()
		}
		seen[s.Field] = true
	}
	for i := range q.Filters {
		f := &q.Filters[i]
		field, ok := fields[f.Field]
		if !ok || !strings.Contains(" "+field.ops+" ", " "+f.Op+" ") {
			return req, tableInvalid()
		}
		switch f.Op {
		case "contains":
			if field.kind == "decimal_text" && f.Value != nil {
				n, e := strconv.ParseInt(*f.Value, 10, 64)
				if e != nil || n < 1 || strconv.FormatInt(n, 10) != *f.Value {
					return req, tableInvalid()
				}
			}
			if f.Value == nil || utf8.RuneCountInString(*f.Value) > 120 || f.Values != nil || f.From != nil || f.To != nil {
				return req, tableInvalid()
			}
		case "in":
			if f.Value != nil || f.From != nil || f.To != nil || len(f.Values) > 100 {
				return req, tableInvalid()
			}
			for j, v := range f.Values {
				if bytes.Equal(v, []byte("null")) && !((dataset == "audit" && f.Field == "actor") || (dataset == "events" && (f.Field == "verdict" || f.Field == "review_status" || f.Field == "conclusion")) || ((dataset == "access_users" || dataset == "access_members") && (f.Field == "organization_kind" || f.Field == "ngdu_id" || f.Field == "department_id"))) {
					return req, tableInvalid()
				}
				norm, err := tableValue(v, field.kind)
				if err != nil {
					return req, err
				}
				if dataset == "equipmenthistory" {
					var value string
					if json.Unmarshal(norm, &value) != nil {
						return req, tableInvalid()
					}
					allowed := ""
					switch f.Field {
					case "action":
						allowed = "register update replace correct close void"
					case "slot":
						allowed = "pump motor control_station"
					case "source":
						allowed = "manual excel"
					}
					if allowed != "" && !slices.Contains(strings.Fields(allowed), value) {
						return req, tableInvalid()
					}
				}
				f.Values[j] = norm
			}
		case "range", "overlap":
			if f.Value != nil || f.Values != nil || len(f.From) == 0 && len(f.To) == 0 {
				return req, tableInvalid()
			}
			for _, ptr := range []*json.RawMessage{&f.From, &f.To} {
				if len(*ptr) > 0 {
					v, err := tableValue(*ptr, field.kind)
					if err != nil || bytes.Equal(v, []byte("null")) {
						return req, tableInvalid()
					}
					*ptr = v
				}
			}
			if len(f.From) > 0 && len(f.To) > 0 {
				if field.kind == "time" || field.kind == "naive" {
					var a, b string
					_ = json.Unmarshal(f.From, &a)
					_ = json.Unmarshal(f.To, &b)
					layout := time.RFC3339Nano
					if field.kind == "naive" {
						layout = "2006-01-02T15:04:05.999999"
					}
					at, _ := time.Parse(layout, a)
					bt, _ := time.Parse(layout, b)
					if at.After(bt) {
						return req, tableInvalid()
					}
				} else {
					a, okA := new(big.Rat).SetString(string(f.From))
					b, okB := new(big.Rat).SetString(string(f.To))
					if !okA || !okB || a.Cmp(b) > 0 {
						return req, tableInvalid()
					}
				}
			}
		}
	}
	// AND filters and OR values are sets; sort priority remains untouched.
	normalizedFilters := make([]domain.TableFilter, 0, len(q.Filters))
	for _, f := range q.Filters {
		if f.Op == "in" && len(f.Values) == 0 || f.Op == "contains" && f.Value != nil && *f.Value == "" {
			continue
		}
		if f.Op == "in" {
			sort.Slice(f.Values, func(i, j int) bool { return bytes.Compare(f.Values[i], f.Values[j]) < 0 })
			values := make([]json.RawMessage, 0, len(f.Values))
			for _, v := range f.Values {
				if len(values) == 0 || !bytes.Equal(values[len(values)-1], v) {
					values = append(values, v)
				}
			}
			f.Values = values
		}
		normalizedFilters = append(normalizedFilters, f)
	}
	sort.SliceStable(normalizedFilters, func(i, j int) bool {
		a, _ := json.Marshal(normalizedFilters[i])
		b, _ := json.Marshal(normalizedFilters[j])
		return bytes.Compare(a, b) < 0
	})
	q.Filters = normalizedFilters
	principal, present := domain.CurrentPrincipal(ctx)
	binding := struct {
		Dataset   string
		Parent    int64
		Query     domain.TableQuery
		PageSize  int
		Principal domain.Principal
		Bindings  []string
		Present   bool
	}{dataset, parent, req.Query, size, principal, bindings, present}
	normalized, _ := json.Marshal(binding)
	sum := sha256.Sum256(normalized)
	req.Fingerprint = hex.EncodeToString(sum[:])
	req.PageSize = size
	if cursor != "" {
		if len(cursor) > 16384 {
			return req, cursorInvalid()
		}
		parts := strings.Split(cursor, ".")
		if len(parts) != 2 {
			return req, cursorInvalid()
		}
		payload, e := base64.RawURLEncoding.DecodeString(parts[0])
		sig, e2 := base64.RawURLEncoding.DecodeString(parts[1])
		mac := hmac.New(sha256.New, tableCursorKey)
		_, _ = mac.Write(payload)
		if e != nil || e2 != nil || !hmac.Equal(sig, mac.Sum(nil)) {
			return req, cursorInvalid()
		}
		var v struct {
			Fingerprint string            `json:"q"`
			Keys        []json.RawMessage `json:"k"`
		}
		if json.Unmarshal(payload, &v) != nil || v.Fingerprint != req.Fingerprint || len(v.Keys) == 0 {
			return req, cursorInvalid()
		}
		req.After = v.Keys
	}
	return req, nil
}
func encodeTableCursor(req domain.TableRequest, keys json.RawMessage) (string, error) {
	var values []json.RawMessage
	if json.Unmarshal(keys, &values) != nil {
		return "", cursorInvalid()
	}
	payload, err := json.Marshal(struct {
		Fingerprint string            `json:"q"`
		Keys        []json.RawMessage `json:"k"`
	}{req.Fingerprint, values})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, tableCursorKey)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func tablePage[T any](req domain.TableRequest, rows domain.TableRows[T], err error) (domain.TablePage[T], error) {
	page := domain.TablePage[T]{Items: rows.Items}
	if err != nil {
		return page, err
	}
	if page.Items == nil {
		page.Items = []T{}
	}
	if rows.HasMore {
		raw, _ := json.Marshal(rows.Keys)
		token, e := encodeTableCursor(req, raw)
		if e != nil {
			return page, e
		}
		page.NextCursor = &token
	}
	return page, nil
}
