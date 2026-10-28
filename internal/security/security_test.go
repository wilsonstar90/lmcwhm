package security

import "testing"

func TestParseEvent(t *testing.T) {
	line := `{"transaction":{"client_ip":"198.51.100.4","time_stamp":"Wed Oct 28 04:09:22 2026","request":{"method":"GET","uri":"/?id=1' OR 1=1--","headers":{"Host":"shop.test"}},"response":{"http_code":403},"messages":[{"message":"SQL Injection Attack Detected via libinjection","details":{"ruleId":"942100","severity":"2","data":"Matched Data: s&1c found"}},{"message":"Inbound Anomaly Score Exceeded (Total Score: 5)","details":{"ruleId":"949110","severity":"0"}}]}}`
	e, ok := ParseEvent(line)
	if !ok || e.IP != "198.51.100.4" || e.Host != "shop.test" || !e.Blocked || e.Time == 0 {
		t.Fatalf("%+v", e)
	}
	if len(e.Messages) != 1 || e.Messages[0].ID != "942100" || e.Messages[0].Severity != "CRITICAL" {
		t.Fatalf("messages %+v", e.Messages)
	}
	if _, ok := ParseEvent("not json"); ok {
		t.Fatal("parsed garbage")
	}
}
