package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"lab-router/internal/model"
	"lab-router/internal/system"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
)

const Socket = "/run/lab-router-helper/helper.sock"

type Helper struct {
	mu        sync.Mutex
	LastError string
}

func call(name string, args ...string) error {
	_, e := system.Run(context.Background(), name, args...)
	if e != nil {
		return fmt.Errorf("falha em %s (saída suprimida para proteger credenciais)", name)
	}
	return nil
}
func atomic(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".pending-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	f.Chmod(0600)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	f.Close()
	if e != nil {
		return e
	}
	return os.Rename(tmp, path)
}
func ownNFT() ([]byte, error) {
	var b bytes.Buffer
	for _, v := range [][]string{{"inet", "lab_router"}, {"ip", "lab_router_nat"}} {
		s, e := system.Run(context.Background(), "nft", append([]string{"list", "table"}, v...)...)
		if e == nil {
			if !strings.Contains(s, "Owned by LAB Router") {
				return nil, fmt.Errorf("tabela homônima sem identificação LAB Router")
			}
			b.WriteString(s)
			b.WriteByte('\n')
		}
	}
	return b.Bytes(), nil
}
func nftBatch(body []byte) []byte {
	var b bytes.Buffer
	for _, v := range [][]string{{"inet", "lab_router"}, {"ip", "lab_router_nat"}} {
		_, e := system.Run(context.Background(), "nft", append([]string{"list", "table"}, v...)...)
		if e == nil {
			fmt.Fprintf(&b, "delete table %s %s\n", v[0], v[1])
		}
	}
	b.Write(body)
	return b.Bytes()
}
func applyNFT(body []byte) error {
	path := Dir + "/.transaction.nft"
	if e := atomic(path, nftBatch(body)); e != nil {
		return e
	}
	if e := call("nft", "-c", "-f", path); e != nil {
		return e
	}
	return call("nft", "-f", path)
}
func blockedRoutes() []string {
	raw, _ := system.Run(context.Background(), "ip", "-j", "-4", "route", "show", "table", "all")
	var rows []struct {
		Dst string `json:"dst"`
		Dev string `json:"dev"`
	}
	json.Unmarshal([]byte(raw), &rows)
	out := []string{}
	for _, r := range rows {
		if r.Dst == "default" || r.Dst == "" || r.Dev == system.AP || r.Dev == "lo" {
			continue
		}
		s := r.Dst
		if !strings.Contains(s, "/") {
			s += "/32"
		}
		out = append(out, s)
	}
	return out
}
func (h *Helper) Apply(c model.Config) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, e := os.Stat(Dir + "/authorized"); e != nil {
		return fmt.Errorf("rede não autorizada pelo instalador")
	}
	if e := ValidateEnvironment(c); e != nil {
		return e
	}
	// Never take over an unmanaged hostapd. No killall, pkill, global firewall flush or WAN changes.
	procs, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	managed := system.Service("lab-router-ap.service") == "active"
	for _, p := range procs {
		b, _ := os.ReadFile(p)
		f := strings.Split(string(b), "\x00")
		if len(f) > 0 && filepath.Base(f[0]) == "hostapd" && !managed {
			return fmt.Errorf("hostapd existente: migração manual necessária")
		}
	}
	oldNFT, e := ownNFT()
	if e != nil {
		return e
	}
	var old model.Config
	oldBytes, readErr := os.ReadFile(Dir + "/config.json")
	hasOld := readErr == nil && json.Unmarshal(oldBytes, &old) == nil
	if !hasOld {
		ifs, _ := net.InterfaceByName(system.AP)
		if ifs != nil {
			aa, _ := ifs.Addrs()
			for _, a := range aa {
				if strings.Contains(a.String(), ".") {
					return fmt.Errorf("AP já tem IPv4 não gerenciado; migração manual necessária")
				}
			}
		}
	}
	// Refuse existing later base-chain drop policies: accepting in our chain cannot override them.
	raw, _ := system.Run(context.Background(), "nft", "-j", "list", "ruleset")
	var rules struct {
		NFT []map[string]json.RawMessage `json:"nftables"`
	}
	json.Unmarshal([]byte(raw), &rules)
	for _, r := range rules.NFT {
		if b := r["chain"]; b != nil {
			var ch struct{ Table, Hook, Policy string }
			json.Unmarshal(b, &ch)
			if ch.Hook == "forward" && ch.Policy == "drop" && ch.Table != "lab_router" {
				return fmt.Errorf("firewall existente possui policy drop em forward (%s); precisa de integração autorizada", ch.Table)
			}
		}
	}
	files := map[string][]byte{"hostapd.conf": []byte(Hostapd(c)), "dnsmasq.conf": []byte(Dnsmasq(c)), "deny.mac": []byte(strings.Join(c.WiFi.DenyMAC, "\n") + "\n")}
	before := map[string][]byte{}
	for name := range files {
		b, _ := os.ReadFile(Dir + "/" + name)
		before[name] = b
	}
	wasAP := system.Service("lab-router-ap.service") == "active"
	wasDHCP := system.Service("lab-router-dhcp.service") == "active"
	rollback := func(cause error) error {
		errs := []error{cause}
		for n, b := range before {
			if b != nil {
				errs = append(errs, atomic(Dir+"/"+n, b))
			} else {
				os.Remove(Dir + "/" + n)
			}
		}
		errs = append(errs, applyNFT(oldNFT))
		call("systemctl", "stop", "lab-router-ap.service", "lab-router-dhcp.service")
		if !hasOld || old.LAN.CIDR != c.LAN.CIDR {
			call("ip", "addr", "del", c.LAN.CIDR, "dev", system.AP)
		}
		if hasOld {
			call("ip", "addr", "replace", old.LAN.CIDR, "dev", system.AP)
		}
		if wasAP {
			errs = append(errs, call("systemctl", "start", "lab-router-ap.service"))
		}
		if wasDHCP {
			errs = append(errs, call("systemctl", "start", "lab-router-dhcp.service"))
		}
		return errors.Join(errs...)
	}
	for n, b := range files {
		if e := atomic(Dir+"/"+n, b); e != nil {
			return rollback(e)
		}
	}
	if e := call("dnsmasq", "--test", "--conf-file="+Dir+"/dnsmasq.conf"); e != nil {
		return rollback(e)
	}
	if e := applyNFT([]byte(Firewall(c, blockedRoutes()))); e != nil {
		return rollback(e)
	}
	if system.Read("/proc/sys/net/ipv4/ip_forward") != "1" {
		return rollback(fmt.Errorf("IPv4 forwarding desativado; habilite somente após revisar conflitos no instalador"))
	}
	if e := call("ip", "addr", "replace", c.LAN.CIDR, "dev", system.AP); e != nil {
		return rollback(e)
	}
	if hasOld && old.LAN.CIDR != c.LAN.CIDR {
		if e := call("ip", "addr", "del", old.LAN.CIDR, "dev", system.AP); e != nil {
			return rollback(e)
		}
	}
	restartAP := !hasOld || !reflect.DeepEqual(old.WiFi, c.WiFi) || !wasAP
	if !c.WiFi.Enabled {
		e = call("systemctl", "stop", "lab-router-ap.service")
	} else if restartAP {
		e = call("systemctl", "restart", "lab-router-ap.service")
	}
	if e != nil {
		return rollback(e)
	}
	if c.DHCP.Enabled && c.WiFi.Enabled {
		e = call("systemctl", "restart", "lab-router-dhcp.service")
	} else {
		e = call("systemctl", "stop", "lab-router-dhcp.service")
	}
	if e != nil {
		return rollback(e)
	}
	if c.WiFi.Enabled {
		ok := false
		for i := 0; i < 15; i++ {
			s, _ := system.Run(context.Background(), "hostapd_cli", "-s", filepath.Dir(Socket), "-p", "/run/lab-router-hostapd", "-i", system.AP, "status")
			a := system.WirelessInfo(system.AP)
			w := system.WirelessInfo(system.WAN)
			if strings.Contains(s, "state=ENABLED") && a.SSID == c.WiFi.SSID && a.Channel == c.WiFi.Channel && w.Channel == a.Channel {
				ok = true
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		if !ok {
			return rollback(fmt.Errorf("AP não confirmou SSID/canal; rollback executado"))
		}
	}
	if e := applyPPPoE(old, hasOld, c); e != nil {
		return rollback(e)
	}
	if hasOld {
		if e := atomic(Dir+"/config.previous.json", oldBytes); e != nil {
			return rollback(e)
		}
	}
	b, _ := json.Marshal(c)
	if e := atomic(Dir+"/config.json", b); e != nil {
		return rollback(e)
	}
	atomic(Dir+"/firewall.nft", []byte(Firewall(c, blockedRoutes())))
	return nil
}
func Client() *http.Client {
	return &http.Client{Timeout: 45 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", Socket)
	}}}
}
func Apply(c model.Config) error {
	b, _ := json.Marshal(c)
	r, e := Client().Post("http://helper/apply", "application/json", bytes.NewReader(b))
	if e != nil {
		return fmt.Errorf("helper indisponível: execute o instalador e autorize a rede")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		return fmt.Errorf("%s", strings.TrimSpace(string(b)))
	}
	return nil
}
func Inspect() any {
	r, e := Client().Get("http://helper/status")
	if e != nil {
		return map[string]any{"available": false, "reason": "helper não instalado; rede não alterada"}
	}
	defer r.Body.Close()
	var v any
	json.NewDecoder(r.Body).Decode(&v)
	return v
}
func Reboot() error {
	r, e := Client().Post("http://helper/reboot", "application/json", nil)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("reboot lógico falhou")
	}
	return nil
}

