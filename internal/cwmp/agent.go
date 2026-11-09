package cwmp

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"github.com/icholy/digest"
	"io"
	"lab-router/internal/model"
	"lab-router/internal/store"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Status struct {
	Updated     time.Time `json:"updated"`
	SessionID   string    `json:"session_id"`
	State       string    `json:"state"`
	LastInform  time.Time `json:"last_inform"`
	LastSuccess time.Time `json:"last_success"`
	NextInform  time.Time `json:"next_inform"`
	HTTP        int       `json:"http"`
	Error       string    `json:"error"`
	Events      []string  `json:"events"`
	Received    []string  `json:"received"`
	Executed    []string  `json:"executed"`
	DurationMS  int64     `json:"duration_ms"`
	STUN        string    `json:"stun"`
	XMPP        string    `json:"xmpp"`
}
type Agent struct {
	Model Model
	mu    sync.Mutex
	State Status
	Wake  chan string
	After []func()
}

func New(m Model) *Agent {
	return &Agent{Model: m, State: Status{State: "disabled", STUN: "consulte diagnóstico STUN", XMPP: "consulte diagnóstico XMPP"}, Wake: make(chan string, 8)}
}
func (a *Agent) save() {
	a.State.Updated = time.Now().UTC()
	a.Model.Service.Store.Put("cwmp_status", a.State)
}
func (a *Agent) Trigger(code string) bool {
	select {
	case a.Wake <- code:
		return true
	default:
		return false
	}
}
func (a *Agent) Session(ctx context.Context) (resultErr error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.Model.Service.Store
	c, e := s.Config()
	if e != nil {
		return e
	}
	if !c.CWMP.Enabled {
		return fmt.Errorf("CWMP desativado")
	}
	a.State.SessionID = model.Token()
	if s.PendingRequests() {
		s.Event("6 CONNECTION REQUEST", "")
	}
	if e = s.BeginRequests(a.State.SessionID); e != nil {
		return fmt.Errorf("fila de Connection Requests indisponível")
	}
	defer func() { s.FinishRequests(a.State.SessionID, resultErr) }()
	a.State.State = "connecting"
	a.State.Error = ""
	a.State.Received = []string{}
	a.State.Executed = []string{}
	a.State.Events = []string{}
	a.After = nil
	start := time.Now()
	a.save()
	defer func() { a.State.DurationMS = time.Since(start).Milliseconds(); a.save() }()
	events := s.Events()
	if len(events) == 0 {
		s.Event("2 PERIODIC", "")
		events = s.Events()
	}
	for _, v := range events {
		a.State.Events = append(a.State.Events, v.Code)
	}
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, MaxIdleConnsPerHost: 1, ResponseHeaderTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	auth := &digest.Transport{Username: c.CWMP.Username, Password: c.CWMP.Password, Transport: transport, Jar: jar}
	client := &http.Client{Transport: auth, Jar: jar, Timeout: 90 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	post := func(body []byte, request bool) ([]byte, error) {
		req, e := http.NewRequestWithContext(ctx, "POST", c.CWMP.URL, bytes.NewReader(body))
		if e != nil {
			return nil, fmt.Errorf("URL ACS inválida")
		}
		if len(body) > 0 {
			req.Header.Set("Content-Type", "text/xml; charset=utf-8")
			if request {
				req.Header.Set("SOAPAction", `""`)
			} else {
				req.Header["SOAPAction"] = []string{""}
			}
		}
		if c.CWMP.Username != "" {
			req.SetBasicAuth(c.CWMP.Username, c.CWMP.Password)
		}
		res, e := client.Do(req)
		if e != nil {
			return nil, fmt.Errorf("falha de transporte/autenticação ACS: %v", e)
		}
		defer res.Body.Close()
		a.State.HTTP = res.StatusCode
		a.save()
		b, e := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		if e != nil {
			return nil, fmt.Errorf("leitura SOAP falhou")
		}
		if res.StatusCode == 204 {
			return nil, nil
		}
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("ACS HTTP %d", res.StatusCode)
		}
		if len(b) >= 2<<20 {
			return nil, fmt.Errorf("resposta ACS excede limite")
		}
		return b, nil
	}
	fail := func(err error) error {
		a.State.State = "error"
		a.State.Error = err.Error()
		s.Record("sessions", "CPE", "session", a.State.Error)
		return err
	}
	a.State.LastInform = time.Now().UTC()
	s.Record("sessions", "CPE", "Inform", "session="+a.State.SessionID+" events="+strings.Join(a.State.Events, ", "))
	body, e := post(a.inform(c, events), true)
	if e != nil {
		return fail(e)
	}
	_, rpc, e := Parse(body)
	if e != nil || rpc.Name.Local != "InformResponse" {
		return fail(fmt.Errorf("ACS não retornou InformResponse SOAP válido"))
	}
	s.Ack(events)
	a.State.State = "session"
	a.save()
	body, e = post(nil, false)
	if e != nil {
		return fail(e)
	}
	for round := 0; len(body) > 0; round++ {
		if round >= 64 {
			return fail(fmt.Errorf("limite de RPCs por sessão atingido"))
		}
		id, rpc, e := Parse(body)
		if e != nil {
			return fail(fmt.Errorf("SOAP recebido inválido"))
		}
		if rpc.Name.Local == "Fault" {
			return fail(fmt.Errorf("ACS retornou SOAP Fault"))
		}
		a.State.Received = append(a.State.Received, rpc.Name.Local)
		response, after, e := a.Model.Handle(ctx, rpc)
		result := "sucesso"
		if e != nil {
			f, ok := e.(*Fault)
			if !ok {
				f = &Fault{9002, "Internal error", ""}
			}
			response = faultXML(f)
			result = fmt.Sprintf("fault %d: %s", f.Code, f.Message)
			if f.Parameter != "" {
				result += " (" + f.Parameter + ")"
			}
		} else {
			a.State.Executed = append(a.State.Executed, rpc.Name.Local)
			if after != nil {
				a.After = append(a.After, after)
			}
		}
		s.Record("sessions", "ACS", rpc.Name.Local, result)
		a.save()
		body, e = post(Envelope(id, response), false)
		if e != nil {
			return fail(e)
		}
	}
	a.State.State = "idle"
	a.State.LastSuccess = time.Now().UTC()
	s.Record("sessions", "CPE", "session", "InformResponse e encerramento CWMP confirmados")
	for _, fn := range a.After {
		fn()
	}
	a.Model.RunPendingTR143() // TR-143: Requested diagnostics start after the session ends
	return nil
}
func (a *Agent) inform(c model.Config, events []store.Event) []byte {
	w := c.CWMP
	eventXML := fmt.Sprintf(`<Event soap-enc:arrayType="cwmp:EventStruct[%d]">`, len(events))
	for _, e := range events {
		eventXML += "<EventStruct><EventCode>" + esc(e.Code) + "</EventCode><CommandKey>" + esc(e.Key) + "</CommandKey></EventStruct>"
	}
	eventXML += "</Event>"
	ps, _ := a.Model.Parameters()
	included := []Parameter{}
	for _, p := range ps {
		if p.Name == MS+"ConnectionRequestURL" || p.Name == MS+"ParameterKey" || p.Name == MS+"UDPConnectionRequestAddress" || p.Name == MS+"NATDetected" || p.Name == MS+"ConnReqJabberID" || strings.HasPrefix(p.Name, Root+"DeviceInfo.") || informExtra[p.Name] {
			included = append(included, p)
		}
	}
	return Envelope(model.Token()[:12], "<cwmp:Inform><DeviceId><Manufacturer>"+esc(w.Manufacturer)+"</Manufacturer><OUI>"+esc(w.OUI)+"</OUI><ProductClass>"+esc(w.ProductClass)+"</ProductClass><SerialNumber>"+esc(w.Serial)+"</SerialNumber></DeviceId>"+eventXML+"<MaxEnvelopes>1</MaxEnvelopes><CurrentTime>"+time.Now().UTC().Format(time.RFC3339)+"</CurrentTime><RetryCount>0</RetryCount>"+valuesXML(included)+"</cwmp:Inform>")
}
func (a *Agent) Run(ctx context.Context) {
	s := a.Model.Service.Store
	s.Event("1 BOOT", "")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	next := time.Now()
	retries := 0
	lastRequest := ""
	for {
		select {
		case <-ctx.Done():
			return
		case code := <-a.Wake:
			s.Event(code, "")
			next = time.Now()
		case <-ticker.C:
		}
		var request string
		s.Get("inform_request", &request)
		if request != "" && request != lastRequest {
			lastRequest = request
			next = time.Now()
		}
		c, e := s.Config()
		if e != nil || !c.CWMP.Enabled {
			a.mu.Lock()
			a.State.State = "disabled"
			a.save()
			a.mu.Unlock()
			continue
		}
		if retries == 0 && (s.PendingRequests() || len(s.Events()) > 0) {
			next = time.Now()
		}
		if retries == 0 && !a.State.LastInform.IsZero() && c.CWMP.PeriodicEnable {
			candidate := a.State.LastInform.Add(time.Duration(c.CWMP.Interval) * time.Second)
			if candidate.Before(next) {
				next = candidate
			}
		}
		due := !time.Now().Before(next)
		if !due {
			continue
		}
		if len(s.Events()) == 0 && !c.CWMP.PeriodicEnable {
			next = time.Now().Add(5 * time.Second)
			continue
		}
		if len(s.Events()) == 0 {
			s.Event("2 PERIODIC", "")
		}
		if e = a.Session(ctx); e != nil {
			retries++
			delay := time.Duration(10*(1<<min(retries, 6))) * time.Second
			next = time.Now().Add(delay)
		} else {
			retries = 0
			next = a.State.LastInform.Add(time.Duration(c.CWMP.Interval) * time.Second)
		}
		a.mu.Lock()
		a.State.NextInform = next
		a.save()
		a.mu.Unlock()
	}
}

