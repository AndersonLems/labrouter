package traversal

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"lab-router/internal/cwmp"
	"lab-router/internal/model"
	"lab-router/internal/service"
	"lab-router/internal/store"
	"math/big"
	"mellium.im/sasl"
	"mellium.im/xmlstream"
	"mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func environment(t *testing.T) (*Manager, model.Config, *store.Store, context.Context) {
	t.Helper()
	dir := t.TempDir()
	s, e := store.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	s.Init("test-admin-pass")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); s.DB.Close() })
	acs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if len(b) == 0 {
			w.WriteHeader(204)
			return
		}
		_, rpc, e := cwmp.Parse(b)
		if e != nil || rpc.Name.Local != "Inform" {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/xml")
		w.Write(cwmp.Envelope("test", "<cwmp:InformResponse><MaxEnvelopes>1</MaxEnvelopes></cwmp:InformResponse>"))
	}))
	t.Cleanup(acs.Close)
	c, _ := s.Config()
	c.CWMP.Enabled = true
	c.CWMP.PeriodicEnable = false
	c.CWMP.URL = acs.URL
	c.CWMP.CRUsername = "cpe-request"
	c.CWMP.CRPassword = "secret-for-connection-request"
	s.Put("config", c)
	a := cwmp.New(cwmp.Model{Service: &service.ConfigService{Store: s, Dir: dir}})
	go a.Run(ctx)
	m := New(s, a.QueueRequest)
	m.LocalIP = net.IPv4(127, 0, 0, 1)
	return m, c, s, ctx
}
func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
func udpMessage(c model.CWMP, ts int64, id uint64, valid bool) []byte {
	q := url.Values{"ts": {fmt.Sprint(ts)}, "id": {fmt.Sprint(id)}, "un": {c.CRUsername}, "cn": {"nonce-one"}}
	h := hmac.New(sha1.New, []byte(c.CRPassword))
	h.Write([]byte(q.Get("ts") + q.Get("id") + q.Get("un") + q.Get("cn")))
	signature := h.Sum(nil)
	if !valid {
		signature[0] ^= 0xff
	}
	q.Set("sig", hex.EncodeToString(signature))
	return []byte("GET http://127.0.0.1:12345?" + q.Encode() + " HTTP/1.1\r\nHost: 127.0.0.1:12345\r\n\r\n")
}
func TestUDPReplayAndAuthentication(t *testing.T) {
	m, c, s, _ := environment(t)
	packet := udpMessage(c.CWMP, 1700000000, 100, true)
	m.udpRequest(packet, c.CWMP, "127.0.0.1:3478")
	m.udpRequest(packet, c.CWMP, "127.0.0.1:3478")
	m.udpRequest(udpMessage(c.CWMP, 1700000001, 101, false), c.CWMP, "127.0.0.1:3478")
	eventually(t, func() bool {
		r := s.Requests("stun")
		return len(r) == 1 && r[0].State == "cwmp_success" && r[0].SessionID != ""
	})
	var status Status
	s.Get("stun_status", &status)
	if status.Accepted != 1 || status.Rejected != 2 {
		t.Fatalf("incorrect auth/replay counters: %+v", status)
	}
	var last replay
	s.Get("udp_replay", &last)
	if last.Timestamp != 1700000000 {
		t.Fatal("invalid packet changed replay state")
	}
}
func TestSTUNWireAndUDPToCWMP(t *testing.T) {
	for _, mode := range []string{"rfc3489", "rfc5389"} {
		t.Run(mode, func(t *testing.T) {
			m, c, s, parent := environment(t)
			ctx, cancel := context.WithCancel(parent)
			defer cancel()
			server, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if e != nil {
				t.Fatal(e)
			}
			defer server.Close()
			c.CWMP.STUNServer = "127.0.0.1"
			c.CWMP.STUNPort = server.LocalAddr().(*net.UDPAddr).Port
			c.CWMP.STUNMode = mode
			c.CWMP.STUNUsername = "stun-account"
			c.CWMP.STUNPassword = "stun-account-password"
			modern := mode == "rfc5389"
			serverErr := make(chan error, 1)
			go func() {
				buf := make([]byte, 8192)
				sent := false
				for {
					n, peer, e := server.ReadFromUDP(buf)
					if e != nil {
						return
					}
					request, e := parseSTUN(buf[:n])
					if e != nil {
						serverErr <- e
						return
					}
					if string(request.get(0xc001)) != "dslforum.org/TR-111 " {
						serverErr <- fmt.Errorf("missing TR-111 attribute")
						return
					}
					response := make([]byte, 20)
					copy(response[4:], request.tx[:])
					key := []byte(c.CWMP.STUNPassword)
					if modern {
						md := md5.Sum([]byte(c.CWMP.STUNUsername + ":test-realm:" + c.CWMP.STUNPassword))
						key = md[:]
					}
					if request.get(8) == nil {
						binary.BigEndian.PutUint16(response, 0x0111)
						response = append(response, attr(9, []byte{0, 0, 4, 1})...)
						if modern {
							response = append(response, attr(0x14, []byte("test-realm"))...)
							response = append(response, attr(0x15, []byte("server-nonce"))...)
						}
					} else {
						if !verifySTUN(request, key, modern) {
							serverErr <- fmt.Errorf("client MESSAGE-INTEGRITY invalid")
							return
						}
						binary.BigEndian.PutUint16(response, 0x0101)
						mapped := []byte{0, 1, 0x9c, 0x40, 203, 0, 113, 44}
						response = append(response, attr(1, mapped)...)
					}
					binary.BigEndian.PutUint16(response[2:], uint16(len(response)-20))
					if request.get(8) != nil {
						response = signSTUN(response, key, modern)
					}
					server.WriteToUDP(response, peer)
					if request.get(8) != nil && !sent {
						sent = true
						server.WriteToUDP(udpMessage(c.CWMP, 1700000000, 22, true), peer)
					}
				}
			}()
			done := make(chan error, 1)
			go func() { done <- m.runSTUN(ctx, c.CWMP) }()
			eventually(t, func() bool {
				select {
				case e := <-serverErr:
					t.Error(e)
					return true
				default:
				}
				r := s.Requests("stun")
				return len(r) == 1 && r[0].State == "cwmp_success"
			})
			var status Status
			s.Get("stun_status", &status)
			if status.MappedAddress != "203.0.113.44:40000" || !status.Authenticated || !status.NATDetected {
				t.Fatalf("binding/auth failed: %+v", status)
			}
			cancel()
			if e := <-done; e != nil {
				t.Fatal(e)
			}
		})
	}
}
func certificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	private, _ := x509.MarshalECPrivateKey(key)
	cert, e := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}))
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	parsed, _ := x509.ParseCertificate(der)
	pool.AddCert(parsed)
	return cert, pool
}
func TestXMPPStartTLSAuthAndRequestToCWMP(t *testing.T) {
	m, c, s, parent := environment(t)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	cert, pool := certificate(t)
	m.TLSConfig = &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	c.CWMP.XMPPServer = "127.0.0.1"
	c.CWMP.XMPPPort = listener.Addr().(*net.TCPAddr).Port
	c.CWMP.XMPPDomain = "localhost"
	c.CWMP.XMPPUsername = "cpe"
	c.CWMP.XMPPPassword = "xmpp-account-password"
	c.CWMP.XMPPAllowed = []string{"acs@localhost"}
	serverDone := make(chan error, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			serverDone <- e
			return
		}
		defer conn.Close()
		go func() { <-ctx.Done(); conn.Close() }()
		session, e := xmpp.ReceiveClientSession(ctx, jid.MustParse("localhost"), conn, xmpp.StartTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}), xmpp.SASLServer(func(n *sasl.Negotiator) bool {
			username, password, _ := n.Credentials()
			return bytes.Equal(username, []byte("cpe")) && bytes.Equal(password, []byte(c.CWMP.XMPPPassword))
		}, sasl.Plain), xmpp.BindCustom(func(_ jid.JID, resource string) (jid.JID, error) { return jid.Parse("cpe@localhost/" + resource) }))
		if e != nil {
			serverDone <- e
			return
		}
		defer session.Conn().Close()
		go session.Serve(xmpp.HandlerFunc(func(t xmlstream.TokenReadEncoder, start *xml.StartElement) error { return nil }))
		for i := 0; i < 2; i++ {
			password := "wrong"
			if i == 1 {
				password = c.CWMP.CRPassword
			}
			iq := stanza.IQ{ID: fmt.Sprintf("cr-%d", i), Type: stanza.GetIQ, To: jid.MustParse("cpe@localhost/lab-router"), From: jid.MustParse("acs@localhost/acs")}
			payload := xml.NewDecoder(strings.NewReader(`<connectionRequest xmlns="` + ConnReqNS + `"><username>` + c.CWMP.CRUsername + `</username><password>` + password + `</password></connectionRequest>`))
			var result struct{}
			e := session.UnmarshalIQ(ctx, iq.Wrap(payload), &result)
			if i == 0 && e == nil {
				serverDone <- fmt.Errorf("invalid CR password accepted")
				return
			}
			if i == 1 && e != nil {
				serverDone <- fmt.Errorf("valid IQ refused: %v", e)
				return
			}
		}
		serverDone <- nil
		<-ctx.Done()
	}()
	clientDone := make(chan error, 1)
	go func() { clientDone <- m.runXMPP(ctx, c.CWMP) }()
	select {
	case e := <-serverDone:
		if e != nil {
			t.Fatal(e)
		}
	case e := <-clientDone:
		t.Fatalf("client failed: %v", e)
	case <-time.After(10 * time.Second):
		t.Fatal("XMPP negotiation timed out")
	}
	eventually(t, func() bool { r := s.Requests("xmpp"); return len(r) == 1 && r[0].State == "cwmp_success" })
	var status Status
	s.Get("xmpp_status", &status)
	if !status.TLS || !status.Authenticated || status.Accepted != 1 || status.Rejected != 1 {
		t.Fatalf("missing TLS/auth/request evidence %+v", status)
	}
	cancel()
	<-clientDone
}

