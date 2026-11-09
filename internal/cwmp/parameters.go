package cwmp

import (
	"context"
	"fmt"
	"lab-router/internal/model"
	"lab-router/internal/network"
	"lab-router/internal/service"
	"lab-router/internal/system"
	"sort"
	"strconv"
	"strings"
)

const Root = "InternetGatewayDevice."
const WLAN = Root + "LANDevice.1.WLANConfiguration.1."
const MS = Root + "ManagementServer."

type Parameter struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Value    string `json:"value"`
	Writable bool   `json:"writable"`
	Secret   bool   `json:"secret,omitempty"`
}
type Fault struct {
	Code      int
	Message   string
	Parameter string
}

func (f *Fault) Error() string { return fmt.Sprintf("CWMP %d: %s", f.Code, f.Message) }

type Model struct {
	Service      *service.ConfigService
	ReadWireless func(string) system.Wireless
	ReadStations func() []Station      // nil: helper "iw station dump"
	ReadLeases   func() []system.Lease // nil: dnsmasq lease file
	ReadPPPoE    func() network.PPPoEStatus
}

func (m Model) Parameters() ([]Parameter, error) {
	c, e := m.Service.Store.Config()
	if e != nil {
		return nil, e
	}
	reader := m.ReadWireless
	if reader == nil {
		reader = system.WirelessInfo
	}
	a := reader(system.AP)
	ssid := c.WiFi.SSID
	if a.SSID != "" {
		ssid = a.SSID
	}
	out := []Parameter{}
	add := func(n, t, v string, w bool) { out = append(out, Parameter{n, t, v, w, false}) }
	secret := func(n string) { out = append(out, Parameter{n, "string", "", true, true}) }
	b := strconv.FormatBool
	i := strconv.Itoa
	add(Root+"DeviceInfo.Manufacturer", "string", c.CWMP.Manufacturer, false)
	add(Root+"DeviceInfo.ManufacturerOUI", "string", c.CWMP.OUI, false)
	add(Root+"DeviceInfo.ProductClass", "string", c.CWMP.ProductClass, false)
	add(Root+"DeviceInfo.SerialNumber", "string", c.CWMP.Serial, false)
	add(Root+"DeviceInfo.SoftwareVersion", "string", "0.1.0", false)
	add(Root+"DeviceInfo.HardwareVersion", "string", "Linux", false)
	up := strings.Fields(system.Read("/proc/uptime"))
	if len(up) > 0 {
		seconds, _, _ := strings.Cut(up[0], ".")
		add(Root+"DeviceInfo.UpTime", "unsignedInt", seconds, false)
	}
	add(Root+"DeviceInfo.ProcessStatus.CPUUsage", "unsignedInt", strconv.Itoa(CPUUsage()), false)
	memTotal, memFree := MemoryKiB()
	add(Root+"DeviceInfo.MemoryStatus.Total", "unsignedInt", strconv.FormatUint(memTotal, 10), false)
	add(Root+"DeviceInfo.MemoryStatus.Free", "unsignedInt", strconv.FormatUint(memFree, 10), false)
	var mapped string
	var nat bool
	m.Service.Store.Get("udp_address", &mapped)
	m.Service.Store.Get("nat_detected", &nat)
	var xmppStatus struct {
		JID string `json:"jid"`
	}
	m.Service.Store.Get("xmpp_status", &xmppStatus)
	add(MS+"UDPConnectionRequestAddress", "string", mapped, false)
	add(MS+"NATDetected", "boolean", b(nat), false)
	add(MS+"STUNEnable", "boolean", b(c.CWMP.STUNEnable), true)
	add(MS+"STUNServerAddress", "string", c.CWMP.STUNServer, true)
	add(MS+"STUNServerPort", "unsignedInt", i(c.CWMP.STUNPort), true)
	add(MS+"STUNUsername", "string", c.CWMP.STUNUsername, true)
	secret(MS + "STUNPassword")
	add(MS+"ConnReqJabberID", "string", xmppStatus.JID, false)
	add(MS+"ConnReqAllowedJabberIDs", "string", strings.Join(c.CWMP.XMPPAllowed, ","), true)
	add(MS+"URL", "string", c.CWMP.URL, true)
	add(MS+"Username", "string", c.CWMP.Username, true)
	secret(MS + "Password")
	add(MS+"PeriodicInformEnable", "boolean", b(c.CWMP.PeriodicEnable), true)
	add(MS+"PeriodicInformInterval", "unsignedInt", i(c.CWMP.Interval), true)
	add(MS+"ConnectionRequestURL", "string", c.CWMP.CRURL, false)
	crUser, _ := c.CWMP.ConnReq()
	add(MS+"ConnectionRequestUsername", "string", crUser, true)
	secret(MS + "ConnectionRequestPassword")
	key := c.ParameterKey
	add(MS+"ParameterKey", "string", key, false)
	add(WLAN+"SSID", "string", ssid, true)
	add(WLAN+"Enable", "boolean", b(c.WiFi.Enabled), true)
	add(WLAN+"Status", "string", func() string {
		if a.SSID != "" {
			return "Up"
		}
		return "Disabled"
	}(), false)
	add(WLAN+"Channel", "unsignedInt", i(c.WiFi.Channel), true)
	add(WLAN+"BSSID", "string", system.Read("/sys/class/net/ap-tr069/address"), false)
	add(WLAN+"BeaconType", "string", func() string {
		if c.WiFi.Security == "wpa2" {
			return "11i"
		}
		return ""
	}(), false)
	add(WLAN+"IEEE11iEncryptionModes", "string", "AESEncryption", false)
	add(WLAN+"IEEE11iAuthenticationMode", "string", "PSKAuthentication", false)
	secret(WLAN + "PreSharedKey.1.KeyPassphrase")
	add(WLAN+"X_LAB_SecurityMode", "string", c.WiFi.Security, true)
	add(WLAN+"X_LAB_APIsolation", "boolean", b(c.WiFi.Isolation), true)
	add(Root+"LANDevice.1.LANHostConfigManagement.IPRouters", "string", strings.Split(c.LAN.CIDR, "/")[0], false)
	add(Root+"LANDevice.1.LANHostConfigManagement.DHCPServerEnable", "boolean", b(c.DHCP.Enabled), true)
	add(Root+"LANDevice.1.LANHostConfigManagement.MinAddress", "string", c.DHCP.Start, true)
	add(Root+"LANDevice.1.LANHostConfigManagement.MaxAddress", "string", c.DHCP.End, true)
	add(Root+"WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ConnectionStatus", "string", func() string {
		if system.WirelessInfo(system.WAN).SSID != "" {
			return "Connected"
		}
		return "Disconnected"
	}(), false)
	m.tr143Parameters(add)
	m.hostParameters(add)
	counterParameters(add)
	m.pppParameters(c, add, secret)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func boolean(v string) (bool, error) {
	switch v {
	case "1", "true":
		return true, nil
	case "0", "false":
		return false, nil
	}
	return false, fmt.Errorf("boolean inválido")
}

// describeSet summarizes an SPV for the session history. Lab tool: values,
// including passwords/passphrases, are recorded in clear text on purpose so the
// operator can verify exactly what the ACS sent.
func describeSet(values []Parameter) string {
	parts := make([]string, 0, len(values))
	for _, p := range values {
		name, v := strings.TrimPrefix(p.Name, Root), p.Value
		if len(v) > 80 {
			v = v[:80] + "…"
		}
		parts = append(parts, name+"="+v)
	}
	return strings.Join(parts, "; ")
}
func (m Model) Set(ctx context.Context, values []Parameter, key string) error {
	m.Service.Store.Record("sessions", "ACS", "SetParameterValues.parametros", describeSet(values))
	params, e := m.Parameters()
	if e != nil {
		return e
	}
	known := map[string]Parameter{}
	for _, p := range params {
		known[p.Name] = p
	}
	seen := map[string]bool{}
	for _, p := range values {
		k, ok := known[p.Name]
		if !ok {
			return &Fault{9005, "Invalid parameter name", p.Name}
		}
		if !k.Writable {
			return &Fault{9008, "Attempt to set a non-writable parameter", p.Name}
		}
		if seen[p.Name] {
			return &Fault{9003, "Duplicate parameter", p.Name}
		}
		seen[p.Name] = true
		if p.Type != "" && p.Type != k.Type {
			return &Fault{9006, "Invalid parameter type", p.Name}
		}
	}
	// TR-143 diagnostics live outside the config: validate them first, persist
	// only after the config part commits, so a failing SPV changes nothing.
	var cfgValues, diagValues []Parameter
	for _, p := range values {
		if isTR143(p.Name) {
			diagValues = append(diagValues, p)
		} else {
			cfgValues = append(cfgValues, p)
		}
	}
	diag, e := m.prepareTR143(diagValues)
	if e != nil {
		return e
	}
	e = m.Service.Update(ctx, "ACS", func(c *model.Config) error {
		c.ParameterKey = key
		for _, p := range cfgValues {
			var err error
			switch p.Name {
			case WLAN + "SSID":
				c.WiFi.SSID = p.Value
			case WLAN + "Enable":
				c.WiFi.Enabled, err = boolean(p.Value)
			case WLAN + "Channel":
				c.WiFi.Channel, err = strconv.Atoi(p.Value)
			case WLAN + "PreSharedKey.1.KeyPassphrase":
				c.WiFi.Password = p.Value
			case WLAN + "X_LAB_SecurityMode":
				c.WiFi.Security = p.Value
			case WLAN + "X_LAB_APIsolation":
				c.WiFi.Isolation, err = boolean(p.Value)
			case MS + "STUNEnable":
				c.CWMP.STUNEnable, err = boolean(p.Value)
			case MS + "STUNServerAddress":
				c.CWMP.STUNServer = p.Value
			case MS + "STUNServerPort":
				c.CWMP.STUNPort, err = strconv.Atoi(p.Value)
			case MS + "STUNUsername":
				c.CWMP.STUNUsername = p.Value
			case MS + "STUNPassword":
				c.CWMP.STUNPassword = p.Value
			case MS + "ConnReqAllowedJabberIDs":
				c.CWMP.XMPPAllowed = nil
				for _, v := range strings.Split(p.Value, ",") {
					if strings.TrimSpace(v) != "" {
						c.CWMP.XMPPAllowed = append(c.CWMP.XMPPAllowed, strings.TrimSpace(v))
					}
				}
			case MS + "URL":
				c.CWMP.URL = p.Value
			case MS + "Username":
				c.CWMP.Username = p.Value
			case MS + "Password":
				c.CWMP.Password = p.Value
			case MS + "PeriodicInformEnable":
				c.CWMP.PeriodicEnable, err = boolean(p.Value)
			case MS + "PeriodicInformInterval":
				c.CWMP.Interval, err = strconv.Atoi(p.Value)
			case MS + "ConnectionRequestUsername":
				c.CWMP.CRUsername = p.Value
			case MS + "ConnectionRequestPassword":
				c.CWMP.CRPassword = p.Value
			case WANPPP + "Enable":
				c.PPPoE.Enabled, err = boolean(p.Value)
			case WANPPP + "Username":
				c.PPPoE.Username = p.Value
			case WANPPP + "Password":
				c.PPPoE.Password = p.Value
			case WANPPP + "PPPoEServiceName":
				c.PPPoE.ServiceName = p.Value
			case WANPPP + "MaxMRUSize":
				c.PPPoE.MRU, err = strconv.Atoi(p.Value)
			case Root + "LANDevice.1.LANHostConfigManagement.DHCPServerEnable":
				c.DHCP.Enabled, err = boolean(p.Value)
			case Root + "LANDevice.1.LANHostConfigManagement.MinAddress":
				c.DHCP.Start = p.Value
			case Root + "LANDevice.1.LANHostConfigManagement.MaxAddress":
				c.DHCP.End = p.Value
			}
			if err != nil {
				return &Fault{9007, "Invalid parameter value", p.Name}
			}
		}
		if err := model.Validate(*c); err != nil {
			return &Fault{9007, "Invalid parameter value: " + err.Error(), ""}
		}
		return nil
	})
	if e != nil {
		if _, ok := e.(*Fault); ok {
			return e
		}
		return &Fault{9002, "Network apply failed; configuration not committed: " + e.Error(), ""}
	}
	for kind, d := range diag {
		m.putDiag(kind, d)
	}
	return nil
}
