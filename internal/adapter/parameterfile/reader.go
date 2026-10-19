// Package parameterfile читает внешний справочник названий параметров.
package parameterfile

import (
	"context"
	"encoding/json"
	"os"
	"sort"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type Reader struct{ Path string }

func (r Reader) Load(ctx context.Context) ([]domain.Parameter, error) {
	raw, err := os.ReadFile(r.Path)
	if os.IsNotExist(err) {
		return []domain.Parameter{}, nil
	}
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]domain.Parameter, 0, len(keys))
	for _, key := range keys {
		names := []string{}
		if err = json.Unmarshal(fields[key], &names); err != nil {
			var name string
			if err = json.Unmarshal(fields[key], &name); err != nil {
				return nil, err
			}
			names = []string{name}
		}
		name := key
		if len(names) > 0 {
			name = names[0]
		}
		out = append(out, domain.Parameter{Field: key, Name: name, Aliases: names})
	}
	return out, nil
}