// Connection Request is deliberately loopback-only until reachability is explicitly designed.
// HTTP Digest MD5 qop=auth, bounded nonce lifetime and nonce-count replay protection.
func (a *Agent) ConnectionHandler() http.Handler {
	var mu sync.Mutex
	nonces := map[string]time.Time{}
	counts := map[string]int{}
	re := regexp.MustCompile(`(\w+)=(?:"([^"]*)"|([^, ]+))`)
	md := func(s string) string { b := md5.Sum([]byte(s)); return hex.EncodeToString(b[:]) }
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/connection-request" {
			http.NotFound(w, r)
			return
		}
		c, e := a.Model.Service.Store.Config()
		crUser, crPass := c.CWMP.ConnReq()
		if e != nil || !c.CWMP.Enabled || crUser == "" || crPass == "" {
			http.Error(w, "Connection Request desativado", 503)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for n, t := range nonces {
			if time.Since(t) > 2*time.Minute {
				delete(nonces, n)
			}
		}
		fields := map[string]string{}
		header := r.Header.Get("Authorization")
		if strings.HasPrefix(header, "Digest ") {
			for _, m := range re.FindAllStringSubmatch(strings.TrimPrefix(header, "Digest "), -1) {
				v := m[2]
				if v == "" {
					v = m[3]
				}
				fields[m[1]] = v
			}
		}
		n := fields["nonce"]
		nc, ne := strconv.ParseInt(fields["nc"], 16, 32)
		k := n + ":" + fields["cnonce"]
		t, exists := nonces[n]
		expected := md(md(crUser+":LAB Router:"+crPass) + ":" + n + ":" + fields["nc"] + ":" + fields["cnonce"] + ":auth:" + md("GET:"+r.URL.RequestURI()))
		valid := exists && time.Since(t) < 2*time.Minute && fields["username"] == crUser && fields["realm"] == "LAB Router" && fields["uri"] == r.URL.RequestURI() && fields["qop"] == "auth" && fields["cnonce"] != "" && ne == nil && nc > int64(counts[k]) && subtle.ConstantTimeCompare([]byte(expected), []byte(fields["response"])) == 1
		if !valid {
			if len(nonces) > 512 {
				nonces = map[string]time.Time{}
				counts = map[string]int{}
			}
			nonce := model.Token()
			nonces[nonce] = time.Now()
			w.Header().Set("WWW-Authenticate", `Digest realm="LAB Router", nonce="`+nonce+`", algorithm=MD5, qop="auth"`)
			w.WriteHeader(401)
			return
		}
		counts[k] = int(nc)
		if _, e := a.QueueRequest("http", r.RemoteAddr); e != nil {
			http.Error(w, "fila ocupada", 503)
			return
		}
		w.WriteHeader(200)
	})
}
func Diagnose(ctx context.Context, acs string) map[string]any {
	out := map[string]any{}
	u, e := url.Parse(acs)
	if e != nil {
		return map[string]any{"error": "URL inválida"}
	}
	ips, e := net.DefaultResolver.LookupHost(ctx, u.Hostname())
	out["dns"] = ips
	out["dns_ok"] = e == nil
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	out["tcp_ok"] = e == nil
	if conn != nil {
		conn.Close()
	}
	out["note"] = "DNS/TCP não comprovam SOAP. Use Enviar Inform e consulte histórico."
	return out
}

func (a *Agent) QueueRequest(mechanism, peer string) (string, error) {
	c, e := a.Model.Service.Store.Config()
	if e != nil || !c.CWMP.Enabled {
		return "", fmt.Errorf("CWMP desativado")
	}
	id, e := a.Model.Service.Store.AddRequest(mechanism, peer)
	if e != nil {
		return "", e
	}
	a.Model.Service.Store.Event("6 CONNECTION REQUEST", "")
	a.Trigger("6 CONNECTION REQUEST")
	return id, nil
}
