package network

import (
	"fmt"
	"lab-router/internal/model"
	"lab-router/internal/system"
	"net"
	"net/netip"
	"sort"
	"strings"
)

const Dir = "/var/lib/lab-router-network"

func Hostapd(c model.Config) string {
	w := c.WiFi
	band := "a"
	if w.Channel <= 14 {
		band = "g"
	}
	security := "wpa_key_mgmt=WPA-PSK\n"
	switch w.Security {
	case "wpa3":
		security = "wpa_key_mgmt=SAE\nieee80211w=2\nsae_require_mfp=1\n"
	case "mixed":
		security = "wpa_key_mgmt=WPA-PSK SAE\nieee80211w=1\n"
	}
	return fmt.Sprintf("interface=ap-tr069\ndriver=nl80211\nctrl_interface=/run/lab-router-hostapd\nssid=%s\ncountry_code=BR\nhw_mode=%s\nchannel=%d\nauth_algs=1\nwpa=2\nwpa_passphrase=%s\n%srsn_pairwise=CCMP\nignore_broadcast_ssid=%d\nap_isolate=%d\nmax_num_sta=%d\nmacaddr_acl=0\ndeny_mac_file=%s/deny.mac\n", w.SSID, band, w.Channel, w.Password, security, bit(w.Hidden), bit(w.Isolation), w.MaxClients, Dir)
}
func bit(v bool) int {
	if v {
		return 1
	}
	return 0
}
func Dnsmasq(c model.Config) string {
	p, _ := netip.ParsePrefix(c.LAN.CIDR)
	mask := net.IP(net.CIDRMask(p.Bits(), 32)).String()
	s := fmt.Sprintf("interface=ap-tr069\nexcept-interface=wlp2s0\nbind-dynamic\nlisten-address=%s\nport=0\nno-hosts\nno-resolv\ndhcp-authoritative\ndhcp-leasefile=/var/lib/lab-router-dhcp/leases\nlog-dhcp\nlog-facility=-\n", p.Addr())
	if c.DHCP.Enabled {
		s += fmt.Sprintf("dhcp-range=%s,%s,%s,%dh\ndhcp-option=3,%s\ndhcp-option=6,%s\n", c.DHCP.Start, c.DHCP.End, mask, c.DHCP.LeaseHours, p.Addr(), strings.Join(c.DHCP.DNS, ","))
		for _, r := range c.DHCP.Reservations {
			s += fmt.Sprintf("dhcp-host=%s,%s,%s\n", r.MAC, r.IP, r.Hostname)
		}
	}
	return s
}
func Firewall(c model.Config, blocked []string) string {
	p, _ := netip.ParsePrefix(c.LAN.CIDR)
	subnet := p.Masked().String()
	set := map[string]bool{"10.0.0.0/8": true, "172.16.0.0/12": true, "192.168.0.0/16": true, "100.64.0.0/10": true, "169.254.0.0/16": true, "127.0.0.0/8": true, "224.0.0.0/4": true, "240.0.0.0/4": true, "0.0.0.0/8": true}
	for _, r := range blocked {
		if p, e := netip.ParsePrefix(r); e == nil && p.Addr().Is4() {
			set[p.Masked().String()] = true
		}
	}
	dest := []string{}
	for r := range set {
		dest = append(dest, r)
	}
	sort.Strings(dest)
	s := `table inet lab_router {
 comment "Owned by LAB Router"
 chain input {
 type filter hook input priority -10; policy accept;
 iifname "ap-tr069" meta nfproto ipv6 counter drop
 iifname "ap-tr069" udp dport 67 counter accept
 iifname "ap-tr069" counter drop
 }
 chain forward {
 type filter hook forward priority -10; policy accept;
 iifname "ap-tr069" meta nfproto ipv6 counter drop
 oifname "ap-tr069" meta nfproto ipv6 counter drop
 iifname "ap-tr069" ip saddr != ` + subnet + ` counter drop
 iifname "ap-tr069" ip daddr { ` + strings.Join(dest, ", ") + ` } counter drop
 iifname "ap-tr069" oifname != "wlp2s0" counter drop
 oifname "ap-tr069" iifname != "wlp2s0" counter drop
 `
	for _, m := range c.WiFi.DenyMAC {
		s += fmt.Sprintf("iifname \"ap-tr069\" ether saddr %s counter drop\n", m)
	}
	if c.Firewall.NAT {
		s += `iifname "ap-tr069" oifname "wlp2s0" counter accept
 oifname "ap-tr069" ct state established,related counter accept
 `
		for _, f := range c.Firewall.Forwards {
			s += fmt.Sprintf("iifname \"wlp2s0\" oifname \"ap-tr069\" ip daddr %s %s dport %d ct status dnat counter accept\n", f.IP, f.Protocol, f.Internal)
		}
	}
	s += `iifname "ap-tr069" counter drop
 oifname "ap-tr069" counter drop
 }
}
table ip lab_router_nat {
 comment "Owned by LAB Router"
 chain postrouting {
 type nat hook postrouting priority 110; policy accept;
 `
	if c.Firewall.NAT {
		s += fmt.Sprintf("ip saddr %s oifname \"wlp2s0\" counter masquerade\n", subnet)
	}
	s += "}\nchain prerouting {\ntype nat hook prerouting priority -90; policy accept;\n"
	if c.Firewall.NAT {
		for _, f := range c.Firewall.Forwards {
			s += fmt.Sprintf("iifname \"wlp2s0\" fib daddr type local %s dport %d counter dnat to %s:%d\n", f.Protocol, f.External, f.IP, f.Internal)
		}
	}
	return s + "}\n}\n"
}
func ValidateEnvironment(c model.Config) error {
	if e := model.Validate(c); e != nil {
		return e
	}
	wan := system.WirelessInfo(system.WAN)
	ap := system.WirelessInfo(system.AP)
	if wan.Channel == 0 {
		return fmt.Errorf("WAN sem canal confirmado; aplicação recusada")
	}
	if ap.PHY == "" || ap.PHY != wan.PHY {
		return fmt.Errorf("interface AP não pertence ao rádio WAN esperado")
	}
	if c.WiFi.Channel != wan.Channel {
		return fmt.Errorf("STA+AP: canal deve permanecer %d (%d MHz)", wan.Channel, wan.Frequency)
	}
	p, _ := netip.ParsePrefix(c.LAN.CIDR)
	ifs, e := net.Interfaces()
	if e != nil {
		return e
	}
	for _, i := range ifs {
		if i.Name == system.AP {
			continue
		}
		aa, _ := i.Addrs()
		for _, a := range aa {
			other, e := netip.ParsePrefix(a.String())
			if e == nil && other.Addr().Is4() && p.Overlaps(other) {
				return fmt.Errorf("LAN conflita com %s", i.Name)
			}
		}
	}
	return nil
}
