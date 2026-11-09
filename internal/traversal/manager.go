package traversal

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"lab-router/internal/model"
	"lab-router/internal/store"
	"net"
	"sync"
	"time"
)

type Status struct {
	State          string    `json:"state"`
	Connected      bool      `json:"connected"`
	Authenticated  bool      `json:"authenticated"`
	Authentication string    `json:"authentication"`
	TLS            bool      `json:"tls"`
	LocalAddress   string    `json:"local_address"`
	RemoteAddress  string    `json:"remote_address"`
	JID            string    `json:"jid"`
	MappedAddress  string    `json:"mapped_address"`
	NATDetected    bool      `json:"nat_detected"`
	LastConnected  time.Time `json:"last_connected"`
	LastAuth       time.Time `json:"last_auth"`
	LastRx         time.Time `json:"last_rx"`
	LastEvent      string    `json:"last_event"`
	LastError      string    `json:"last_error"`
	Received       int       `json:"received"`
	Accepted       int       `json:"accepted"`
	Rejected       int       `json:"rejected"`
	LastRequestID  string    `json:"last_request_id"`
	Updated        time.Time `json:"updated"`
}
type Manager struct {
	Store     *store.Store
	Request   func(string, string) (string, error)
	LocalIP   net.IP
	TLSConfig *tls.Config
	mu        sync.Mutex
	states    map[string]Status
}

func New(s *store.Store, request func(string, string) (string, error)) *Manager {
	return &Manager{Store: s, Request: request, states: map[string]Status{}}
}
func (m *Manager) update(kind string, edit func(*Status)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.states[kind]
	edit(&s)
	s.Updated = time.Now().UTC()
	m.states[kind] = s
	m.Store.Put(kind+"_status", s)
}
func (m *Manager) event(kind, action, result string) {
	m.update(kind, func(s *Status) { s.LastEvent = action })
	m.Store.Record("mechanisms", kind, action, result)
}
func (m *Manager) receive(kind, peer string, valid bool, reason string) (string, error) {
	m.update(kind, func(s *Status) {
		s.LastRx = time.Now().UTC()
		s.Received++
		if !valid {
			s.Rejected++
		}
	})
	if !valid {
		m.event(kind, "connection_request.rejected", reason)
		return "", fmt.Errorf("%s", reason)
	}
	id, e := m.Request(kind, peer)
	if e != nil {
		m.update(kind, func(s *Status) { s.Rejected++ })
		m.event(kind, "connection_request.rejected", "CWMP desativado ou fila indisponível")
		return "", e
	}
	m.update(kind, func(s *Status) { s.Accepted++; s.LastRequestID = id })
	m.event(kind, "connection_request.accepted", "autenticado e enfileirado; aguarde resultado da sessão CWMP")
	return id, nil
}
func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func fingerprint(c model.CWMP) [32]byte { b, _ := json.Marshal(c); return sha256.Sum256(b) }
func (m *Manager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, kind := range []string{"xmpp", "stun"} {
		kind := kind
		wg.Add(1)
		go func() { defer wg.Done(); m.supervise(ctx, kind) }()
	}
	wg.Wait()
}
func (m *Manager) supervise(ctx context.Context, kind string) {
	for ctx.Err() == nil {
		c, e := m.Store.Config()
		if e != nil {
			if !wait(ctx, 2*time.Second) {
				return
			}
			continue
		}
		enabled := c.CWMP.XMPPEnable
		if kind == "stun" {
			enabled = c.CWMP.STUNEnable
		}
		if !enabled {
			m.update(kind, func(s *Status) {
				s.State = "disabled"
				s.Connected = false
				s.Authenticated = false
				s.Authentication = "disabled"
			})
			if !wait(ctx, 2*time.Second) {
				return
			}
			continue
		}
		var reconnect string
		m.Store.Get(kind+"_reconnect", &reconnect)
		child, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			if kind == "stun" {
				done <- m.runSTUN(child, c.CWMP)
			} else {
				done <- m.runXMPP(child, c.CWMP)
			}
		}()
		ticker := time.NewTicker(2 * time.Second)
		changed := false
		var runErr error
	monitor:
		for {
			select {
			case <-ctx.Done():
				cancel()
				<-done
				ticker.Stop()
				return
			case runErr = <-done:
				break monitor
			case <-ticker.C:
				n, e := m.Store.Config()
				var newReconnect string
				m.Store.Get(kind+"_reconnect", &newReconnect)
				if e == nil && (fingerprint(n.CWMP) != fingerprint(c.CWMP) || newReconnect != reconnect) {
					changed = true
					cancel()
					<-done
					break monitor
				}
			}
		}
		ticker.Stop()
		cancel()
		if changed {
			continue
		}
		if runErr != nil {
			m.update(kind, func(s *Status) {
				s.State = "error"
				s.Connected = false
				s.Authenticated = false
				s.TLS = false
				s.LastError = runErr.Error()
			})
			m.event(kind, "transport.error", runErr.Error())
		}
		if !wait(ctx, 10*time.Second) {
			return
		}
	}
}