// Maintain isolation if WAN channel or corporate routes change while running.
func (h *Helper) watch(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	lastRules := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		h.mu.Lock()
		b, e := os.ReadFile(Dir + "/config.json")
		var c model.Config
		if e != nil || json.Unmarshal(b, &c) != nil {
			h.mu.Unlock()
			continue
		}
		wan := system.WirelessInfo(system.WAN)
		if wan.Channel == 0 || wan.Channel != c.WiFi.Channel {
			call("systemctl", "stop", "lab-router-ap.service", "lab-router-dhcp.service")
			h.LastError = "WAN mudou de canal ou desconectou; AP parado para preservar a WAN. Reaplique a configuração no canal atual."
			h.mu.Unlock()
			continue
		}
		rules := Firewall(c, blockedRoutes())
		if rules != lastRules {
			if _, e = ownNFT(); e == nil {
				e = applyNFT([]byte(rules))
			}
			if e != nil {
				h.LastError = "Atualização do isolamento falhou; AP e DHCP parados"
				call("systemctl", "stop", "lab-router-ap.service", "lab-router-dhcp.service")
			} else {
				lastRules = rules
				atomic(Dir+"/firewall.nft", []byte(rules))
			}
		}
		h.mu.Unlock()
	}
}
func Serve() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("helper requer root")
	}
	os.MkdirAll(Dir, 0700)
	os.MkdirAll(filepath.Dir(Socket), 0750)
	os.Remove(Socket)
	l, e := net.Listen("unix", Socket)
	if e != nil {
		return e
	}
	os.Chmod(Socket, 0660)
	h := &Helper{}
	go h.watch(context.Background())
	mux := http.NewServeMux()
	mux.HandleFunc("/apply", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "método inválido", 405)
			return
		}
		var c model.Config
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		d.DisallowUnknownFields()
		if e := d.Decode(&c); e != nil {
			http.Error(w, "configuração recusada pelo helper ("+e.Error()+"); se o binário foi atualizado, reinicie lab-router-helper", 400)
			return
		}
		if e := h.Apply(c); e != nil {
			h.mu.Lock()
			h.LastError = e.Error()
			h.mu.Unlock()
			http.Error(w, e.Error(), 409)
			return
		}
		h.mu.Lock()
		h.LastError = ""
		h.mu.Unlock()
		io.WriteString(w, "ok")
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		clients, ce := system.Run(r.Context(), "iw", "dev", system.AP, "station", "dump")
		status, _ := system.Run(r.Context(), "hostapd_cli", "-s", filepath.Dir(Socket), "-p", "/run/lab-router-hostapd", "-i", system.AP, "status")
		nft, ne := ownNFT()
		var current model.Config
		if b, e := os.ReadFile(Dir + "/config.json"); e == nil {
			json.Unmarshal(b, &current)
		}
		json.NewEncoder(w).Encode(map[string]any{"available": true, "error": h.LastError, "clients": clients, "clients_available": ce == nil, "hostapd": status, "firewall": string(nft), "firewall_available": ne == nil && len(nft) > 0, "pppoe": ReadPPPoEStatus(current)})
	})
	mux.HandleFunc("/reboot", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "método inválido", 405)
			return
		}
		b, e := os.ReadFile(Dir + "/config.json")
		var c model.Config
		if e != nil || json.Unmarshal(b, &c) != nil {
			http.Error(w, "rede não instalada", 409)
			return
		}
		h.mu.Lock()
		if c.WiFi.Enabled {
			e = call("systemctl", "restart", "lab-router-ap.service")
		}
		if e == nil && c.WiFi.Enabled && c.DHCP.Enabled {
			e = call("systemctl", "restart", "lab-router-dhcp.service")
		}
		h.mu.Unlock()
		if e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		io.WriteString(w, "ok")
	})
	// Restore only this application's network on boot. Fail closed on any conflict.
	if b, e := os.ReadFile(Dir + "/config.json"); e == nil {
		var c model.Config
		if json.Unmarshal(b, &c) == nil {
			if e = h.Apply(c); e != nil {
				fmt.Fprintln(os.Stderr, "restauração da rede recusada:", e)
			}
		}
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 60 * time.Second}
	return server.Serve(l)
}
