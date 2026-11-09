package traversal

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"lab-router/internal/model"
	"mellium.im/sasl"
	"mellium.im/xmlstream"
	"mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
	"net"
	"strconv"
	"strings"
	"time"
)

const ConnReqNS = "urn:broadband-forum-org:cwmp:xmppConnReq-1-0"

type ConnectionIQ struct {
	stanza.IQ
	Request *ConnectionBody `xml:"urn:broadband-forum-org:cwmp:xmppConnReq-1-0 connectionRequest"`
	Ping    *struct{}       `xml:"urn:xmpp:ping ping"`
}
type ConnectionBody struct {
	Username string `xml:"urn:broadband-forum-org:cwmp:xmppConnReq-1-0 username"`
	Password string `xml:"urn:broadband-forum-org:cwmp:xmppConnReq-1-0 password"`
}
type iqError struct {
	XMLName   xml.Name `xml:"error"`
	Type      string   `xml:"type,attr"`
	Condition condition
}
type condition struct{ XMLName xml.Name }
type iqReply struct {
	stanza.IQ
	Error *iqError `xml:",omitempty"`
}

func same(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func allowedJID(from jid.JID, allowed []string) bool {
	if from.String() == "" || from.String() == "." {
		return false
	}
	if len(allowed) == 0 {
		return true
	}
	for _, v := range allowed {
		j, e := jid.Parse(v)
		if e == nil && (j.Equal(from) || j.Resourcepart() == "" && j.Bare().Equal(from.Bare())) {
			return true
		}
	}
	return false
}
func validateIQ(iq ConnectionIQ, c model.CWMP) bool {
	user, pass := c.ConnReq()
	return iq.Type == stanza.GetIQ && iq.ID != "" && iq.Request != nil && allowedJID(iq.From, c.XMPPAllowed) && user != "" && pass != "" && same(iq.Request.Username, user) && same(iq.Request.Password, pass)
}

// Bound decoded stanza size without retaining raw XML (which can contain secrets).
type boundedReader struct {
	xml.TokenReader
	size int
}

func (r *boundedReader) Token() (xml.Token, error) {
	t, e := r.TokenReader.Token()
	switch v := t.(type) {
	case xml.CharData:
		r.size += len(v)
	case xml.StartElement:
		r.size += len(v.Name.Local) + len(v.Name.Space) + 32
		for _, a := range v.Attr {
			r.size += len(a.Value) + len(a.Name.Local)
		}
	}
	if r.size > 65536 {
		return nil, fmt.Errorf("XMPP stanza excede limite")
	}
	return t, e
}
func (m *Manager) runXMPP(ctx context.Context, c model.CWMP) error {
	domain := c.XMPPDomain
	if domain == "" {
		domain = c.XMPPServer
	}
	user := c.XMPPUsername
	if !strings.Contains(user, "@") {
		user += "@" + domain
	}
	j, e := jid.Parse(user)
	if e != nil || j.Localpart() == "" {
		return fmt.Errorf("JID XMPP inválido")
	}
	resource := c.XMPPResource
	if resource == "" {
		resource = "lab-router"
	}
	j, e = jid.Parse(j.Bare().String() + "/" + resource)
	if e != nil {
		return fmt.Errorf("recurso XMPP inválido")
	}
	address := net.JoinHostPort(c.XMPPServer, strconv.Itoa(c.XMPPPort))
	m.update("xmpp", func(s *Status) {
		s.State = "connecting"
		s.Connected = false
		s.Authenticated = false
		s.Authentication = "pending SASL"
		s.TLS = false
		s.LastError = ""
		s.RemoteAddress = address
	})
	conn, e := (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", address)
	if e != nil {
		return fmt.Errorf("conexão TCP XMPP falhou")
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
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	m.update("xmpp", func(s *Status) {
		s.Connected = true
		s.LocalAddress = conn.LocalAddr().String()
		s.State = "negotiating TLS"
		s.LastConnected = time.Now().UTC()
	})
	tlsConfig := &tls.Config{ServerName: j.Domain().String(), MinVersion: tls.VersionTLS12}
	if m.TLSConfig != nil {
		tlsConfig = m.TLSConfig.Clone()
		if tlsConfig.ServerName == "" {
			tlsConfig.ServerName = j.Domain().String()
		}
	}
	if c.XMPPInsecureTLS {
		// Explicit lab opt-in: encryption stays on, server identity is not verified.
		tlsConfig.InsecureSkipVerify = true
	}
	return m.negotiateXMPP(ctx, conn, j, c, tlsConfig)
}
func (m *Manager) negotiateXMPP(ctx context.Context, conn net.Conn, j jid.JID, c model.CWMP, tlsConfig *tls.Config) error {
	tlsFeature := xmpp.StartTLS(tlsConfig)
	tlsNegotiate := tlsFeature.Negotiate
	tlsFeature.Negotiate = func(ctx context.Context, s *xmpp.Session, v interface{}) (xmpp.SessionState, io.ReadWriter, error) {
		state, rw, e := tlsNegotiate(ctx, s, v)
		if e == nil {
			if handshake, ok := rw.(interface{ HandshakeContext(context.Context) error }); ok {
				e = handshake.HandshakeContext(ctx)
			}
		}
		if e == nil {
			m.update("xmpp", func(s *Status) { s.TLS = true; s.State = "authenticating SASL" })
		}
		return state, rw, e
	}
	mechanisms := []sasl.Mechanism{sasl.ScramSha256Plus, sasl.ScramSha256, sasl.ScramSha1Plus, sasl.ScramSha1, sasl.Plain}
	if c.XMPPInsecureTLS {
		// Lab mode: SCRAM-*-PLUS channel binding is moot without certificate verification and the
		// library's binding is rejected by some servers over TLS 1.3 ("Invalid channel binding").
		mechanisms = []sasl.Mechanism{sasl.ScramSha256, sasl.ScramSha1, sasl.Plain}
	}
	auth := xmpp.SASL("", c.XMPPPassword, mechanisms...)
	negotiate := auth.Negotiate
	auth.Negotiate = func(ctx context.Context, s *xmpp.Session, v interface{}) (xmpp.SessionState, io.ReadWriter, error) {
		state, rw, e := negotiate(ctx, s, v)
		if e == nil {
			m.update("xmpp", func(s *Status) {
				s.Authenticated = true
				s.Authentication = "SASL authenticated"
				s.LastAuth = time.Now().UTC()
				s.State = "binding resource"
			})
		}
		return state, rw, e
	}
	session, e := xmpp.NewClientSession(ctx, j, conn, tlsFeature, auth, resourceBinding())
	if e != nil {
		var certificateError *tls.CertificateVerificationError
		var unknownAuthority x509.UnknownAuthorityError
		var hostnameError x509.HostnameError
		if errors.As(e, &certificateError) || errors.As(e, &unknownAuthority) {
			return fmt.Errorf("certificado TLS XMPP não confiável ou inválido (ex.: autoassinado); para testes locais habilite \"Aceitar certificado sem verificação\"")
		}
		if errors.As(e, &hostnameError) {
			return fmt.Errorf("certificado TLS XMPP não corresponde ao domínio do JID")
		}
		return fmt.Errorf("negociação TLS/SASL/bind XMPP falhou: %v", e)
	}
	defer session.Conn().Close()
	if session.State()&xmpp.Secure == 0 || session.State()&xmpp.Authn == 0 {
		return fmt.Errorf("sessão XMPP sem TLS/autenticação recusada")
	}
	conn.SetDeadline(time.Time{})
	m.update("xmpp", func(s *Status) { s.State = "online"; s.JID = session.LocalAddr().String(); s.LastError = "" })
	if c.XMPPInsecureTLS {
		m.event("xmpp", "session.online", "TLS (certificado NÃO verificado), SASL e resource bind confirmados")
	} else {
		m.event("xmpp", "session.online", "TLS, SASL e resource bind confirmados")
	}
	if e = session.Send(ctx, stanza.Presence{Type: stanza.AvailablePresence}.Wrap(nil)); e != nil {
		return fmt.Errorf("envio de presença XMPP falhou")
	}
	keepaliveCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		for wait(keepaliveCtx, 30*time.Second) {
			pingCtx, cancel := context.WithTimeout(keepaliveCtx, 15*time.Second)
			r, e := session.SendIQ(pingCtx, stanza.IQ{Type: stanza.GetIQ, ID: model.Token(), To: j.Domain()}.Wrap(xml.NewDecoder(strings.NewReader(`<ping xmlns="urn:xmpp:ping"/>`))))
			cancel()
			if r != nil {
				r.Close()
			}
			if e != nil {
				conn.Close()
				return
			}
		}
	}()
	seen := map[string]time.Time{}
	e = session.Serve(xmpp.HandlerFunc(func(t xmlstream.TokenReadEncoder, start *xml.StartElement) error {
		if start.Name.Local != "iq" {
			return nil
		}
		var iq ConnectionIQ
		if e := xml.NewTokenDecoder(&boundedReader{TokenReader: &prependToken{first: start, rest: t}}).Decode(&iq); e != nil {
			return fmt.Errorf("stanza XMPP inválida ou excessiva")
		}
		if iq.Type != stanza.GetIQ && iq.Type != stanza.SetIQ {
			return nil
		}
		response := iqReply{IQ: stanza.IQ{ID: iq.ID, To: iq.From, From: session.LocalAddr(), Type: stanza.ResultIQ}}
		failure := ""
		if iq.Ping != nil && iq.Type == stanza.GetIQ {
			return t.Encode(response)
		}
		if iq.Request == nil {
			failure = "service-unavailable"
		} else if !validateIQ(iq, c) {
			m.receive("xmpp", iq.From.String(), false, "JID, tipo IQ ou credenciais Connection Request inválidos")
			failure = "not-authorized"
		} else {
			for k, v := range seen {
				if time.Since(v) > 5*time.Minute {
					delete(seen, k)
				}
			}
			key := iq.From.String() + "\x00" + iq.ID
			if _, ok := seen[key]; ok {
				m.receive("xmpp", iq.From.String(), false, "IQ repetido")
				failure = "not-authorized"
			} else if len(seen) >= 1024 {
				failure = "resource-constraint"
			} else if _, e := m.receive("xmpp", iq.From.String(), true, ""); e != nil {
				failure = "service-unavailable"
			} else {
				seen[key] = time.Now()
			}
		}
		if failure != "" {
			response.Type = stanza.ErrorIQ
			response.Error = &iqError{Type: "cancel", Condition: condition{XMLName: xml.Name{Space: "urn:ietf:params:xml:ns:xmpp-stanzas", Local: failure}}}
		}
		return t.Encode(response)
	}))
	if ctx.Err() != nil {
		return nil
	}
	if e != nil {
		return fmt.Errorf("stream XMPP encerrado ou inválido")
	}
	return fmt.Errorf("servidor XMPP encerrou o stream")
}

// Bind explicitly so the requested resource is encoded correctly on the wire.
// mellium v0.22's bindPayload encodes an empty resource when a client requests one.
func resourceBinding() xmpp.StreamFeature {
	feature := xmpp.BindResource()
	feature.Negotiate = func(ctx context.Context, s *xmpp.Session, _ interface{}) (xmpp.SessionState, io.ReadWriter, error) {
		writer := s.TokenWriter()
		defer writer.Close()
		reader := s.TokenReader()
		defer reader.Close()
		type payload struct {
			Resource string `xml:"resource,omitempty"`
			JID      string `xml:"jid,omitempty"`
		}
		type binding struct {
			stanza.IQ
			Bind payload `xml:"urn:ietf:params:xml:ns:xmpp-bind bind"`
		}
		// Marshal the request without stanza.IQ: its empty to=""/from="" attributes are
		// invalid JIDs and strict servers (e.g. ejabberd) close the stream as not-well-formed.
		type request struct {
			XMLName xml.Name `xml:"iq"`
			ID      string   `xml:"id,attr"`
			Type    string   `xml:"type,attr"`
			Bind    payload  `xml:"urn:ietf:params:xml:ns:xmpp-bind bind"`
		}
		id := model.Token()
		b, e := xml.Marshal(request{ID: id, Type: "set", Bind: payload{Resource: s.LocalAddr().Resourcepart()}})
		if e != nil {
			return 0, nil, e
		}
		if _, e = xmlstream.Copy(writer, stripXMLNS{xml.NewDecoder(bytes.NewReader(b))}); e != nil {
			return 0, nil, e
		}
		if e = writer.Flush(); e != nil {
			return 0, nil, e
		}
		var response binding
		if e = xml.NewTokenDecoder(&boundedReader{TokenReader: reader}).Decode(&response); e != nil {
			return 0, nil, e
		}
		if response.ID != id || response.Type != stanza.ResultIQ {
			return 0, nil, fmt.Errorf("resource bind recusado")
		}
		bound, e := jid.Parse(response.Bind.JID)
		if e != nil || bound.Resourcepart() == "" || !bound.Bare().Equal(s.LocalAddr().Bare()) {
			return 0, nil, fmt.Errorf("JID vinculado inválido")
		}
		s.UpdateAddr(bound)
		return xmpp.Ready, nil, nil
	}
	return feature
}

type prependToken struct {
	first *xml.StartElement
	rest  xml.TokenReader
}

func (p *prependToken) Token() (xml.Token, error) {
	if p.first != nil {
		v := *p.first
		p.first = nil
		return v, nil
	}
	return p.rest.Token()
}

// stripXMLNS drops literal xmlns attributes: the stream token writer already emits the
// namespace from Name.Space, and keeping both yields a duplicate attribute that strict
// servers (ejabberd) reject as not-well-formed.
type stripXMLNS struct{ xml.TokenReader }

func (r stripXMLNS) Token() (xml.Token, error) {
	t, e := r.TokenReader.Token()
	if start, ok := t.(xml.StartElement); ok {
		attrs := start.Attr[:0:0]
		for _, a := range start.Attr {
			if a.Name.Space == "" && a.Name.Local == "xmlns" || a.Name.Space == "xmlns" {
				continue
			}
			attrs = append(attrs, a)
		}
		start.Attr = attrs
		t = start
	}
	return t, e
}
