package model

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

type WiFi struct {
	Enabled    bool     `json:"enabled"`
	SSID       string   `json:"ssid"`
	Password   string   `json:"password"`
	Security   string   `json:"security"`
	Channel    int      `json:"channel"`
	Width      int      `json:"width"`
	Hidden     bool     `json:"hidden"`
	Isolation  bool     `json:"isolation"`
	MaxClients int      `json:"max_clients"`
	DenyMAC    []string `json:"deny_mac"`
}
type LAN struct {
	CIDR string `json:"cidr"`
}
type Reservation struct {
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
}
type DHCP struct {
	Enabled      bool          `json:"enabled"`
	Start        string        `json:"start"`
	End          string        `json:"end"`
	LeaseHours   int           `json:"lease_hours"`
	DNS          []string      `json:"dns"`
	Reservations []Reservation `json:"reservations"`
}
type PortForward struct {
	Protocol string `json:"protocol"`
	External int    `json:"external"`
	IP       string `json:"ip"`
	Internal int    `json:"internal"`
}
type Firewall struct {
	NAT      bool          `json:"nat"`
	Forwards []PortForward `json:"forwards"`
}
type CWMP struct {
	Enabled        bool     `json:"enabled"`
	URL            string   `json:"url"`
	Username       string   `json:"username"`
	Password       string   `json:"password"`
	PeriodicEnable bool     `json:"periodic_enable"`
	Interval       int      `json:"interval"`
	CRURL          string   `json:"cr_url"`
	CRUsername     string   `json:"cr_username"`
	CRPassword     string   `json:"cr_password"`
	Manufacturer   string   `json:"manufacturer"`
	ProductClass   string   `json:"product_class"`
	Serial         string   `json:"serial"`
	OUI            string   `json:"oui"`
	STUNMode       string   `json:"stun_mode"`
	STUNKeepAlive  int      `json:"stun_keepalive"`
	STUNLocalPort  int      `json:"stun_local_port"`
	XMPPDomain     string   `json:"xmpp_domain"`
	XMPPResource   string   `json:"xmpp_resource"`
	XMPPAllowed    []string `json:"xmpp_allowed"`
	STUNEnable     bool     `json:"stun_enable"`
	STUNServer     string   `json:"stun_server"`
	STUNPort       int      `json:"stun_port"`
	STUNUsername   string   `json:"stun_username"`
	STUNPassword   string   `json:"stun_password"`
	XMPPEnable     bool     `json:"xmpp_enable"`
	XMPPServer     string   `json:"xmpp_server"`
	XMPPPort       int      `json:"xmpp_port"`
	XMPPUsername   string   `json:"xmpp_username"`
	XMPPPassword   string   `json:"xmpp_password"`
	// XMPPInsecureTLS skips certificate verification (self-signed lab servers only).
	XMPPInsecureTLS bool `json:"xmpp_insecure_tls"`
}

