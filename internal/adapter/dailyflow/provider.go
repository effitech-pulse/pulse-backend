// Package dailyflow формирует только воспроизводимый тестовый суточный набор.
package dailyflow

import (
 "context"
 "crypto/sha256"
 _ "embed"
 "encoding/binary"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "strconv"
 "time"

 "github.com/effitech-pulse/pulse-backend/internal/domain"
)

//go:embed profiles/prep100-v1.json
var manifest []byte

// GoldenDatasetSHA256 фиксирует результат recipe v1; изменение требует новой версии.
const GoldenDatasetSHA256 = "4124916953c1344c001ab123b18cfc4b0cbc5efde8f7eaf7b024e0b367ac0b83"

type Provider struct { profile domain.DailyFlowProfile; rows map[int64][]domain.DailyFlowSourceRow }
func digest(raw []byte) string { sum:=sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func New(now time.Time) (*Provider,error) {
 var profile domain.DailyFlowProfile
 if err:=json.Unmarshal(manifest,&profile);err!=nil{return nil,fmt.Errorf("daily profile: %w",err)}
 end,err:=time.Parse("2006-01-02",profile.EndDate);if err!=nil{return nil,err}
 msk:=time.FixedZone("Europe/Moscow",10800)
 closed:=time.Date(end.Year(),end.Month(),end.Day()+1,0,0,0,0,msk)
 if now.Before(closed){return nil,fmt.Errorf("daily profile contains an unclosed day")}
 if len(profile.Members)!=100 || profile.DatasetVersion!="prep100-daily-v1" || profile.RecipeVersion!="daily-liquid-synthetic-v1" || profile.StartDate!="2026-07-27" || profile.EndDate!="2026-10-24" || profile.CohortVersion!="presentation-v1" || profile.Canonicalization!="daily-flow-canonical-v1" {return nil,fmt.Errorf("invalid daily profile")}
 p:=&Provider{profile:profile,rows:map[int64][]domain.DailyFlowSourceRow{}}
 p.profile.ManifestSHA256=digest(manifest)
 families:=map[string]int{};external:=map[int64]bool{};ordinals:=map[int]bool{}
 type memberRows struct {WellID int64 `json:"well_id"`;Rows []domain.DailyFlowSourceRow `json:"rows"`}
 all:=make([]memberRows,0,100)
 last:=int64(0)
 for _,m:=range profile.Members {
  if m.WellID<=last || m.UnitekID>=0 || m.ArmitsID!=m.UnitekID || external[m.UnitekID] || ordinals[m.Ordinal] || m.Ordinal<0 || m.Ordinal>=100 {return nil,fmt.Errorf("invalid daily binding")}
  last=m.WellID;external[m.UnitekID]=true;ordinals[m.Ordinal]=true;families[m.ScenarioCode]++
  rows:=make([]domain.DailyFlowSourceRow,0,90);start,_:=time.Parse("2006-01-02",profile.StartDate)
  for day:=0;day<90;day++ {date:=start.AddDate(0,0,day).Format("2006-01-02");r,e:=generate(profile.Seed,m,day,date);if e!=nil{return nil,e};rows=append(rows,r)}
  p.rows[m.WellID]=rows;all=append(all,memberRows{m.WellID,rows})
 }
 if len(families)!=5{return nil,fmt.Errorf("invalid daily families")};for _,n:=range families{if n!=20{return nil,fmt.Errorf("invalid daily family count")}}
 canonical:=struct {Canonicalization string `json:"canonicalization"`;Profile domain.DailyFlowProfile `json:"profile"`;Members []memberRows `json:"members"`}{profile.Canonicalization,profile,all}
 raw,err:=json.Marshal(canonical);if err!=nil{return nil,err};p.profile.DatasetSHA256=digest(raw)
 if p.profile.DatasetSHA256!=GoldenDatasetSHA256{return nil,fmt.Errorf("daily recipe golden mismatch")}
 return p,nil
}
func generate(seed string,m domain.DailyFlowMember,day int,date string)(domain.DailyFlowSourceRow,error){
 sum:=sha256.Sum256([]byte(seed+"|"+strconv.Itoa(m.Ordinal)+"|"+date));j:=int64(binary.BigEndian.Uint16(sum[:2])%101)-50
 expected:=int64(10000+(m.Ordinal/5)*200)+j;actual:=expected+j
 row:=domain.DailyFlowSourceRow{Date:date,ActualHundredths:&actual,ExpectedHundredths:&expected,RecordState:"present"}
 switch m.ScenarioCode {
 case "steady":
 case "decline":actual=expected-int64(day)*45
 case "zero_actual":if day%11==0 || day==89 {actual=0}
 case "gaps":switch day%10 {case 0:row.ActualHundredths=nil;case 1:row.ExpectedHundredths=nil;case 2:row.ActualHundredths=nil;row.ExpectedHundredths=nil;case 3:row.RecordState="missing_day";row.ActualHundredths=nil;row.ExpectedHundredths=nil};if day==89{row.ActualHundredths=nil}
 case "zero_expected":if day%3==0 || day==89 {expected=0;if day%2==0{actual=0}}
 default:return row,fmt.Errorf("invalid daily scenario")
 }
 return row,nil
}
func (p *Provider) Profile() domain.DailyFlowProfile {v:=p.profile;v.Members=append([]domain.DailyFlowMember(nil),v.Members...);return v}
func (p *Provider) Read(ctx context.Context,id int64,start,end string)([]domain.DailyFlowSourceRow,error){
 rows,ok:=p.rows[id];if !ok{return nil,domain.NewError(domain.Unavailable,"daily_flow_unavailable")}
 out:=make([]domain.DailyFlowSourceRow,0,90)
 for _,r:=range rows {if err:=ctx.Err();err!=nil{return nil,err};if r.Date>=start && r.Date<=end {if r.ActualHundredths!=nil{v:=*r.ActualHundredths;r.ActualHundredths=&v};if r.ExpectedHundredths!=nil{v:=*r.ExpectedHundredths;r.ExpectedHundredths=&v};out=append(out,r)}}
 return out,nil
}
