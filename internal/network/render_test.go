package network

import (
	"lab-router/internal/model"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestFirewallScope(t *testing.T) {
	c := model.Default()
	c.Firewall.Forwards = []model.PortForward{{Protocol: "tcp", External: 18080, IP: "192.168.50.110", Internal: 80}}
	s := Firewall(c, []string{"203.0.113.0/24"})
	for _, required := range []string{"table inet lab_router", "table ip lab_router_nat", "203.0.113.0/24", "oifname != \"wlp2s0\" counter drop", "ip saddr != 192.168.50.0/24", "meta nfproto ipv6 counter drop", "fib daddr type local"} {
		if !strings.Contains(s, required) {
			t.Fatalf("missing security rule: %s", required)
		}
	}
	if strings.Contains(s, "flush ruleset") {
		t.Fatal("global flush")
	}
	if s != Firewall(c, []string{"203.0.113.0/24"}) {
		t.Fatal("non deterministic rules")
	}
}
func TestDnsmasqSyntax(t *testing.T) {
	binary, e := exec.LookPath("dnsmasq")
	if e != nil {
		t.Skip("dnsmasq missing")
	}
	c := model.Default()
	c.DHCP.Reservations = []model.Reservation{{MAC: "02:00:00:00:00:05", IP: "192.168.50.105", Hostname: "lab-phone"}}
	p := t.TempDir() + "/dnsmasq.conf"
	os.WriteFile(p, []byte(Dnsmasq(c)), 0600)
	b, e := exec.Command(binary, "--test", "--conf-file="+p).CombinedOutput()
	if e != nil {
		t.Fatalf("dnsmasq syntax: %s", b)
	}
}
func TestHostapdRenderedScope(t *testing.T) {
	c := model.Default()
	s := Hostapd(c)
	if strings.Contains(s, "wlp2s0") || !strings.Contains(s, "interface=ap-tr069\n") {
		t.Fatal("WAN exposed to hostapd")
	}
	if !strings.Contains(s, "ap_isolate=1") || !strings.Contains(s, "country_code=BR") {
		t.Fatal("required safety missing")
	}
}