// PPPoE is the CPE's PPPoE dialer (TR-098 WANPPPConnection.1). It dials the
// simulated BRAS only; it never replaces the host default route or DNS.
type PPPoE struct {
	Enabled     bool   `json:"enabled"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	ServiceName string `json:"service_name"`
	MRU         int    `json:"mru"` // 0 = 1492
}

// BRAS is the lab-only PPPoE concentrator running in network namespace lab-bras.
type BRAS struct {
	ServiceName string        `json:"service_name"`
	Accounts    []BRASAccount `json:"accounts"`
}
type BRASAccount struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type Config struct {
	ParameterKey string   `json:"parameter_key"`
	WiFi         WiFi     `json:"wifi"`
	LAN          LAN      `json:"lan"`
	DHCP         DHCP     `json:"dhcp"`
	Firewall     Firewall `json:"firewall"`
	CWMP         CWMP     `json:"cwmp"`
	PPPoE        PPPoE    `json:"pppoe"`
	BRAS         BRAS     `json:"bras"`
}

// pppSafe accepts PPP credentials that can be written to pppd option and
// secrets files without quoting issues: printable, no whitespace/quotes/backslash.
func pppSafe(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if r <= ' ' || r > '~' || r == '"' || r == '\\' || r == '\'' || r == '#' {
			return false
		}
	}
	return true
}
func serviceNameOK(s string) bool {
	if len(s) > 32 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func Token() string {
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func Default() Config {
	return Config{WiFi: WiFi{Enabled: true, SSID: "LAB-TR069-REAL", Password: Token()[:20], Security: "wpa2", Channel: 40, Width: 20, Isolation: true, MaxClients: 16, DenyMAC: []string{}}, LAN: LAN{CIDR: "192.168.50.1/24"}, DHCP: DHCP{Enabled: true, Start: "192.168.50.100", End: "192.168.50.200", LeaseHours: 12, DNS: []string{"1.1.1.1", "8.8.8.8"}, Reservations: []Reservation{}}, Firewall: Firewall{NAT: true, Forwards: []PortForward{}}, CWMP: CWMP{URL: "http://acs.example.com:7547/", PeriodicEnable: true, Interval: 60, Manufacturer: "LAB Router", ProductClass: "LinuxVirtualRouter", Serial: "LAB-" + Token()[:16], OUI: "020000", STUNServer: "acs.example.com", STUNPort: 3478, STUNMode: "rfc3489", STUNKeepAlive: 30, XMPPResource: "lab-router", XMPPServer: "acs.example.com", XMPPPort: 5222}, PPPoE: PPPoE{ServiceName: "internet"}, BRAS: BRAS{ServiceName: "internet", Accounts: []BRASAccount{{Username: "lab", Password: "lab123"}}}}
}

var hostname = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,62}$`)

