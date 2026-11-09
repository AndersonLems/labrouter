package cwmp

// TR-098 client reporting:
//   LANDevice.1.Hosts.Host.{i}                       (DHCP leases + associated stations)
//   LANDevice.1.WLANConfiguration.1.AssociatedDevice.{i} (stations from "iw station dump")
// Optional lab-only simulated hosts are appended after the real ones and flagged
// with X_LAB_Simulated=true; they never touch the radio, DHCP or firewall.

import (
	"bufio"
	"fmt"
	"lab-router/internal/network"
	"lab-router/internal/system"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	LANDev         = Root + "LANDevice.1."
	LeaseFile      = "/var/lib/lab-router-dhcp/leases"
	MaxSimulated   = 16
	simulatedStore = "simulated_hosts"
)

type Station struct {
	MAC       string
	Signal    int
	TxBitrate string // e.g. "866.7 MBit/s"
	Connected int
}

type Host struct {
	MAC          string `json:"mac"`
	IP           string `json:"ip"`
	Hostname     string `json:"hostname"`
	LeaseExpires int64  `json:"lease_expires"` // unix; 0 = infinite
	Active       bool   `json:"active"`
	Wireless     bool   `json:"wireless"`
	Signal       int    `json:"signal"`
	Bitrate      string `json:"bitrate"`
	Simulated    bool   `json:"simulated"`
}

// ParseStations parses `iw dev <if> station dump`.
func ParseStations(text string) []Station {
	out := []Station{}
	var cur *Station
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "Station ") {
			f := strings.Fields(l)
			if len(f) >= 2 {
				out = append(out, Station{MAC: strings.ToLower(f[1])})
				cur = &out[len(out)-1]
			}
			continue
		}
		k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
		if cur == nil || !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "signal":
			cur.Signal, _ = strconv.Atoi(strings.Fields(v + " 0")[0])
		case "tx bitrate":
			if f := strings.Fields(v); len(f) >= 2 {
				cur.TxBitrate = f[0] + " " + f[1]
			}
		case "connected time":
			cur.Connected, _ = strconv.Atoi(strings.Fields(v + " 0")[0])
		}
	}
	return out
}

func readStations() []Station {
	v, ok := network.Inspect().(map[string]any)
	if !ok {
		return nil
	}
	text, _ := v["clients"].(string)
	return ParseStations(text)
}

// SimulatedCount returns how many lab-only simulated hosts are configured.
func (m Model) SimulatedCount() int {
	var n int
	m.Service.Store.Get(simulatedStore, &n)
	return max(0, min(n, MaxSimulated))
}
func (m Model) SetSimulatedCount(n int) error {
	if n < 0 || n > MaxSimulated {
		return fmt.Errorf("hosts simulados: 0–%d", MaxSimulated)
	}
	return m.Service.Store.Put(simulatedStore, n)
}

var simProfiles = []struct {
	name   string
	signal int
	rate   string
}{{"sim-notebook", -48, "866.7 MBit/s"}, {"sim-smartphone", -57, "433.3 MBit/s"}, {"sim-smarttv", -63, "200.0 MBit/s"}, {"sim-tablet", -71, "65.0 MBit/s"}}

// simulatedHosts picks deterministic MACs (locally administered 02:5a:b0:…) and
// free IPs from the top of the DHCP pool, skipping addresses already leased.
func simulatedHosts(n int, end string, used map[string]bool) []Host {
	out := []Host{}
	ip := net.ParseIP(end).To4()
	for i := 0; i < n && ip != nil; i++ {
		for used[ip.String()] {
			ip = prevIP(ip)
		}
		p := simProfiles[i%len(simProfiles)]
		out = append(out, Host{MAC: fmt.Sprintf("02:5a:b0:00:00:%02x", i+1), IP: ip.String(), Hostname: fmt.Sprintf("%s-%d", p.name, i+1), Active: true, Wireless: true, Signal: p.signal, Bitrate: p.rate, Simulated: true})
		used[ip.String()] = true
		ip = prevIP(ip)
	}
	return out
}
func prevIP(ip net.IP) net.IP {
	n := append(net.IP(nil), ip...)
	for i := len(n) - 1; i >= 0; i-- {
		n[i]--
		if n[i] != 255 {
			break
		}
	}
	return n
}

