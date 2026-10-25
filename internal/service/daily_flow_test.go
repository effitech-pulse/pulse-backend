package service

import (
 "context"
 "testing"
 "time"

 "github.com/effitech-pulse/pulse-backend/internal/adapter/dailyflow"
 "github.com/effitech-pulse/pulse-backend/internal/domain"
)
type dailyProvider struct {DailyFlowProvider; rows []domain.DailyFlowSourceRow; calls int}
func(p *dailyProvider)Read(context.Context,int64,string,string)([]domain.DailyFlowSourceRow,error){p.calls++;return p.rows,nil}
func dailySetup(t *testing.T)(*DailyFlow,*dailyProvider,*cohortFixture){t.Helper();p,e:=dailyflow.New(time.Date(2026,10,25,0,0,0,0,time.UTC));if e!=nil{t.Fatal(e)};f:=&cohortFixture{cohort:domain.Cohort{Version:"presentation-v1",ManifestSHA256:p.Profile().CohortManifestSHA256}};for _,m:=range p.Profile().Members{u,a:=m.UnitekID,m.ArmitsID;f.wells=append(f.wells,domain.Well{ID:m.WellID,UnitekID:&u,ArmitsID:&a,FundType:"presentation",Active:true})};scope,e:=NewFundScope(internalFixtureContext(),f,"presentation-v1");if e!=nil{t.Fatal(e)};provider:=&dailyProvider{DailyFlowProvider:p};return NewDailyFlow(NewCatalog(f).WithScope(scope),scope,provider,nil,"synthetic"),provider,f}
func TestDailyFlowComparisonAndCoverage(t *testing.T){
 cases:=[]struct{a,e *int64;delta,pct *float64;status string}{{ptr(int64(9000)),ptr(int64(10000)),ptr(-10.0),ptr(-10.0),"paired"},{ptr(int64(0)),ptr(int64(10000)),ptr(-100.0),ptr(-100.0),"paired"},{ptr(int64(0)),ptr(int64(0)),ptr(0.0),nil,"zero_expected"},{ptr(int64(1000)),ptr(int64(0)),ptr(10.0),nil,"zero_expected"},{nil,ptr(int64(100)),nil,nil,"missing_actual"},{ptr(int64(1)),nil,nil,nil,"missing_expected"},{nil,nil,nil,nil,"missing_both"},{ptr(int64(19999)),ptr(int64(20000)),ptr(-0.01),ptr(-0.01),"paired"},{ptr(int64(20001)),ptr(int64(20000)),ptr(0.01),ptr(0.01),"paired"}}
 for _,c:=range cases{s,p,_:=dailySetup(t);p.rows=[]domain.DailyFlowSourceRow{{Date:"2026-10-24",ActualHundredths:c.a,ExpectedHundredths:c.e,RecordState:"present"}};r,e:=s.Read(internalFixtureContext(),1001,domain.DailyFlowQuery{StartDate:"2026-10-24",EndDate:"2026-10-24"});if e!=nil{t.Fatal(e)};v:=r.View.Rows[0];eq:=func(a,b *float64)bool{return a==nil&&b==nil||a!=nil&&b!=nil&&*a==*b};if !eq(v.Delta,c.delta)||!eq(v.DeltaPercent,c.pct)||v.ComparisonStatus!=c.status{t.Fatalf("%+v",v)}}
 s,p,_:=dailySetup(t);p.rows=[]domain.DailyFlowSourceRow{{Date:"2026-10-23",ActualHundredths:ptr(int64(0)),ExpectedHundredths:ptr(int64(0)),RecordState:"present"}};r,e:=s.Read(internalFixtureContext(),1001,domain.DailyFlowQuery{});if e!=nil{t.Fatal(e)};if r.View.StartDate!="2026-09-25"||len(r.View.Rows)!=30||r.View.Rows[29].Actual!=nil||r.View.Rows[29].RecordState!="missing_day"||r.View.Coverage.PairedDays!=1||r.View.Coverage.ComparablePercentDays!=0{t.Fatal(r.View)}
}
func TestDailyFlowReadIntervals(t *testing.T){s,_,_:=dailySetup(t);for _,q:=range []domain.DailyFlowQuery{{StartDate:"2026-10-24"},{StartDate:"2026-10-25",EndDate:"2026-10-25"},{StartDate:"2026-10-24",EndDate:"2026-10-23"},{StartDate:"2026-07-26",EndDate:"2026-10-24"},{StartDate:"2026-02-29",EndDate:"2026-10-24"}}{if _,e:=s.Read(internalFixtureContext(),1001,q);e==nil{t.Fatal(q)}};for _,n:=range []int{1,7,30,90}{end,_:=time.Parse("2006-01-02","2026-10-24");r,e:=s.Read(internalFixtureContext(),1001,domain.DailyFlowQuery{StartDate:end.AddDate(0,0,1-n).Format("2006-01-02"),EndDate:"2026-10-24"});if e!=nil||len(r.View.Rows)!=n{t.Fatal(n,e)}}}
func TestDailyFlowAuthBeforeProfile(t *testing.T){s,p,f:=dailySetup(t);s.provider=nil;s.mode="off";if _,e:=s.Read(context.Background(),1001,domain.DailyFlowQuery{DatasetVersion:"bad"});e==nil{t.Fatal("anonymous accepted")};if _,e:=s.Read(internalFixtureContext(),9999,domain.DailyFlowQuery{});e==nil{t.Fatal("outside cohort accepted")};s.mode="synthetic";s.provider=p;f.wells[0].UnitekID=ptr(int64(-42));if _,e:=s.Read(internalFixtureContext(),1001,domain.DailyFlowQuery{});e==nil{t.Fatal("binding accepted")}}