func safe(s string) bool { return !strings.ContainsAny(s, "\x00\r\n") }
func Validate(c Config) error {
	w := c.WiFi
	if len(w.SSID) < 1 || len(w.SSID) > 32 || !safe(w.SSID) {
		return fmt.Errorf("SSID deve ter 1–32 bytes, sem quebras de linha")
	}
	if len(w.Password) < 8 || len(w.Password) > 63 || !safe(w.Password) {
		return fmt.Errorf("senha Wi-Fi deve ter 8–63 bytes")
	}
	if w.Security != "wpa2" && w.Security != "wpa3" && w.Security != "mixed" {
		return fmt.Errorf("segurança inválida")
	}
	if w.Channel < 1 || w.Channel > 165 || w.Width != 20 || w.MaxClients < 1 || w.MaxClients > 128 {
		return fmt.Errorf("canal/clientes inválidos; largura validada nesta versão: 20 MHz")
	}
	for _, m := range w.DenyMAC {
		if a, e := net.ParseMAC(m); e != nil || len(a) != 6 {
			return fmt.Errorf("MAC inválido")
		}
	}
	p, e := netip.ParsePrefix(c.LAN.CIDR)
	if e != nil || !p.Addr().Is4() || p.Bits() < 24 || p.Bits() > 28 || p.Addr() == p.Masked().Addr() {
		return fmt.Errorf("LAN requer IPv4 /24 a /28 e endereço de host")
	}
	if !p.Addr().IsPrivate() {
		return fmt.Errorf("LAN deve usar endereçamento privado")
	}
	start, e := netip.ParseAddr(c.DHCP.Start)
	if e != nil || !p.Contains(start) {
		return fmt.Errorf("início DHCP fora da LAN")
	}
	end, e := netip.ParseAddr(c.DHCP.End)
	if e != nil || !p.Contains(end) || start.Compare(end) > 0 {
		return fmt.Errorf("fim DHCP inválido")
	}
	last := p.Masked().Addr()
	for i := 0; i < (1<<(32-p.Bits()))-1; i++ {
		last = last.Next()
	}
	if p.Addr() == last || start == p.Masked().Addr() || end == last || (start.Compare(p.Addr()) <= 0 && end.Compare(p.Addr()) >= 0) {
		return fmt.Errorf("pool inclui gateway/rede/broadcast")
	}
	if c.DHCP.LeaseHours < 1 || c.DHCP.LeaseHours > 8760 || len(c.DHCP.DNS) == 0 {
		return fmt.Errorf("concessão/DNS inválidos")
	}
	for _, d := range c.DHCP.DNS {
		a, e := netip.ParseAddr(d)
		if e != nil || !a.Is4() || !a.IsGlobalUnicast() {
			return fmt.Errorf("DNS IPv4 inválido")
		}
	}
	seen := map[string]bool{}
	for _, r := range c.DHCP.Reservations {
		m, e := net.ParseMAC(r.MAC)
		a, ae := netip.ParseAddr(r.IP)
		if e != nil || len(m) != 6 || ae != nil || !p.Contains(a) || a == p.Addr() || a == last || a == p.Masked().Addr() || !hostname.MatchString(r.Hostname) || seen[a.String()] || seen[m.String()] {
			return fmt.Errorf("reserva DHCP inválida/duplicada")
		}
		seen[a.String()] = true
		seen[m.String()] = true
	}
	ports := map[string]bool{}
	for _, f := range c.Firewall.Forwards {
		a, e := netip.ParseAddr(f.IP)
		k := fmt.Sprint(f.Protocol, f.External)
		if e != nil || !p.Contains(a) || a == p.Addr() || a == last || a == p.Masked().Addr() || (f.Protocol != "tcp" && f.Protocol != "udp") || f.External < 1024 || f.External > 65535 || f.Internal < 1 || f.Internal > 65535 || ports[k] {
			return fmt.Errorf("redirecionamento inválido (porta externa >=1024)")
		}
		ports[k] = true
	}
	u, e := url.Parse(c.CWMP.URL)
	if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("URL ACS inválida")
	}
	if c.CWMP.Interval < 5 || c.CWMP.Interval > 86400*30 { // lab: allows short intervals for testing
		return fmt.Errorf("Periodic Inform: 5–2592000 segundos")
	}
	if !regexp.MustCompile(`^[0-9A-Fa-f]{6}$`).MatchString(c.CWMP.OUI) {
		return fmt.Errorf("OUI deve ter 6 dígitos hexadecimais")
	}
	for _, v := range []string{c.CWMP.Serial, c.CWMP.Manufacturer, c.CWMP.ProductClass} {
		if len(v) == 0 || len(v) > 64 || !safe(v) {
			return fmt.Errorf("identidade inválida")
		}
	}
	if c.CWMP.STUNEnable {
		if !validHost(c.CWMP.STUNServer) || c.CWMP.STUNPort < 1 || c.CWMP.STUNPort > 65535 {
			return fmt.Errorf("servidor/porta STUN inválidos")
		}
		if c.CWMP.STUNMode != "" && c.CWMP.STUNMode != "rfc3489" && c.CWMP.STUNMode != "rfc5389" {
			return fmt.Errorf("modo STUN deve ser rfc3489 ou rfc5389")
		}
		if c.CWMP.STUNKeepAlive != 0 && (c.CWMP.STUNKeepAlive < 15 || c.CWMP.STUNKeepAlive > 600) {
			return fmt.Errorf("keepalive STUN: 15–600 segundos")
		}
		if c.CWMP.STUNLocalPort != 0 && (c.CWMP.STUNLocalPort < 1024 || c.CWMP.STUNLocalPort > 65535) {
			return fmt.Errorf("porta UDP local deve ser 0 ou 1024–65535")
		}
		if u, p := c.CWMP.ConnReq(); u == "" || p == "" {
			return fmt.Errorf("STUN habilitado: preencha usuário e senha CWMP (Configuração do ACS) ou credenciais Connection Request próprias")
		}
	}
	if c.CWMP.XMPPEnable {
		if !validHost(c.CWMP.XMPPServer) || c.CWMP.XMPPPort < 1 || c.CWMP.XMPPPort > 65535 || c.CWMP.XMPPUsername == "" || c.CWMP.XMPPPassword == "" {
			return fmt.Errorf("XMPP requer servidor, porta e conta própria")
		}
		if c.CWMP.XMPPDomain != "" && !validHost(c.CWMP.XMPPDomain) {
			return fmt.Errorf("domínio XMPP inválido")
		}
		if u, p := c.CWMP.ConnReq(); u == "" || p == "" {
			return fmt.Errorf("XMPP habilitado: preencha usuário e senha CWMP (Configuração do ACS) ou credenciais Connection Request próprias")
		}
		for _, v := range c.CWMP.XMPPAllowed {
			if !safe(v) || len(v) > 1024 || strings.ContainsAny(v, " <>'\"") {
				return fmt.Errorf("JID permitido inválido")
			}
		}
	}
	if p := c.PPPoE; p.Enabled {
		if !pppSafe(p.Username) || !pppSafe(p.Password) {
			return fmt.Errorf("PPPoE: usuário e senha com 1–64 caracteres, sem espaços, aspas, # ou barra invertida")
		}
	}
	if !serviceNameOK(c.PPPoE.ServiceName) || !serviceNameOK(c.BRAS.ServiceName) {
		return fmt.Errorf("Service-Name PPPoE: até 32 caracteres (letras, números, . _ -)")
	}
	if c.PPPoE.MRU != 0 && (c.PPPoE.MRU < 576 || c.PPPoE.MRU > 1492) {
		return fmt.Errorf("MRU PPPoE: 576–1492 (0 usa 1492)")
	}
	if len(c.BRAS.Accounts) > 32 {
		return fmt.Errorf("BRAS: no máximo 32 contas")
	}
	seenAcc := map[string]bool{}
	for _, a := range c.BRAS.Accounts {
		if !pppSafe(a.Username) || !pppSafe(a.Password) {
			return fmt.Errorf("BRAS: conta inválida (1–64 caracteres, sem espaços, aspas, # ou barra invertida)")
		}
		if seenAcc[a.Username] {
			return fmt.Errorf("BRAS: usuário %s duplicado", a.Username)
		}
		seenAcc[a.Username] = true
	}
	if c.CWMP.CRURL != "" {
		u, e := url.Parse(c.CWMP.CRURL)
		if e != nil || u.Scheme != "http" || u.Hostname() == "" || u.User != nil || u.Path != "/connection-request" {
			return fmt.Errorf("Connection Request requer http://host:7548/connection-request")
		}
	}
	return nil
}

