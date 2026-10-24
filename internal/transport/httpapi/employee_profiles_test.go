package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestEmployeeProfileStrictDTO(t *testing.T) {
	base := `{"id":"7","login":"valid","name":"Имя","active":true,"revision":"1","role_ids":[],"group_ids":[],"profile":{"revision":"1","job_title":null,"workplace":{"organization":null,"division":null,"department":null},"manager_id":null}}`
	var v userChangeDTO
	if e := json.Unmarshal([]byte(base), &v); e != nil {
		t.Fatal(e)
	}
	if e := service.ValidateAccessChange(changeDTO{User: &v}.value()); e != nil {
		t.Fatal(e)
	}
	for _, body := range []string{strings.Replace(base, `"revision":"1","job_title"`, `"revision":1,"job_title"`, 1), strings.Replace(base, `"manager_id":null`, `"manager_id":"01"`, 1), strings.Replace(base, `"department":null`, `"department":null,"permissions":[]`, 1), strings.Replace(base, `"job_title":null`, `"job_title":null,"active":true`, 1)} {
		if e := json.Unmarshal([]byte(body), &v); e == nil {
			t.Fatal("unknown/unsafe DTO accepted", body)
		}
	}
	for _, body := range []string{strings.Replace(base, `"job_title":null,`, "", 1), strings.Replace(base, `"department":null`, `"department_missing":null`, 1)} {
		var v userChangeDTO
		if e := json.Unmarshal([]byte(body), &v); e != nil {
			continue
		}
		if e := service.ValidateAccessChange(changeDTO{User: &v}.value()); e == nil {
			t.Fatal("missing field accepted")
		}
	}
	p := domain.ManagedProfile{Revision: 9007199254740993}
	raw, e := json.Marshal(p)
	if e != nil || !strings.Contains(string(raw), `"revision":"9007199254740993"`) || !strings.Contains(string(raw), `"manager_id":null`) {
		t.Fatal(string(raw), e)
	}
}
