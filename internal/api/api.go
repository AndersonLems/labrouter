package api

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/crypto/bcrypt"
	"io/fs"
	"lab-router/internal/cwmp"
	"lab-router/internal/model"
	"lab-router/internal/network"
	"lab-router/internal/service"
	"lab-router/internal/store"
	"lab-router/internal/system"
	"lab-router/internal/traversal"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed ui
var assets embed.FS

type session struct {
	CSRF    string
	Expires time.Time
}
type API struct {
	Service    *service.ConfigService
	mu         sync.Mutex
	sessions   map[string]session
	loginTimes []time.Time
	Leases     string
}

func New(s *service.ConfigService) *API {
	return &API{Service: s, sessions: map[string]session{}, Leases: "/var/lib/lab-router-dhcp/leases"}
}
func hash(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, e error) {
	write(w, status, map[string]string{"error": e.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func (a *API) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, e := r.Cookie("lab_session")
		if e != nil {
			fail(w, 401, fmt.Errorf("autenticação necessária"))
			return
		}
		a.mu.Lock()
		s, ok := a.sessions[hash(cookie.Value)]
		if ok && time.Now().After(s.Expires) {
			delete(a.sessions, hash(cookie.Value))
			ok = false
		}
		a.mu.Unlock()
		if !ok {
			fail(w, 401, fmt.Errorf("sessão expirada"))
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("X-CSRF-Token") != s.CSRF {
			fail(w, 403, fmt.Errorf("CSRF inválido"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, e := net.SplitHostPort(r.Host)
			if e != nil {
				host = r.Host
			}
			if host != "127.0.0.1" && host != "localhost" && host != "::1" {
				fail(w, 403, fmt.Errorf("host não autorizado"))
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
				fail(w, 403, fmt.Errorf("origem não autorizada"))
				return
			}
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'")
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
		})
	})
	r.Post("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		recent := []time.Time{}
		for _, t := range a.loginTimes {
			if time.Since(t) < time.Minute {
				recent = append(recent, t)
			}
		}
		limited := len(recent) >= 5
		a.loginTimes = append(recent, time.Now())
		a.mu.Unlock()
		if limited {
			fail(w, 429, fmt.Errorf("aguarde um minuto antes de tentar novamente"))
			return
		}
		var v struct {
			Password string `json:"password"`
		}
		if decode(w, r, &v) != nil || !a.Service.Store.Authenticate(v.Password) {
			a.Service.Store.Record("audit", "local", "login", "negado")
			fail(w, 401, fmt.Errorf("credencial inválida"))
			return
		}
		token := model.Token()
		csrf := model.Token()
		a.mu.Lock()
		for k, s := range a.sessions {
			if time.Now().After(s.Expires) {
				delete(a.sessions, k)
			}
		}
		a.sessions[hash(token)] = session{csrf, time.Now().Add(8 * time.Hour)}
		a.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "lab_session", Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 28800})
		write(w, 200, map[string]string{"csrf": csrf})
		a.Service.Store.Record("audit", "admin", "login", "sucesso")
	})
	r.Group(func(r chi.Router) {
		r.Use(a.auth)
		r.Get("/api/auth/session", func(w http.ResponseWriter, r *http.Request) {
			c, _ := r.Cookie("lab_session")
			a.mu.Lock()
			s := a.sessions[hash(c.Value)]
			a.mu.Unlock()
			write(w, 200, map[string]string{"csrf": s.CSRF})
		})
		r.Post("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
			c, _ := r.Cookie("lab_session")
			a.mu.Lock()
			delete(a.sessions, hash(c.Value))
			a.mu.Unlock()
			http.SetCookie(w, &http.Cookie{Name: "lab_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
			write(w, 200, map[string]bool{"ok": true})
		})
		r.Put("/api/auth/password", func(w http.ResponseWriter, r *http.Request) {
			var v struct {
				Current  string `json:"current"`
				Password string `json:"password"`
			}
			if decode(w, r, &v) != nil || len(v.Password) < 12 || len(v.Password) > 72 || !a.Service.Store.Authenticate(v.Current) {
				fail(w, 400, fmt.Errorf("senha atual inválida ou nova senha fora de 12–72 bytes"))
				return
			}
			h, e := bcrypt.GenerateFromPassword([]byte(v.Password), 12)
			if e == nil {
				e = a.Service.Store.Put("admin", string(h))
			}
			if e != nil {
				fail(w, 500, fmt.Errorf("falha ao persistir senha"))
				return
			}
			a.mu.Lock()
			a.sessions = map[string]session{}
			a.mu.Unlock()
			a.Service.Store.Record("audit", "admin", "password.change", "sucesso; sessões revogadas")
			write(w, 200, map[string]bool{"ok": true})
		})
		r.Get("/api/dashboard", func(w http.ResponseWriter, r *http.Request) { write(w, 200, a.snapshot()) })
		r.Get("/api/system/status", func(w http.ResponseWriter, r *http.Request) { write(w, 200, a.snapshot()) })
		r.Get("/api/interfaces", func(w http.ResponseWriter, r *http.Request) { write(w, 200, system.Interfaces()) })
		r.Get("/api/wan", func(w http.ResponseWriter, r *http.Request) {
			write(w, 200, map[string]any{"wireless": system.WirelessInfo(system.WAN), "interfaces": system.Interfaces(), "routes": system.Routes(), "dns": system.Read("/etc/resolv.conf"), "editable": false, "reason": "WAN protegida"})
		})
		r.Get("/api/wifi/radios", func(w http.ResponseWriter, r *http.Request) { write(w, 200, system.Radios()) })
		r.Get("/api/wifi/clients", func(w http.ResponseWriter, r *http.Request) { write(w, 200, network.Inspect()) })
		r.Get("/api/dhcp/leases", func(w http.ResponseWriter, r *http.Request) { write(w, 200, system.Leases(a.Leases)) })
		for _, path := range []string{"config", "wifi", "wifi/ssids", "lan", "dhcp", "dhcp/reservations", "nat", "firewall", "tr069/config", "pppoe", "bras"} {
			p := path
			r.Get("/api/"+p, func(w http.ResponseWriter, r *http.Request) {
				c, e := a.Service.Store.Config()
				if e != nil {
					fail(w, 500, fmt.Errorf("banco indisponível"))
					return
				}
				write(w, 200, part(c.Redacted(), p))
			})
			r.Put("/api/"+p, func(w http.ResponseWriter, r *http.Request) {
				var raw json.RawMessage
				if e := decode(w, r, &raw); e != nil {
					fail(w, 400, fmt.Errorf("JSON inválido"))
					return
				}
				e := a.Service.Update(r.Context(), "admin", func(c *model.Config) error {
					old := *c
					target := target(c, p)
					d := json.NewDecoder(strings.NewReader(string(raw)))
					d.DisallowUnknownFields()
					if e := d.Decode(target); e != nil {
						return fmt.Errorf("campos inválidos")
					}
					c.PreserveSecrets(old)
					return nil
				})
				if e != nil {
					fail(w, 409, e)
					return
				}
				write(w, 200, map[string]bool{"ok": true})
			})
		}
		r.Get("/api/tr069/diagnostics/{kind}", func(w http.ResponseWriter, r *http.Request) {
			kind := chi.URLParam(r, "kind")
			if kind != "basic" && kind != "periodic" && kind != "http" && kind != "xmpp" && kind != "stun" {
				fail(w, 404, fmt.Errorf("mecanismo desconhecido"))
				return
			}
			config, e := a.Service.Store.Config()
			if e != nil {
				fail(w, 500, fmt.Errorf("configuração indisponível"))
				return
			}
			var state cwmp.Status
			a.Service.Store.Get("cwmp_status", &state)
			events := []store.Record{}
			for _, v := range a.Service.Store.Records("mechanisms") {
				if v.Actor == kind {
					events = append(events, v)
				}
			}
			var status traversal.Status
			if kind == "stun" || kind == "xmpp" {
				a.Service.Store.Get(kind+"_status", &status)
			}
			enabled := config.CWMP.Enabled
			if kind == "stun" {
				enabled = config.CWMP.STUNEnable
			}
			if kind == "xmpp" {
				enabled = config.CWMP.XMPPEnable
			}
			if !enabled {
				status.State = "disabled"
				status.Connected = false
				status.Authenticated = false
			}
			protocolConfig := map[string]any{"enabled": enabled, "acs_url": config.CWMP.URL, "periodic_enable": config.CWMP.PeriodicEnable, "interval": config.CWMP.Interval, "connection_request_credentials_configured": func() bool { u, p := config.CWMP.ConnReq(); return u != "" && p != "" }(), "connection_request_credentials_source": map[bool]string{true: "própria", false: "CWMP"}[config.CWMP.CRUsername != "" && config.CWMP.CRPassword != ""]}
			if kind == "stun" {
				protocolConfig["server"] = config.CWMP.STUNServer
				protocolConfig["port"] = config.CWMP.STUNPort
				protocolConfig["mode"] = config.CWMP.STUNMode
				protocolConfig["keepalive"] = config.CWMP.STUNKeepAlive
			}
			if kind == "xmpp" {
				protocolConfig["server"] = config.CWMP.XMPPServer
				protocolConfig["port"] = config.CWMP.XMPPPort
				protocolConfig["domain"] = config.CWMP.XMPPDomain
				protocolConfig["allowed_jids"] = config.CWMP.XMPPAllowed
				protocolConfig["insecure_tls"] = config.CWMP.XMPPInsecureTLS
			}
			write(w, 200, map[string]any{"mechanism": kind, "config": protocolConfig, "status": status, "cwmp": state, "requests": a.Service.Store.Requests(kind), "events": events, "sessions": a.Service.Store.Records("sessions")})
		})
		r.Post("/api/tr069/{kind}/reconnect", func(w http.ResponseWriter, r *http.Request) {
			kind := chi.URLParam(r, "kind")
			if kind != "stun" && kind != "xmpp" {
				fail(w, 404, fmt.Errorf("mecanismo desconhecido"))
				return
			}
			c, e := a.Service.Store.Config()
			if e != nil || kind == "stun" && !c.CWMP.STUNEnable || kind == "xmpp" && !c.CWMP.XMPPEnable {
				fail(w, 409, fmt.Errorf("ative e configure o mecanismo primeiro"))
				return
			}
			if e = a.Service.Store.Put(kind+"_reconnect", model.Token()); e != nil {
				fail(w, 500, fmt.Errorf("fila indisponível"))
				return
			}
			write(w, 202, map[string]string{"status": "reconexão solicitada; consulte o resultado real"})
		})
		r.Get("/api/tr069/status", func(w http.ResponseWriter, r *http.Request) {
			var v cwmp.Status
			if a.Service.Store.Get("cwmp_status", &v) != nil {
				write(w, 200, map[string]string{"state": "agente não iniciado", "stun": "Não implementado", "xmpp": "Não implementado"})
				return
			}
			c, _ := a.Service.Store.Config()
			if !c.CWMP.Enabled {
				v.State = "disabled"
			}
			write(w, 200, v)
		})
		r.Get("/api/tr069/parameters", func(w http.ResponseWriter, r *http.Request) {
			ps, e := (cwmp.Model{Service: a.Service}).Parameters()
			if e != nil {
				fail(w, 500, fmt.Errorf("parâmetros indisponíveis"))
				return
			}
			write(w, 200, ps)
		})
		r.Get("/api/tr069/sessions", func(w http.ResponseWriter, r *http.Request) { write(w, 200, a.Service.Store.Records("sessions")) })
		r.Post("/api/tr069/inform", func(w http.ResponseWriter, r *http.Request) {
			c, e := a.Service.Store.Config()
			if e != nil || !c.CWMP.Enabled {
				fail(w, 409, fmt.Errorf("ative CWMP primeiro"))
				return
			}
			a.Service.Store.Event("4 VALUE CHANGE", "")
			if e = a.Service.Store.Put("inform_request", model.Token()); e != nil {
				fail(w, 500, fmt.Errorf("fila indisponível"))
				return
			}
			a.Service.Store.Record("audit", "admin", "cwmp.inform", "enfileirado; aguarde confirmação SOAP no histórico")
			write(w, 202, map[string]string{"status": "enfileirado"})
		})
		r.Get("/api/tr069/diagnostics", func(w http.ResponseWriter, r *http.Request) {
			c, _ := a.Service.Store.Config()
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			write(w, 200, cwmp.Diagnose(ctx, c.CWMP.URL))
		})
		for _, kind := range []string{"ping", "traceroute", "dns"} {
			k := kind
			r.Post("/api/diagnostics/"+k, func(w http.ResponseWriter, r *http.Request) {
				var v struct {
					Target string `json:"target"`
				}
				if decode(w, r, &v) != nil {
					fail(w, 400, fmt.Errorf("destino inválido"))
					return
				}
				out, e := system.Diagnose(r.Context(), k, v.Target)
				if e != nil {
					write(w, 422, map[string]any{"error": "diagnóstico retornou falha", "output": out})
					return
				}
				write(w, 200, map[string]any{"output": out})
			})
		}
		r.Post("/api/diagnostics/speedtest", func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
			defer cancel()
			res, e := system.Speedtest(ctx)
			if e != nil {
				status := 502
				if e == system.ErrSpeedBusy {
					status = 409
				}
				a.Service.Store.Record("audit", "admin", "diagnostics.speedtest", "falha: "+e.Error())
				fail(w, status, e)
				return
			}
			a.Service.Store.Record("audit", "admin", "diagnostics.speedtest", fmt.Sprintf("sucesso; %.1f/%.1f Mbps; %.0f ms", res.DownloadMbps, res.UploadMbps, res.LatencyMs))
			write(w, 200, res)
		})
		r.Get("/api/lab/hosts", func(w http.ResponseWriter, r *http.Request) {
			m := cwmp.Model{Service: a.Service, ReadLeases: func() []system.Lease { return system.Leases(a.Leases) }}
			write(w, 200, map[string]any{"simulated_count": m.SimulatedCount(), "max_simulated": cwmp.MaxSimulated, "hosts": m.Hosts()})
		})
		r.Put("/api/lab/simulated-hosts", func(w http.ResponseWriter, r *http.Request) {
			var v struct {
				Count int `json:"count"`
			}
			if decode(w, r, &v) != nil {
				fail(w, 400, fmt.Errorf("JSON inválido"))
				return
			}
			m := cwmp.Model{Service: a.Service}
			if e := m.SetSimulatedCount(v.Count); e != nil {
				fail(w, 422, e)
				return
			}
			a.Service.Store.Record("audit", "admin", "lab.simulated_hosts", fmt.Sprintf("sucesso; %d hosts simulados", v.Count))
			a.Service.Store.Event("4 VALUE CHANGE", "") // prompt an Inform so the ACS refreshes the host list
			write(w, 200, map[string]int{"count": v.Count})
		})
		r.Get("/api/logs", func(w http.ResponseWriter, r *http.Request) { write(w, 200, a.Service.Store.Records("audit")) })
		r.Get("/api/backups", func(w http.ResponseWriter, r *http.Request) { write(w, 200, a.Service.Store.Backups()) })
		r.Post("/api/backups", func(w http.ResponseWriter, r *http.Request) {
			id, e := a.Service.Store.Backup()
			if e != nil {
				fail(w, 500, fmt.Errorf("backup falhou"))
				return
			}
			a.Service.Store.Record("audit", "admin", "backup.create", "sucesso")
			write(w, 201, map[string]string{"id": id})
		})
		r.Post("/api/backups/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
			c, e := a.Service.Store.LoadBackup(chi.URLParam(r, "id"))
			if e != nil {
				fail(w, 404, fmt.Errorf("backup não encontrado"))
				return
			}
			e = a.Service.Update(r.Context(), "admin:restore", func(current *model.Config) error { *current = c; return nil })
			if e != nil {
				fail(w, 409, e)
				return
			}
			write(w, 200, map[string]bool{"ok": true})
		})
		r.Post("/api/system/reboot", func(w http.ResponseWriter, r *http.Request) {
			if !a.Service.Live {
				fail(w, 409, fmt.Errorf("modo observação"))
				return
			}
			if e := network.Reboot(); e != nil {
				fail(w, 409, fmt.Errorf("reboot lógico falhou"))
				return
			}
			a.Service.Store.Event("1 BOOT", "")
			a.Service.Store.Record("audit", "admin", "logical.reboot", "sucesso")
			write(w, 200, map[string]bool{"ok": true})
		})
		r.Put("/api/tr069/factory-reset-policy", func(w http.ResponseWriter, r *http.Request) {
			var v struct {
				Allowed bool `json:"allowed"`
			}
			if decode(w, r, &v) != nil {
				fail(w, 400, fmt.Errorf("JSON inválido"))
				return
			}
			if e := a.Service.Store.Put("allow_factory_reset", v.Allowed); e != nil {
				fail(w, 500, fmt.Errorf("falha ao persistir política"))
				return
			}
			a.Service.Store.Record("audit", "admin", "factory_reset.policy", fmt.Sprintf("allowed=%t", v.Allowed))
			write(w, 200, v)
		})
		r.Get("/api/events", func(w http.ResponseWriter, r *http.Request) {
			f, ok := w.(http.Flusher)
			if !ok {
				return
			}
			http.NewResponseController(w).SetWriteDeadline(time.Time{})
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("X-Accel-Buffering", "no")
			t := time.NewTicker(3 * time.Second)
			defer t.Stop()
			for {
				b, _ := json.Marshal(a.snapshot())
				if _, e := fmt.Fprintf(w, "data: %s\n\n", b); e != nil {
					return
				}
				f.Flush()
				select {
				case <-r.Context().Done():
					return
				case <-t.C:
				}
				cookie, e := r.Cookie("lab_session")
				if e != nil {
					return
				}
				a.mu.Lock()
				s, ok := a.sessions[hash(cookie.Value)]
				a.mu.Unlock()
				if !ok || time.Now().After(s.Expires) {
					return
				}
			}
		})
	})
	r.Handle("/api/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fail(w, 404, fmt.Errorf("endpoint não implementado")) }))
	sub, _ := fs.Sub(assets, "ui")
	r.Handle("/*", http.FileServer(http.FS(sub)))
	return r
}
func (a *API) snapshot() map[string]any {
	s := system.Snapshot(a.Leases)
	s["mode"] = "observation"
	if a.Service.Live {
		s["mode"] = "live"
	}
	s["network"] = network.Inspect()
	var c cwmp.Status
	if a.Service.Store.Get("cwmp_status", &c) == nil {
		config, _ := a.Service.Store.Config()
		if !config.CWMP.Enabled {
			c.State = "disabled"
		}
		s["cwmp"] = c
	}
	return s
}
func part(c model.Config, p string) any {
	switch p {
	case "config":
		return c
	case "wifi", "wifi/ssids":
		return c.WiFi
	case "lan":
		return c.LAN
	case "dhcp":
		return c.DHCP
	case "dhcp/reservations":
		return c.DHCP.Reservations
	case "firewall", "nat":
		return c.Firewall
	case "tr069/config":
		return c.CWMP
	case "pppoe":
		return c.PPPoE
	case "bras":
		return c.BRAS
	}
	return nil
}
func target(c *model.Config, p string) any {
	switch p {
	case "config":
		return c
	case "wifi", "wifi/ssids":
		return &c.WiFi
	case "lan":
		return &c.LAN
	case "dhcp":
		return &c.DHCP
	case "dhcp/reservations":
		return &c.DHCP.Reservations
	case "firewall", "nat":
		return &c.Firewall
	case "tr069/config":
		return &c.CWMP
	case "pppoe":
		return &c.PPPoE
	case "bras":
		return &c.BRAS
	}
	return nil
}
