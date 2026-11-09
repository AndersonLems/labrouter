package model

import "testing"

func TestValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Config)
	}{{"injection", func(c *Config) { c.WiFi.SSID = "lab\ninterface=wlp2s0" }}, {"gateway_in_pool", func(c *Config) { c.DHCP.Start = "192.168.50.1" }}, {"off_subnet", func(c *Config) { c.DHCP.End = "192.168.3.120" }}, {"short_password", func(c *Config) { c.WiFi.Password = "123" }}, {"url_creds", func(c *Config) { c.CWMP.URL = "http://admin:secret@example.com/" }}, {"stun", func(c *Config) { c.CWMP.STUNEnable = true; c.CWMP.STUNPort = 0 }}, {"admin_port", func(c *Config) { c.Firewall.Forwards = []PortForward{{"tcp", 22, "192.168.50.101", 22}} }}, {"broadcast", func(c *Config) { c.LAN.CIDR = "192.168.50.255/24" }}} {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.edit(&c)
			if Validate(c) == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	if e := Validate(Default()); e != nil {
		t.Fatal(e)
	}
}
func TestSecretRedaction(t *testing.T) {
	c := Default()
	c.CWMP.Password = "secret"
	c.CWMP.CRPassword = "request"
	r := c.Redacted()
	if r.WiFi.Password != "" || r.CWMP.Password != "" || r.CWMP.CRPassword != "" {
		t.Fatal("credential leaked")
	}
	r.PreserveSecrets(c)
	if r.WiFi.Password != c.WiFi.Password {
		t.Fatal("secret not preserved")
	}
}

func TestConnReqFallsBackToCWMPCredentials(t *testing.T) {
	c := CWMP{Username: "cpe-user", Password: "cpe-pass"}
	if u, p := c.ConnReq(); u != "cpe-user" || p != "cpe-pass" {
		t.Fatalf("fallback: got %q/%q", u, p)
	}
	c.CRUsername, c.CRPassword = "cr", "secret"
	if u, p := c.ConnReq(); u != "cr" || p != "secret" {
		t.Fatalf("dedicated pair must win: got %q/%q", u, p)
	}
	c.CRPassword = "" // incomplete dedicated pair is ignored
	if u, _ := c.ConnReq(); u != "cpe-user" {
		t.Fatalf("incomplete pair: got %q", u)
	}
}

func TestPPPoEValidation(t *testing.T) {
	c := Default()
	c.PPPoE = PPPoE{Enabled: true, Username: "user@isp", Password: "ok-pass"}
	if e := Validate(c); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"has space", "quo\"te", "back\\slash", "hash#", ""} {
		c.PPPoE.Password = bad
		if Validate(c) == nil {
			t.Fatalf("password %q accepted (would break pppd option file)", bad)
		}
	}
	c.PPPoE.Password = "ok"
	c.BRAS.Accounts = append(c.BRAS.Accounts, BRASAccount{"lab", "x"})
	if Validate(c) == nil {
		t.Fatal("duplicate BRAS user accepted")
	}
}
