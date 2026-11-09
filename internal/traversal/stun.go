package traversal

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"lab-router/internal/model"
	"lab-router/internal/system"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const cookie uint32 = 0x2112a442

type stunAttr struct {
	kind   uint16
	value  []byte
	offset int
}
type stunMessage struct {
	kind  uint16
	tx    [16]byte
	attrs []stunAttr
	raw   []byte
}

func attr(kind uint16, value []byte) []byte {
	b := make([]byte, 4+(len(value)+3)/4*4)
	binary.BigEndian.PutUint16(b, kind)
	binary.BigEndian.PutUint16(b[2:], uint16(len(value)))
	copy(b[4:], value)
	return b
}
func parseSTUN(b []byte) (stunMessage, error) {
	var m stunMessage
	if len(b) < 20 || len(b) > 4096 || int(binary.BigEndian.Uint16(b[2:])) != len(b)-20 || len(b)%4 != 0 {
		return m, fmt.Errorf("STUN inválido")
	}
	m.kind = binary.BigEndian.Uint16(b)
	copy(m.tx[:], b[4:20])
	m.raw = append([]byte(nil), b...)
	for off := 20; off < len(b); {
		if off+4 > len(b) {
			return m, fmt.Errorf("atributo STUN truncado")
		}
		n := int(binary.BigEndian.Uint16(b[off+2:]))
		if off+4+n > len(b) {
			return m, fmt.Errorf("atributo STUN truncado")
		}
		m.attrs = append(m.attrs, stunAttr{binary.BigEndian.Uint16(b[off:]), append([]byte(nil), b[off+4:off+4+n]...), off})
		off += 4 + (n+3)/4*4
	}
	return m, nil
}
func (m stunMessage) get(kind uint16) []byte {
	for _, a := range m.attrs {
		if a.kind == kind {
			return a.value
		}
	}
	return nil
}
func integrity(b, key []byte, modern bool) []byte {
	message := append([]byte(nil), b...)
	if !modern {
		for len(message)%64 != 0 {
			message = append(message, 0)
		}
	}
	h := hmac.New(sha1.New, key)
	h.Write(message)
	return h.Sum(nil)
}
func signSTUN(b, key []byte, modern bool) []byte {
	binary.BigEndian.PutUint16(b[2:], uint16(len(b)-20+24))
	sig := integrity(b, key, modern)
	return append(b, attr(8, sig)...)
}
func verifySTUN(m stunMessage, key []byte, modern bool) bool {
	for _, a := range m.attrs {
		if a.kind == 8 && len(a.value) == 20 {
			b := append([]byte(nil), m.raw[:a.offset]...)
			binary.BigEndian.PutUint16(b[2:], uint16(a.offset-20+24))
			return hmac.Equal(a.value, integrity(b, key, modern))
		}
	}
	return false
}
func requestSTUN(modern, change bool, username, realm, nonce string, key []byte) ([]byte, [16]byte) {
	var tx [16]byte
	if _, e := rand.Read(tx[:]); e != nil {
		panic(e)
	}
	if modern {
		binary.BigEndian.PutUint32(tx[:4], cookie)
	}
	b := make([]byte, 20)
	binary.BigEndian.PutUint16(b, 1)
	copy(b[4:], tx[:])
	b = append(b, attr(0xc001, []byte("dslforum.org/TR-111 "))...)
	if change {
		b = append(b, attr(0xc002, nil)...)
	}
	if username != "" {
		if !modern {
			for len(username)%4 != 0 {
				username += " "
			}
		}
		b = append(b, attr(6, []byte(username))...)
	}
	if realm != "" {
		b = append(b, attr(0x0014, []byte(realm))...)
	}
	if nonce != "" {
		b = append(b, attr(0x0015, []byte(nonce))...)
	}
	binary.BigEndian.PutUint16(b[2:], uint16(len(b)-20))
	if len(key) > 0 {
		b = signSTUN(b, key, modern)
	}
	return b, tx
}
func mappedSTUN(m stunMessage) (string, error) {
	v := m.get(0x0020)
	xor := len(v) > 0
	if !xor {
		v = m.get(1)
	}
	if len(v) != 8 || v[1] != 1 {
		return "", fmt.Errorf("resposta STUN sem mapeamento IPv4")
	}
	port := binary.BigEndian.Uint16(v[2:4])
	ip := append(net.IP(nil), v[4:8]...)
	if xor {
		if binary.BigEndian.Uint32(m.tx[:4]) != cookie {
			return "", fmt.Errorf("XOR-MAPPED-ADDRESS incompatível")
		}
		port ^= uint16(cookie >> 16)
		var mask [4]byte
		binary.BigEndian.PutUint32(mask[:], cookie)
		for i := range ip {
			ip[i] ^= mask[i]
		}
	}
	if port == 0 || !ip.IsGlobalUnicast() && !ip.IsLoopback() {
		return "", fmt.Errorf("endereço STUN inválido")
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(int(port))), nil
}