// ConnReq returns the credentials the ACS must present in Connection Requests
// (HTTP Digest, UDP/STUN HMAC and XMPP IQ). A dedicated pair wins; otherwise the
// CWMP (ACS session) username/password are reused.
func (c CWMP) ConnReq() (string, string) {
	if c.CRUsername != "" && c.CRPassword != "" {
		return c.CRUsername, c.CRPassword
	}
	return c.Username, c.Password
}
func (c Config) Redacted() Config {
	c.WiFi.Password = ""
	c.CWMP.Password = ""
	c.CWMP.CRPassword = ""
	c.CWMP.STUNPassword = ""
	c.CWMP.XMPPPassword = ""
	c.PPPoE.Password = ""
	return c
}
func (c *Config) PreserveSecrets(old Config) {
	if c.WiFi.Password == "" {
		c.WiFi.Password = old.WiFi.Password
	}
	if c.CWMP.Password == "" {
		c.CWMP.Password = old.CWMP.Password
	}
	if c.CWMP.CRPassword == "" {
		c.CWMP.CRPassword = old.CWMP.CRPassword
	}
	if c.CWMP.STUNPassword == "" {
		c.CWMP.STUNPassword = old.CWMP.STUNPassword
	}
	if c.CWMP.XMPPPassword == "" {
		c.CWMP.XMPPPassword = old.CWMP.XMPPPassword
	}
	if c.PPPoE.Password == "" {
		c.PPPoE.Password = old.PPPoE.Password
	}
}

func validHost(s string) bool {
	return len(s) > 0 && len(s) <= 253 && safe(s) && !strings.ContainsAny(s, " /:@?#\t")
}
