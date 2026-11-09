package cwmp

// TR-098 WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1 backed by the lab
// PPPoE dialer (status from the helper; config from model.Config.PPPoE).

import (
	"encoding/json"
	"lab-router/internal/model"
	"lab-router/internal/network"
	"strconv"
)

const WANPPP = Root + "WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1."

func readPPPoE() network.PPPoEStatus {
	var s network.PPPoEStatus
	v, ok := network.Inspect().(map[string]any)
	if !ok {
		return network.PPPoEStatus{State: "Disconnected", LastError: "ERROR_UNKNOWN", Detail: "helper indisponível"}
	}
	b, _ := json.Marshal(v["pppoe"])
	if json.Unmarshal(b, &s) != nil || s.State == "" {
		s.State, s.LastError = "Unconfigured", "ERROR_NONE"
	}
	return s
}

func (m Model) pppParameters(c model.Config, add func(n, t, v string, w bool), secret func(string)) {
	read := m.ReadPPPoE
	if read == nil {
		read = readPPPoE
	}
	s := read()
	b, u := strconv.FormatBool, func(v uint64) string { return strconv.FormatUint(v, 10) }
	mru := c.PPPoE.MRU
	if mru == 0 {
		mru = 1492
	}
	dns := ""
	if s.State == "Connected" {
		dns = network.BRASAddress // ms-dns announced by the simulated BRAS
	}
	add(Root+"WANDevice.1.WANConnectionDevice.1.WANPPPConnectionNumberOfEntries", "unsignedInt", "1", false)
	add(Root+"WANDevice.1.WANConnectionDevice.1.WANIPConnectionNumberOfEntries", "unsignedInt", "1", false)
	add(WANPPP+"Enable", "boolean", b(c.PPPoE.Enabled), true)
	add(WANPPP+"Name", "string", "LAB PPPoE (BRAS simulado)", false)
	add(WANPPP+"ConnectionStatus", "string", s.State, false)
	add(WANPPP+"PossibleConnectionTypes", "string", "IP_Routed", false)
	add(WANPPP+"ConnectionType", "string", "IP_Routed", false)
	add(WANPPP+"ConnectionTrigger", "string", "AlwaysOn", false)
	add(WANPPP+"TransportType", "string", "PPPoE", false)
	add(WANPPP+"NATEnabled", "boolean", "false", false)
	add(WANPPP+"Uptime", "unsignedInt", strconv.FormatInt(s.UptimeSeconds, 10), false)
	add(WANPPP+"LastConnectionError", "string", s.LastError, false)
	add(WANPPP+"Username", "string", c.PPPoE.Username, true)
	secret(WANPPP + "Password")
	add(WANPPP+"ExternalIPAddress", "string", s.LocalIP, false)
	add(WANPPP+"RemoteIPAddress", "string", s.RemoteIP, false)
	add(WANPPP+"DNSServers", "string", dns, false)
	add(WANPPP+"MaxMRUSize", "unsignedInt", strconv.Itoa(mru), true)
	add(WANPPP+"CurrentMRUSize", "unsignedInt", strconv.Itoa(s.MRU), false)
	add(WANPPP+"MACAddress", "string", s.MAC, false)
	add(WANPPP+"PPPoEACName", "string", s.ACName, false)
	add(WANPPP+"PPPoEServiceName", "string", c.PPPoE.ServiceName, true)
	add(WANPPP+"Stats.EthernetBytesReceived", "unsignedLong", u(s.RxBytes), false)
	add(WANPPP+"Stats.EthernetBytesSent", "unsignedLong", u(s.TxBytes), false)
	add(WANPPP+"Stats.EthernetPacketsReceived", "unsignedLong", u(s.RxPackets), false)
	add(WANPPP+"Stats.EthernetPacketsSent", "unsignedLong", u(s.TxPackets), false)
}