// RFC 5769 §2.1 published interoperability vector (independent of our STUN encoder).
func TestRFC5769IntegrityVector(t *testing.T) {
	wire, e := hex.DecodeString("000100582112a442b7e7a701bc34d686fa87dfae802200105354554e207465737420636c69656e74002400046e0001ff80290008932ff9b151263b36000600096576746a3a68367659202020000800149aeaa70cbfd8cb56781ef2b5b2d3f249c1b571a280280004e57a3bcf")
	if e != nil {
		t.Fatal(e)
	}
	m, e := parseSTUN(wire)
	if e != nil {
		t.Fatal(e)
	}
	if !verifySTUN(m, []byte("VOkJxbRl1RmTxUk/WvJxBt"), true) {
		t.Fatal("MESSAGE-INTEGRITY does not match RFC 5769 vector")
	}
}
func TestXMPPUntrustedTLSNeverAuthenticates(t *testing.T) {
	m, c, _, parent := environment(t)
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	cert, _ := certificate(t)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	c.CWMP.XMPPServer = "127.0.0.1"
	c.CWMP.XMPPPort = listener.Addr().(*net.TCPAddr).Port
	c.CWMP.XMPPUsername = "cpe@localhost"
	c.CWMP.XMPPPassword = "never-send-without-tls"
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		xmpp.ReceiveClientSession(ctx, jid.MustParse("localhost"), conn, xmpp.StartTLS(&tls.Config{Certificates: []tls.Certificate{cert}}), xmpp.SASLServer(func(*sasl.Negotiator) bool { return true }, sasl.Plain), xmpp.BindResource())
	}()
	if m.runXMPP(ctx, c.CWMP) == nil {
		t.Fatal("untrusted certificate accepted")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.states["xmpp"].Authenticated || m.states["xmpp"].TLS {
		t.Fatal("TLS/auth reported success without verified TLS")
	}
}

func TestXMPPInsecureTLSOptIn(t *testing.T) {
	m, c, _, parent := environment(t)
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	cert, _ := certificate(t)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	c.CWMP.XMPPServer = "127.0.0.1"
	c.CWMP.XMPPPort = listener.Addr().(*net.TCPAddr).Port
	c.CWMP.XMPPUsername = "cpe@localhost"
	c.CWMP.XMPPPassword = "lab"
	c.CWMP.XMPPInsecureTLS = true
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		xmpp.ReceiveClientSession(ctx, jid.MustParse("localhost"), conn, xmpp.StartTLS(&tls.Config{Certificates: []tls.Certificate{cert}}), xmpp.SASLServer(func(*sasl.Negotiator) bool { return true }, sasl.Plain), xmpp.BindResource())
	}()
	m.runXMPP(ctx, c.CWMP)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.states["xmpp"].TLS {
		t.Fatalf("self-signed certificate rejected despite opt-in: %+v", m.states["xmpp"])
	}
}
