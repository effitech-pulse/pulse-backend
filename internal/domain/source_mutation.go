package domain

import (
	"math"
	"net/url"
	"strings"
)

type SourceMutationKind string

const (
	SourceCreate SourceMutationKind = "create"
	SourceUpdate SourceMutationKind = "update"
	SourceStatus SourceMutationKind = "status"
)

type SourceMutation struct {
	Kind          SourceMutationKind
	Source        TelemetrySource
	ReplaceSecret bool
}

func ValidateTelemetrySource(in TelemetrySource) error {
	switch in.WellIdentity {
	case "", "pulse", "unitek", "armits":
	default:
		return NewError(Invalid, "Неизвестный тип идентификатора скважины")
	}

	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 || in.IntervalSeconds < 5 || in.IntervalSeconds > 86400 || in.WellField == "" || in.TimeField == "" {
		return NewError(Invalid, "Нужны название, поля времени и скважины, интервал от 5 секунд до суток")
	}
	switch in.Kind {
	case "json_pull", "json_push", "clickhouse", "postgres", "mysql":
	default:
		return NewError(Invalid, "Неизвестный тип источника")
	}
	if in.Kind != "json_push" && in.Endpoint == "" {
		return NewError(Invalid, "Укажите адрес источника")
	}
	if in.Kind != "json_push" {
		u, err := url.Parse(in.Endpoint)
		if err != nil || u.Hostname() == "" {
			return NewError(Invalid, "Некорректный адрес источника")
		}
		if u.User != nil {
			if _, has := u.User.Password(); has {
				return NewError(Invalid, "Не включайте пароль в адрес. Используйте поле секрета")
			}
		}
		for _, k := range []string{"password", "token", "api_key", "secret"} {
			if u.Query().Has(k) {
				return NewError(Invalid, "Секреты нельзя включать в адрес источника")
			}
		}
	}
	if (in.Kind == "postgres" || in.Kind == "mysql" || in.Kind == "clickhouse") && in.TableName == "" {
		return NewError(Invalid, "Укажите таблицу или представление")
	}
	seen := map[string]bool{}
	if len(in.Mappings) == 0 || len(in.Mappings) > 100 {
		return NewError(Invalid, "Укажите от 1 до 100 параметров")
	}
	for _, m := range in.Mappings {
		if m.Field == "" || m.Parameter == "" || seen[m.Parameter] || math.IsNaN(m.Scale) || math.IsInf(m.Scale, 0) || math.IsNaN(m.Offset) || math.IsInf(m.Offset, 0) {
			return NewError(Invalid, "Некорректный или повторный параметр словаря")
		}
		seen[m.Parameter] = true
	}
	return nil
}
