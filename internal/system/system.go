package system

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const WAN = "wlp2s0"
const AP = "ap-tr069"

func Run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	b, e := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(b)), e
}
func Read(path string) string { b, _ := os.ReadFile(path); return strings.TrimSpace(string(b)) }
func JSONCommand(name string, args ...string) any {
	b, e := Run(context.Background(), name, args...)
	if e != nil {
		return map[string]any{"available": false, "reason": "comando indisponível ou permissão insuficiente"}
	}
	var v any
	if json.Unmarshal([]byte(b), &v) != nil {
		return b
	}
	return v
}
func Interfaces() any { return JSONCommand("ip", "-j", "-s", "addr") }
func Routes() any     { return JSONCommand("ip", "-j", "route", "show", "table", "all") }

type Wireless struct {
	Interface string `json:"interface"`
	PHY       string `json:"phy"`
	SSID      string `json:"ssid"`
	Channel   int    `json:"channel"`
	Frequency int    `json:"frequency"`
	Info      string `json:"info"`
}

var chanRE = regexp.MustCompile(`channel (\d+) \((\d+)`)

func WirelessInfo(iface string) Wireless {
	info, _ := Run(context.Background(), "iw", "dev", iface, "info")
	w := Wireless{Interface: iface, Info: info}
	for _, l := range strings.Split(info, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "ssid ") {
			w.SSID = strings.TrimPrefix(l, "ssid ")
		}
		if strings.HasPrefix(l, "wiphy ") {
			w.PHY = "phy" + strings.TrimPrefix(l, "wiphy ")
		}
	}
	m := chanRE.FindStringSubmatch(info)
	if len(m) == 3 {
		w.Channel, _ = strconv.Atoi(m[1])
		w.Frequency, _ = strconv.Atoi(m[2])
	}
	return w
}
func Radios() []map[string]any {
	out := []map[string]any{}
	paths, _ := filepath.Glob("/sys/class/ieee80211/*")
	wan := WirelessInfo(WAN)
	for _, p := range paths {
		phy := filepath.Base(p)
		driver, _ := filepath.EvalSymlinks(filepath.Join(p, "device/driver"))
		info, e := Run(context.Background(), "iw", "phy", phy, "info")
		sim := strings.Contains(driver, "hwsim")
		out = append(out, map[string]any{"phy": phy, "driver": filepath.Base(driver), "simulated": sim, "ap_supported": e == nil && strings.Contains(info, "* AP\n"), "shared_wan": wan.PHY == phy, "locked_channel": wan.Channel, "frequency": wan.Frequency, "capabilities": info, "managed": phy == WirelessInfo(AP).PHY, "reason": func() string {
			if sim {
				return "mac80211_hwsim: rádio simulado, sem emissão física"
			}
			if wan.PHY == phy {
				return "STA+AP compartilham um canal; mudar banda/canal afetaria a WAN"
			}
			return "Rádio adicional detectado; provisionamento manual necessário nesta versão"
		}()})
	}
	return out
}

type Lease struct {
	Expires  int64  `json:"expires"`
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
}

func Leases(path string) []Lease {
	out := []Lease{}
	for _, l := range strings.Split(Read(path), "\n") {
		f := strings.Fields(l)
		if len(f) < 4 {
			continue
		}
		exp, _ := strconv.ParseInt(f[0], 10, 64)
		if exp != 0 && exp < time.Now().Unix() {
			continue
		}
		out = append(out, Lease{exp, f[1], f[2], f[3]})
	}
	return out
}
func Service(name string) string {
	s, e := Run(context.Background(), "systemctl", "is-active", name)
	if e != nil && s == "" {
		return "indisponível"
	}
	return s
}
func Snapshot(leasefile string) map[string]any {
	wan := WirelessInfo(WAN)
	ap := WirelessInfo(AP)
	cpu := strings.Fields(strings.Split(Read("/proc/stat"), "\n")[0])
	memory := map[string]uint64{}
	for _, l := range strings.Split(Read("/proc/meminfo"), "\n") {
		f := strings.Fields(l)
		if len(f) > 1 {
			v, _ := strconv.ParseUint(f[1], 10, 64)
			memory[strings.TrimSuffix(f[0], ":")] = v * 1024
		}
	}
	uptime := strings.Fields(Read("/proc/uptime"))
	up := ""
	if len(uptime) > 0 {
		up = uptime[0]
	}
	services := map[string]string{}
	for _, s := range []string{"api", "helper", "ap", "dhcp", "cwmp"} {
		services[s] = Service("lab-router-" + s + ".service")
	}
	return map[string]any{"time": time.Now().UTC(), "interfaces": Interfaces(), "routes": Routes(), "wan": wan, "ap": ap, "memory": memory, "cpu_ticks": cpu, "uptime_seconds": up, "services": services, "leases": Leases(leasefile), "ipv4_forwarding": Read("/proc/sys/net/ipv4/ip_forward"), "load": Read("/proc/loadavg")}
}
func Diagnose(ctx context.Context, kind, target string) (any, error) {
	if len(target) > 253 || strings.HasPrefix(target, "-") || !regexp.MustCompile(`^[a-zA-Z0-9.:_-]+$`).MatchString(target) {
		return nil, fmt.Errorf("destino inválido")
	}
	switch kind {
	case "ping":
		return Run(ctx, "ping", "-n", "-c", "3", "-W", "2", "--", target)
	case "traceroute":
		return Run(ctx, "traceroute", "-n", "-m", "12", "-w", "1", "--", target)
	case "dns":
		r := net.Resolver{}
		return r.LookupIPAddr(ctx, target)
	}
	return nil, fmt.Errorf("diagnóstico inválido")
}