// Hosts merges DHCP leases with associated stations, then appends simulated hosts.
func (m Model) Hosts() []Host {
	c, _ := m.Service.Store.Config()
	stations := map[string]Station{}
	readS := m.ReadStations
	if readS == nil {
		readS = readStations
	}
	for _, s := range readS() {
		stations[s.MAC] = s
	}
	readL := m.ReadLeases
	if readL == nil {
		readL = func() []system.Lease { return system.Leases(LeaseFile) }
	}
	out := []Host{}
	seen, used := map[string]bool{}, map[string]bool{}
	for _, l := range readL() {
		mac := strings.ToLower(l.MAC)
		s, assoc := stations[mac]
		name := l.Hostname
		if name == "*" {
			name = ""
		}
		out = append(out, Host{MAC: mac, IP: l.IP, Hostname: name, LeaseExpires: l.Expires, Active: assoc, Wireless: assoc, Signal: s.Signal, Bitrate: s.TxBitrate})
		seen[mac], used[l.IP] = true, true
	}
	for mac, s := range stations { // associated but without a lease (static IP or pending DHCP)
		if !seen[mac] {
			out = append(out, Host{MAC: mac, Active: true, Wireless: true, Signal: s.Signal, Bitrate: s.TxBitrate})
		}
	}
	if gw, _, ok := strings.Cut(c.LAN.CIDR, "/"); ok {
		used[gw] = true
	}
	return append(out, simulatedHosts(m.SimulatedCount(), c.DHCP.End, used)...)
}

// kbps converts "866.7 MBit/s" to "866700" (TR-098 LastDataTransmitRate is in kbps).
func kbps(rate string) string {
	f := strings.Fields(rate)
	if len(f) == 0 {
		return "0"
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return strconv.Itoa(int(v * 1000))
}
func (m Model) hostParameters(add func(n, t, v string, w bool)) {
	hosts := m.Hosts()
	b := strconv.FormatBool
	assoc := 0
	for i, h := range hosts {
		p := fmt.Sprintf("%sHosts.Host.%d.", LANDev, i+1)
		source, remaining := "DHCP", "0"
		if h.IP == "" {
			source = "Static"
		} else if h.LeaseExpires == 0 {
			remaining = "-1" // TR-098: -1 = infinite lease
		} else {
			remaining = strconv.FormatInt(max(0, h.LeaseExpires-time.Now().Unix()), 10)
		}
		if h.Simulated {
			remaining = "-1"
		}
		ifType, l2 := "Ethernet", ""
		if h.Wireless {
			ifType, l2 = "802.11", LANDev+"WLANConfiguration.1"
		}
		add(p+"MACAddress", "string", h.MAC, false)
		add(p+"IPAddress", "string", h.IP, false)
		add(p+"HostName", "string", h.Hostname, false)
		add(p+"AddressSource", "string", source, false)
		add(p+"LeaseTimeRemaining", "int", remaining, false)
		add(p+"InterfaceType", "string", ifType, false)
		add(p+"Layer2Interface", "string", l2, false)
		add(p+"Active", "boolean", b(h.Active), false)
		add(p+"X_LAB_Simulated", "boolean", b(h.Simulated), false)
		if h.Wireless && h.Active {
			assoc++
			a := fmt.Sprintf("%sAssociatedDevice.%d.", WLAN, assoc)
			add(a+"AssociatedDeviceMACAddress", "string", h.MAC, false)
			add(a+"AssociatedDeviceIPAddress", "string", h.IP, false)
			add(a+"AssociatedDeviceAuthenticationState", "boolean", "true", false)
			add(a+"LastDataTransmitRate", "string", kbps(h.Bitrate), false)
			add(a+"X_LAB_SignalStrength", "int", strconv.Itoa(h.Signal), false)
			add(a+"X_LAB_Simulated", "boolean", b(h.Simulated), false)
		}
	}
	add(LANDev+"Hosts.HostNumberOfEntries", "unsignedInt", strconv.Itoa(len(hosts)), false)
	add(WLAN+"TotalAssociations", "unsignedInt", strconv.Itoa(assoc), false)
}
