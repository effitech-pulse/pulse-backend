package dailyflow

import (
 "context"
 "reflect"
 "testing"
 "time"
)

func TestDailyFlowProfileDeterministic100(t *testing.T) {
 p,err:=New(time.Date(2026,10,25,0,0,0,0,time.FixedZone("MSK",10800))); if err!=nil {t.Fatal(err)}
 profile:=p.Profile(); if len(profile.Members)!=100 {t.Fatal(len(profile.Members))}
 families:=map[string]int{}; count:=0
 for _,m:=range profile.Members {families[m.ScenarioCode]++; a,e:=p.Read(context.Background(),m.WellID,profile.StartDate,profile.EndDate);if e!=nil{t.Fatal(e)};count+=len(a);q,e:=New(time.Now().AddDate(1,0,0));if e!=nil{t.Fatal(e)};b,e:=q.Read(context.Background(),m.WellID,profile.StartDate,profile.EndDate);if e!=nil||!reflect.DeepEqual(a,b){t.Fatal("order/restart drift")}}
 if len(families)!=5 || count!=9000 {t.Fatal(families,count)};for _,n:=range families {if n!=20 {t.Fatal(families)}}
 if _,err=New(time.Date(2026,10,24,23,59,59,0,time.FixedZone("MSK",10800)));err==nil{t.Fatal("unclosed day accepted")}
 if profile.DatasetSHA256==""{t.Fatal("missing identity")}
}
