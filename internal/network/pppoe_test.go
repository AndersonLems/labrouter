package network

import (
	"lab-router/internal/model"
	"strings"
	"testing"
)

func TestPPPoERenderIsolated(t *testing.T) {
	c := model.Default()
	c.PPPoE = model.PPPoE{Enabled: true, Username: "cliente@isp", Password: "s3nha", ServiceName: "internet"}
	o := PPPoEOptions(c)
	for _, want := range []string{"nic-pppoe-wan", "ifname ppp-lab", "user cliente@isp", "password s3nha", "nodefaultroute", "ip-up-script /bin/true", "noipv6", "rp_pppoe_service internet", "mru 1492"} {
		if !strings.Contains(o, want) {
			t.Fatalf("options missing %q:\n%s", want, o)
		}
	}
	if strings.Contains(o, "usepeerdns") || strings.Contains(o, "\ndefaultroute") {
		t.Fatal("dialer must never touch host DNS or default route")
	}
	if s := BRASSecrets(c); !strings.Contains(s, `"lab" * "lab123" *`) {
		t.Fatalf("secrets: %s", s)
	}
	if e := BRASEnv(c); !strings.Contains(e, "-S internet") || !strings.Contains(e, "-I bras0") {
		t.Fatalf("env: %s", e)
	}
}

func TestClassifyPPPLog(t *testing.T) {
	cases := map[string]string{
		"CHAP authentication failed: Access denied\nConnection terminated.":       "ERROR_AUTHENTICATION_FAILURE",
		"Timeout waiting for PADO packets\nUnable to complete PPPoE Discovery":    "ERROR_NO_ANSWER",
		"CHAP authentication failed\n...\nlocal  IP address 100.127.250.10":       "ERROR_NONE",
		"No response to 3 echo-requests\nSerial link appears to be disconnected.": "ERROR_ISP_TIME_OUT",
		"LCP terminated by peer": "ERROR_ISP_DISCONNECT",
		"":                       "ERROR_NONE",
	}
	for log, want := range cases {
		if got, _ := classifyPPPLog(log); got != want {
			t.Fatalf("%q → %s, want %s", log, got, want)
		}
	}
}
