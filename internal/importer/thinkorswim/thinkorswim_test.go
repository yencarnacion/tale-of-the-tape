package thinkorswim

import (
	"testing"
	"time"
)

func TestParseCashStatement(t *testing.T) {
	data := []byte("Account Statement for ABC (Cash)\n\nCash Balance\nDATE,TIME,TYPE,REF #,DESCRIPTION,Misc Fees,Commissions & Fees\n1/2/26,13:00:00,TRD,=\"1\",\"BOT +1,000 ABC @10.25\",-0.10,\n1/2/26,13:01:00,TRD,=\"2\",\"SOLD -1,000 ABC @10.50\",,\n1/2/26,13:02:00,TRD,=\"3\",\"BOT +1 ABC 2 JAN 26 10 CALL @1.0\",,\n")
	r := Parse(data, time.UTC)
	if r.Accepted != 2 || len(r.Rejected) != 1 {
		t.Fatalf("accepted=%d rejects=%d", r.Accepted, len(r.Rejected))
	}
	if r.Executions[0].Quantity != 1000 || r.Executions[0].Fees != 100000 {
		t.Fatalf("bad execution %#v", r.Executions[0])
	}
}