type replay struct {
	Timestamp int64  `json:"timestamp"`
	ID        uint64 `json:"id"`
}

func validateUDP(b []byte, c model.CWMP, last replay) (replay, error) {
	bad := fmt.Errorf("UDP Connection Request inválido, repetido ou sem autenticação")
	if len(b) > 8192 {
		return last, bad
	}
	r, e := http.ReadRequest(bufio.NewReader(bytes.NewReader(b)))
	if e != nil {
		return last, bad
	}
	defer r.Body.Close()
	if r.Method != "GET" || r.Proto != "HTTP/1.1" || r.URL.Scheme != "http" || r.URL.Host == "" || r.URL.Path != "" || r.URL.User != nil || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		return last, bad
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil || len(q) != 5 {
		return last, bad
	}
	for _, k := range []string{"ts", "id", "un", "cn", "sig"} {
		if len(q[k]) != 1 {
			return last, bad
		}
	}
	ts, e := strconv.ParseInt(q["ts"][0], 10, 64)
	if e != nil || ts <= last.Timestamp {
		return last, bad
	}
	id, e := strconv.ParseUint(q["id"][0], 10, 32)
	if e != nil || id == last.ID && last.Timestamp != 0 {
		return last, bad
	}
	user, pass := c.ConnReq()
	if user == "" || pass == "" || !same(q["un"][0], user) || q["cn"][0] == "" {
		return last, bad
	}
	sig, e := hex.DecodeString(q["sig"][0])
	if e != nil || len(sig) != 20 {
		return last, bad
	}
	h := hmac.New(sha1.New, []byte(pass))
	h.Write([]byte(q["ts"][0] + q["id"][0] + q["un"][0] + q["cn"][0]))
	if !hmac.Equal(sig, h.Sum(nil)) {
		return last, bad
	}
	return replay{ts, id}, nil
}
func (m *Manager) udpRequest(b []byte, c model.CWMP, peer string) {
	var last replay
	m.Store.Get("udp_replay", &last)
	next, e := validateUDP(b, c, last)
	if e != nil {
		m.receive("stun", peer, false, "assinatura, formato ou antirreplay UDP inválidos")
		return
	}
	if e = m.Store.Put("udp_replay", next); e != nil {
		m.receive("stun", peer, false, "falha ao persistir proteção antirreplay")
		return
	}
	m.receive("stun", peer, true, "")
}
func wanIP() net.IP {
	i, e := net.InterfaceByName(system.WAN)
	if e != nil {
		return nil
	}
	addresses, _ := i.Addrs()
	for _, a := range addresses {
		ip, _, e := net.ParseCIDR(a.String())
		if e == nil && ip.To4() != nil {
			return ip.To4()
		}
	}
	return nil
}
func (m *Manager) runSTUN(ctx context.Context, c model.CWMP) error {
	m.update("stun", func(s *Status) {
		s.State = "resolving"
		s.Connected = false
		s.Authenticated = false
		s.Authentication = "not verified"
		s.LastError = ""
	})
	addresses, e := net.DefaultResolver.LookupIP(ctx, "ip4", c.STUNServer)
	if e != nil || len(addresses) == 0 {
		return fmt.Errorf("DNS STUN falhou")
	}
	server := &net.UDPAddr{IP: addresses[0].To4(), Port: c.STUNPort}
	local := wanIP()
	if m.LocalIP != nil {
		local = m.LocalIP
	}
	if local == nil {
		return fmt.Errorf("WAN sem IPv4; STUN não iniciado")
	}
	conn, e := net.ListenUDP("udp4", &net.UDPAddr{IP: local, Port: c.STUNLocalPort})
	if e != nil {
		return fmt.Errorf("bind UDP STUN falhou")
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	m.update("stun", func(s *Status) {
		s.State = "binding"
		s.LocalAddress = conn.LocalAddr().String()
		s.RemoteAddress = server.String()
	})
	modern := c.STUNMode == "rfc5389"
	interval := c.STUNKeepAlive
	if interval == 0 {
		interval = 30
	}
	var packet, key []byte
	var tx [16]byte
	realm, nonce := "", ""
	change := false
	attempts, challenges := 0, 0
	var deadline, next time.Time
	lastMapped := ""
	buf := make([]byte, 8193)
	send := func(auth bool) error {
		if !auth {
			key = nil
			realm = ""
			nonce = ""
			challenges = 0
		}
		packet, tx = requestSTUN(modern, change, c.STUNUsername, realm, nonce, key)
		attempts = 0
		if _, e = conn.WriteToUDP(packet, server); e != nil {
			return fmt.Errorf("envio STUN falhou")
		}
		deadline = time.Now().Add(500 * time.Millisecond)
		return nil
	}
	if e = send(false); e != nil {
		return e
	}
	for ctx.Err() == nil {
		conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, peer, readErr := conn.ReadFromUDP(buf)
		if readErr == nil {
			if !peer.IP.Equal(server.IP) {
				m.receive("stun", peer.String(), false, "origem UDP não corresponde ao servidor STUN")
				continue
			}
			if bytes.HasPrefix(buf[:n], []byte("GET ")) {
				m.udpRequest(buf[:n], c, peer.String())
				continue
			}
			response, pe := parseSTUN(buf[:n])
			if pe != nil || response.tx != tx || packet == nil {
				continue
			}
			if peer.Port != server.Port {
				continue
			}
			if response.kind == 0x0111 {
				code := response.get(9)
				number := 0
				if len(code) >= 4 {
					number = int(code[2]&7)*100 + int(code[3])
				}
				if (number == 401 || number == 438) && challenges < 2 && c.STUNUsername != "" && c.STUNPassword != "" {
					challenges++
					realm = string(response.get(0x0014))
					nonce = string(response.get(0x0015))
					key = []byte(c.STUNPassword)
					if modern && realm != "" {
						md := md5.Sum([]byte(c.STUNUsername + ":" + realm + ":" + c.STUNPassword))
						key = md[:]
					}
					m.update("stun", func(s *Status) { s.Authentication = "challenge received" })
					if e = send(true); e != nil {
						return e
					}
					continue
				}
				return fmt.Errorf("autenticação/protocolo STUN recusado (código %d)", number)
			}
			if response.kind != 0x0101 {
				continue
			}
			if len(key) > 0 && !verifySTUN(response, key, modern) {
				return fmt.Errorf("MESSAGE-INTEGRITY STUN inválido ou ausente")
			}
			mapped, pe := mappedSTUN(response)
			if pe != nil {
				return pe
			}
			m.update("stun", func(s *Status) {
				s.State = "mapped"
				s.Connected = true
				s.Authenticated = len(key) > 0
				s.Authentication = "server did not require authentication"
				if len(key) > 0 {
					s.Authentication = "MESSAGE-INTEGRITY verified"
					s.LastAuth = time.Now().UTC()
				}
				s.MappedAddress = mapped
				s.NATDetected = mapped != conn.LocalAddr().String()
				s.LastConnected = time.Now().UTC()
				s.LastError = ""
			})
			packet = nil
			deadline = time.Time{}
			next = time.Now().Add(time.Duration(interval) * time.Second)
			if mapped != lastMapped {
				lastMapped = mapped
				m.Store.Put("udp_address", mapped)
				m.Store.Put("nat_detected", mapped != conn.LocalAddr().String())
				m.Store.Event("4 VALUE CHANGE", "")
				m.event("stun", "binding.changed", "mapeamento externo atualizado")
				change = true
				if e = send(false); e != nil {
					return e
				}
			} else {
				change = false
			}
		} else if ne, ok := readErr.(net.Error); !ok || !ne.Timeout() {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("leitura UDP STUN falhou")
		}
		if packet != nil && time.Now().After(deadline) {
			attempts++
			if attempts >= 7 {
				return fmt.Errorf("timeout STUN sem resposta válida")
			}
			if _, e = conn.WriteToUDP(packet, server); e != nil {
				return fmt.Errorf("retransmissão STUN falhou")
			}
			deadline = time.Now().Add(time.Duration(1<<min(attempts, 3)) * 500 * time.Millisecond)
		}
		if packet == nil && !next.IsZero() && time.Now().After(next) {
			change = false
			if e = send(false); e != nil {
				return e
			}
		}
	}
	return nil
}
