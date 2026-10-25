package service

import (
 "encoding/json"
 "os"
 "path/filepath"
 "testing"
 "time"

 "github.com/effitech-pulse/pulse-backend/internal/adapter/dailyflow"
 "github.com/effitech-pulse/pulse-backend/internal/domain"
)
func TestDailyFlowFixtures(t *testing.T){
 s,p,_:=dailySetup(t);real,err:=dailyflow.New(time.Date(2026,10,25,0,0,0,0,time.UTC));if err!=nil{t.Fatal(err)};s.provider=real
 t.Log("dataset",real.Profile().DatasetSHA256,"manifest",real.Profile().ManifestSHA256)
 names:=[]string{"steady-1001","decline-1002","zero-1003","gaps-1004","zero-expected-1005"}
 values:=map[string]any{"disabled":struct{Enabled bool `json:"enabled"`;Reason string `json:"reason"`}{false,"disabled"}}
 for i,name:=range names{r,e:=s.Read(internalFixtureContext(),int64(1001+i),domain.DailyFlowQuery{});if e!=nil{t.Fatal(e)};values[name]=r.View}
 s.provider=p;p.rows=[]domain.DailyFlowSourceRow{};r,e:=s.Read(internalFixtureContext(),1001,domain.DailyFlowQuery{});if e!=nil{t.Fatal(e)};values["all-null"]=r.View
 for _,x:=range []struct{status,name,code,detail string}{{"401","401","unauthorized","Сеанс недействителен"},{"403","403","forbidden","Недостаточно прав"},{"404","404","not_found","Скважина не найдена"},{"409","409","conflict","daily_flow_view_changed"},{"422","422","validation_error","daily_flow_invalid_query"},{"429","429","rate_limited","daily_flow_busy"},{"503","503","external_service_error","daily_flow_unavailable"}}{values["error-"+x.name]=struct{Code string `json:"code"`;Message string `json:"message"`;Detail any `json:"detail"`}{x.code,x.detail,x.detail}}
 for name,value:=range values{raw,e:=json.MarshalIndent(value,"","  ");if e!=nil{t.Fatal(e)};raw=append(raw,'\n');path:=filepath.Join("..","..","test","fixtures","daily-flow",name+".json");if os.Getenv("UPDATE_DAILY_FLOW_FIXTURES")=="1"{if e=os.MkdirAll(filepath.Dir(path),0755);e!=nil{t.Fatal(e)};if e=os.WriteFile(path,raw,0644);e!=nil{t.Fatal(e)}};golden,e:=os.ReadFile(path);if e!=nil{t.Fatal(e)};if string(raw)!=string(golden){t.Fatalf("fixture drift %s",name)}}
}
