package job

import (
 "errors"
 "testing"
 "time"
)

func TestPaidProfileTaskRejectsMalformedOrUntrustedOperations(t *testing.T){
 base:=elxProfileJob{
  JobID:"job",ClaimID:"claim",Operation:"create-disabled",
  ProfileID:"profile",EntitlementID:"entitlement",VLESSID:"vless",
  SubscriptionID:"subscription",ClientEmail:"elx-00000001",
  ValidUntil:time.Now().Add(time.Hour),
 }
 if !base.valid(){t.Fatal("valid trusted provisioning job rejected")}
 for name,modify:=range map[string]func(*elxProfileJob){
  "missing-claim":func(j *elxProfileJob){j.ClaimID=""},
  "foreign-email":func(j *elxProfileJob){j.ClientEmail="other-account"},
  "unrecognized-operation":func(j *elxProfileJob){j.Operation="delete"},
  "zero-expiry":func(j *elxProfileJob){j.ValidUntil=time.Time{}},
 }{
  t.Run(name,func(t *testing.T){
   probe:=base;modify(&probe)
   if probe.valid(){t.Fatalf("invalid task accepted: %+v",probe)}
  })
 }
}
func TestPaidProfileInboundConfigurationIsExplicit(t *testing.T){
 if _,err:=elxProvisionInbounds("");err==nil{t.Fatal("missing inbound list accepted")}
 for _,raw:=range []string{"0","-1","abc","1,1","1,","1,10000001"}{
  if _,err:=elxProvisionInbounds(raw);err==nil{
   t.Fatalf("invalid paid profile inbounds accepted: %q",raw)
  }
 }
 ids,err:=elxProvisionInbounds("3,4, 5")
 if err!=nil||len(ids)!=3||ids[0]!=3||ids[1]!=4||ids[2]!=5{
  t.Fatalf("inbound parsing: %+v %v",ids,err)
 }
}
func TestPaidProfileNeverAcknowledgesWithoutEnabledRecord(t *testing.T){
 j:=NewElXProfileJob()
 if err:=j.paidProfileConverged(nil,[]int{1});!errors.Is(err,errPaidProfileNotReady){
  t.Fatalf("missing paid profile was acknowledged: %v",err)
 }
}
