package security

import "testing"

const crsFixture = `### /opt/owasp-crs/rules/REQUEST-901-INITIALIZATION.conf
SecAction "id:901001,phase:1,pass,nolog"
### /opt/owasp-crs/rules/REQUEST-942-APPLICATION-ATTACK-SQLI.conf
# comment
SecRule TX:DETECTION_PARANOIA_LEVEL "@lt 1" "id:942011,phase:1,pass,nolog,skipAfter:END-REQUEST-942"
SecRule REQUEST_COOKIES|ARGS "@detectSQLi" \
    "id:942100,\
    phase:2,\
    block,\
    msg:'SQL Injection Attack Detected via libinjection',\
    tag:'paranoia-level/1',\
    severity:'CRITICAL'"

SecRule ARGS "@rx foo" \
    "id:942200,\
    phase:2,\
    msg:'Detects MySQL comment-/space-obfuscated injections and backtick termination',\
    tag:'paranoia-level/2',\
    severity:'CRITICAL',\
    chain"
    SecRule MATCHED_VARS "@rx bar" "t:none"
### /opt/owasp-crs/rules/REQUEST-941-APPLICATION-ATTACK-XSS.conf
SecRule ARGS "@detectXSS" "id:941100,phase:2,msg:'XSS Attack Detected via libinjection',tag:'paranoia-level/1',severity:'CRITICAL'"
`

func TestParseCRS(t *testing.T) {
	cats := ParseCRS(crsFixture)
	if len(cats) != 2 || cats[0].Code != "941" || cats[1].Code != "942" {
		t.Fatalf("%+v", cats)
	}
	sql := cats[1].Rules
	if len(sql) != 2 || sql[0].ID != "942100" || sql[0].Level != 1 || sql[0].Severity != "CRITICAL" || sql[0].Phase != 2 ||
		sql[1].ID != "942200" || sql[1].Level != 2 || sql[1].Message == "" {
		t.Fatalf("%+v", sql)
	}
}
