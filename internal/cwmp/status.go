package cwmp

// Host resource gauges (TR-098 Amendment 2: DeviceInfo.ProcessStatus / MemoryStatus).

import (
	"lab-router/internal/system"
	"strconv"
	"strings"
	"sync"
	"time"
)

type cpuSample struct {
	total, idle uint64
	at          time.Time
}

var (
	cpuMu   sync.Mutex
	cpuLast cpuSample
)

func readCPU() cpuSample {
	f := strings.Fields(strings.SplitN(system.Read("/proc/stat"), "\n", 2)[0])
	s := cpuSample{at: time.Now()}
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		if i < 8 { // user nice system idle iowait irq softirq steal
			s.total += n
		}
		if i == 3 || i == 4 {
			s.idle += n
		}
	}
	return s
}

// CPUUsage returns the CPU percentage since the previous call; if the last
// sample is too old or too recent it takes a short fresh 250 ms window.
func CPUUsage() int {
	cpuMu.Lock()
	defer cpuMu.Unlock()
	prev := cpuLast
	if prev.at.IsZero() || time.Since(prev.at) < 500*time.Millisecond || time.Since(prev.at) > time.Minute {
		prev = readCPU()
		time.Sleep(250 * time.Millisecond)
	}
	cur := readCPU()
	cpuLast = cur
	if cur.total <= prev.total {
		return 0
	}
	busy := float64((cur.total-prev.total)-(cur.idle-prev.idle)) / float64(cur.total-prev.total)
	return int(busy*100 + 0.5)
}

// ifStat reads a 64-bit interface counter (rx_bytes, tx_bytes, rx_packets, tx_packets).
func ifStat(iface, name string) string {
	v := system.Read("/sys/class/net/" + iface + "/statistics/" + name)
	if _, e := strconv.ParseUint(v, 10, 64); e != nil {
		return "0"
	}
	return v
}

// counterParameters reports WAN and lab WLAN traffic counters. TR-098 types them
// unsignedInt (wraps at 4 GiB); the full 64-bit value is sent as unsignedLong so
// ACS rate graphs do not spike on wrap.
func counterParameters(add func(n, t, v string, w bool)) {
	wanIf := Root + "WANDevice.1.WANCommonInterfaceConfig."
	wanIP := Root + "WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.Stats."
	for _, c := range []struct{ suffix, file string }{{"BytesReceived", "rx_bytes"}, {"BytesSent", "tx_bytes"}, {"PacketsReceived", "rx_packets"}, {"PacketsSent", "tx_packets"}} {
		add(wanIf+"Total"+c.suffix, "unsignedLong", ifStat(system.WAN, c.file), false)
		add(wanIP+"Ethernet"+c.suffix, "unsignedLong", ifStat(system.WAN, c.file), false)
		// WLAN counters are from the AP's point of view (Sent = towards clients).
		add(WLAN+"Total"+c.suffix, "unsignedLong", ifStat(system.AP, c.file), false)
	}
}

// informExtra lists the gauges/counters pushed in every Inform so the ACS
// collects a sample per periodic session without extra GetParameterValues.
var informExtra = map[string]bool{
	Root + "WANDevice.1.WANCommonInterfaceConfig.TotalBytesReceived": true,
	Root + "WANDevice.1.WANCommonInterfaceConfig.TotalBytesSent":     true,
	WLAN + "TotalBytesReceived":                                      true,
	WLAN + "TotalBytesSent":                                          true,
	WLAN + "TotalAssociations":                                       true,
	Root + "DeviceInfo.ProcessStatus.CPUUsage":                       true,
	Root + "DeviceInfo.MemoryStatus.Free":                            true,
}

// MemoryKiB returns MemTotal and MemAvailable from /proc/meminfo, in KiB.
func MemoryKiB() (total, free uint64) {
	for _, l := range strings.Split(system.Read("/proc/meminfo"), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = v
		case "MemAvailable:":
			free = v
		}
	}
	return
}
