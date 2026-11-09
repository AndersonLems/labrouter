package cwmp

import (
	"context"
	"lab-router/internal/model"
	"lab-router/internal/system"
	"strings"
	"testing"
)

const stationDump = `Station 9a:c6:d2:07:8a:12 (on ap-tr069)
	inactive time:	120 ms
	rx bytes:	12345
	signal:  	-52 [-52] dBm
	tx bitrate:	866.7 MBit/s VHT-MCS 9 80MHz short GI VHT-NSS 2
	connected time:	300 seconds
Station aa:bb:cc:00:00:01 (on ap-tr069)
	signal:  	-70 dBm
	tx bitrate:	65.0 MBit/s
`

func TestHostsRealAndSimulated(t *testing.T) {
	m := testModel(t)
	m.ReadStations = func() []Station { return ParseStations(stationDump) }
	m.ReadLeases = func() []system.Lease {
		return []system.Lease{{Expires: 0, MAC: "9A:C6:D2:07:8A:12", IP: "192.168.50.137", Hostname: "Nokia-1100"}, {Expires: 1, MAC: "11:22:33:44:55:66", IP: "192.168.50.200", Hostname: "*"}}
	}
	if e := m.SetSimulatedCount(4); e != nil {
		t.Fatal(e)
	}
	if m.SetSimulatedCount(MaxSimulated+1) == nil {
		t.Fatal("simulated count above limit accepted")
	}
	hosts := m.Hosts()
	// 2 leases + 1 station without lease + 4 simulated
	if len(hosts) != 7 {
		t.Fatalf("hosts: %d %+v", len(hosts), hosts)
	}
	if !hosts[0].Active || hosts[0].Signal != -52 || hosts[0].Hostname != "Nokia-1100" || hosts[1].Active {
		t.Fatalf("lease/station merge wrong: %+v", hosts[:2])
	}
	sim := hosts[3:]
	ips := map[string]bool{"192.168.50.137": true, "192.168.50.200": true, "192.168.50.1": true}
	for _, h := range sim {
		if !h.Simulated || !h.Active || h.MAC[:9] != "02:5a:b0:" || ips[h.IP] {
			t.Fatalf("simulated host invalid or IP collision: %+v", h)
		}
		ips[h.IP] = true
	}
	if param(t, m, LANDev+"Hosts.HostNumberOfEntries") != "7" || param(t, m, WLAN+"TotalAssociations") != "6" {
		t.Fatal("counters wrong")
	}
	if param(t, m, WLAN+"AssociatedDevice.1.LastDataTransmitRate") != "866700" || param(t, m, LANDev+"Hosts.Host.4.X_LAB_Simulated") != "true" || param(t, m, LANDev+"Hosts.Host.1.LeaseTimeRemaining") != "-1" {
		t.Fatal("host parameters wrong")
	}
	if e := m.SetSimulatedCount(0); e != nil || len(m.Hosts()) != 3 {
		t.Fatal("simulated hosts not removed")
	}
}

func TestInformCarriesGaugesAndCounters(t *testing.T) {
	m := testModel(t)
	c, _ := m.Service.Store.Config()
	xml := string(New(m).inform(c, nil))
	for _, want := range []string{"WANCommonInterfaceConfig.TotalBytesReceived", "WLANConfiguration.1.TotalBytesSent", "WLANConfiguration.1.TotalAssociations", "ProcessStatus.CPUUsage", "MemoryStatus.Free", "xsd:unsignedLong"} {
		if !strings.Contains(xml, want) {
			t.Fatalf("Inform missing %s", want)
		}
	}
}

func TestACSProvisionsPPPoE(t *testing.T) {
	m := testModel(t)
	applied := false
	m.Service.ApplyNetwork = func(model.Config) error { applied = true; return nil }
	e := m.Set(context.Background(), []Parameter{
		{Name: WANPPP + "Username", Type: "string", Value: "cliente@isp"},
		{Name: WANPPP + "Password", Type: "string", Value: "s3nha"},
		{Name: WANPPP + "Enable", Type: "boolean", Value: "true"},
	}, "pppoe")
	if e != nil {
		t.Fatal(e)
	}
	c, _ := m.Service.Store.Config()
	if !applied || !c.PPPoE.Enabled || c.PPPoE.Username != "cliente@isp" || c.PPPoE.Password != "s3nha" {
		t.Fatalf("PPPoE not provisioned/applied: %+v applied=%v", c.PPPoE, applied)
	}
	if param(t, m, WANPPP+"Username") != "cliente@isp" || param(t, m, WANPPP+"Password") != "" || param(t, m, WANPPP+"TransportType") != "PPPoE" {
		t.Fatal("WANPPPConnection parameters wrong (password must read back empty)")
	}
	if m.Set(context.Background(), []Parameter{{Name: WANPPP + "Password", Type: "string", Value: "has space"}}, "") == nil {
		t.Fatal("invalid PPPoE password accepted via ACS")
	}
}
