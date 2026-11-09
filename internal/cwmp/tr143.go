package cwmp

// TR-143 throughput diagnostics for the TR-098 data model:
//   InternetGatewayDevice.DownloadDiagnostics.  (HTTP/HTTPS GET)
//   InternetGatewayDevice.UploadDiagnostics.    (HTTP/HTTPS PUT)
// Flow (TR-069 A.4 / TR-143): the ACS sets the URL and DiagnosticsState=Requested;
// the test starts after the CWMP session ends; on completion (success or error)
// the CPE queues "8 DIAGNOSTICS COMPLETE" and the ACS reads the results.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	DL             = Root + "DownloadDiagnostics."
	UL             = Root + "UploadDiagnostics."
	tr143TimeFmt   = "2006-01-02T15:04:05.000000Z"
	tr143Unknown   = "0001-01-01T00:00:00Z"
	tr143MaxTime   = 120 * time.Second
	tr143MaxUpload = 1 << 30 // 1 GiB safety cap for TestFileLength
)

// DiagState is persisted per test kind ("download" / "upload").
type DiagState struct {
	State            string    `json:"state"` // None, Requested, Completed, Error_*
	Interface        string    `json:"interface"`
	URL              string    `json:"url"`
	DSCP             int       `json:"dscp"`
	EthernetPriority int       `json:"ethernet_priority"`
	TestFileLength   int64     `json:"test_file_length"`
	ROMTime          time.Time `json:"rom"`
	BOMTime          time.Time `json:"bom"`
	EOMTime          time.Time `json:"eom"`
	TCPOpenRequest   time.Time `json:"tcp_open_request"`
	TCPOpenResponse  time.Time `json:"tcp_open_response"`
	TestBytes        int64     `json:"test_bytes"`  // TestBytesReceived (download)
	TotalBytes       int64     `json:"total_bytes"` // interface Total(BytesReceived|BytesSent)
	Detail           string    `json:"detail,omitempty"`
}

func (m Model) diagState(kind string) DiagState {
	var d DiagState
	if m.Service.Store.Get("tr143_"+kind, &d) != nil || d.State == "" {
		d = DiagState{State: "None"}
	}
	return d
}
func (m Model) putDiag(kind string, d DiagState) { m.Service.Store.Put("tr143_"+kind, d) }

func tr143Time(t time.Time) string {
	if t.IsZero() {
		return tr143Unknown
	}
	return t.UTC().Format(tr143TimeFmt)
}

// tr143Parameters appends the TR-143 objects to the parameter list.
func (m Model) tr143Parameters(add func(n, t, v string, w bool)) {
	add(Root+"Capabilities.PerformanceDiagnostic.DownloadTransports", "string", "HTTP", false)
	add(Root+"Capabilities.PerformanceDiagnostic.UploadTransports", "string", "HTTP", false)
	for _, kind := range []string{"download", "upload"} {
		d, p := m.diagState(kind), DL
		if kind == "upload" {
			p = UL
		}
		add(p+"DiagnosticsState", "string", d.State, true)
		add(p+"Interface", "string", d.Interface, true)
		add(p+"DSCP", "unsignedInt", strconv.Itoa(d.DSCP), true)
		add(p+"EthernetPriority", "unsignedInt", strconv.Itoa(d.EthernetPriority), true)
		add(p+"ROMTime", "dateTime", tr143Time(d.ROMTime), false)
		add(p+"BOMTime", "dateTime", tr143Time(d.BOMTime), false)
		add(p+"EOMTime", "dateTime", tr143Time(d.EOMTime), false)
		add(p+"TCPOpenRequestTime", "dateTime", tr143Time(d.TCPOpenRequest), false)
		add(p+"TCPOpenResponseTime", "dateTime", tr143Time(d.TCPOpenResponse), false)
		add(p+"X_LAB_ThroughputMbps", "string", strconv.FormatFloat(throughputMbps(kind, d), 'f', 2, 64), false)
		if kind == "download" {
			add(p+"DownloadURL", "string", d.URL, true)
			add(p+"TestBytesReceived", "unsignedInt", strconv.FormatInt(d.TestBytes, 10), false)
			add(p+"TotalBytesReceived", "unsignedInt", strconv.FormatInt(d.TotalBytes, 10), false)
		} else {
			add(p+"UploadURL", "string", d.URL, true)
			add(p+"TestFileLength", "unsignedInt", strconv.FormatInt(d.TestFileLength, 10), true)
			add(p+"TotalBytesSent", "unsignedInt", strconv.FormatInt(d.TotalBytes, 10), false)
		}
	}
}

func isTR143(name string) bool { return strings.HasPrefix(name, DL) || strings.HasPrefix(name, UL) }

// prepareTR143 validates the TR-143 part of an SPV and returns the new states
// (not yet persisted, so a failing SPV leaves nothing half-applied).
func (m Model) prepareTR143(values []Parameter) (map[string]DiagState, error) {
	out := map[string]DiagState{}
	requested := map[string]bool{}
	for _, p := range values {
		kind, field := "download", strings.TrimPrefix(p.Name, DL)
		if strings.HasPrefix(p.Name, UL) {
			kind, field = "upload", strings.TrimPrefix(p.Name, UL)
		}
		d, ok := out[kind]
		if !ok {
			d = m.diagState(kind)
		}
		bad := func() error { return &Fault{9007, "Invalid parameter value", p.Name} }
		switch field {
		case "DiagnosticsState":
			if p.Value != "Requested" {
				return nil, bad()
			}
			requested[kind] = true
		case "Interface":
			d.Interface = p.Value
		case "DownloadURL", "UploadURL":
			u, e := url.Parse(p.Value)
			if p.Value != "" && (e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil) {
				return nil, bad()
			}
			d.URL = p.Value
		case "DSCP":
			v, e := strconv.Atoi(p.Value)
			if e != nil || v < 0 || v > 63 {
				return nil, bad()
			}
			d.DSCP = v
		case "EthernetPriority":
			v, e := strconv.Atoi(p.Value)
			if e != nil || v < 0 || v > 7 {
				return nil, bad()
			}
			d.EthernetPriority = v
		case "TestFileLength":
			v, e := strconv.ParseInt(p.Value, 10, 64)
			if e != nil || v < 0 || v > tr143MaxUpload {
				return nil, bad()
			}
			d.TestFileLength = v
		default:
			return nil, &Fault{9008, "Attempt to set a non-writable parameter", p.Name}
		}
		out[kind] = d
	}
	for kind, d := range out {
		if requested[kind] {
			if d.URL == "" || (kind == "upload" && d.TestFileLength == 0) {
				p := DL + "DownloadURL"
				if kind == "upload" {
					p = UL + "UploadURL"
				}
				return nil, &Fault{9007, "Invalid parameter value: URL/TestFileLength required", p}
			}
			d = DiagState{State: "Requested", Interface: d.Interface, URL: d.URL, DSCP: d.DSCP, EthernetPriority: d.EthernetPriority, TestFileLength: d.TestFileLength}
		} else {
			// TR-069: changing any diagnostic parameter without Requested resets the state.
			d.State = "None"
		}
		out[kind] = d
	}
	return out, nil
}

var tr143Running sync.Mutex

// RunPendingTR143 executes every Requested test; called after a CWMP session ends.
func (m Model) RunPendingTR143() {
	if !tr143Running.TryLock() {
		return
	}
	go func() {
		defer tr143Running.Unlock()
		ran := false
		for _, kind := range []string{"download", "upload"} {
			d := m.diagState(kind)
			if d.State != "Requested" {
				continue
			}
			ran = true
			res := runTR143(kind, d)
			m.putDiag(kind, res)
			m.Service.Store.Record("sessions", "CPE", "TR-143 "+kind, describeTR143(kind, res))
		}
		if ran {
			m.Service.Store.Event("8 DIAGNOSTICS COMPLETE", "")
		}
	}()
}

// throughputMbps is the payload rate between BOM and EOM (0 unless Completed).
func throughputMbps(kind string, d DiagState) float64 {
	secs := d.EOMTime.Sub(d.BOMTime).Seconds()
	if d.State != "Completed" || secs <= 0 {
		return 0
	}
	bytes := d.TestBytes
	if kind == "upload" {
		bytes = d.TestFileLength
	}
	return float64(bytes) * 8 / secs / 1e6
}
func describeTR143(kind string, d DiagState) string {
	if d.State != "Completed" {
		return d.State + ": " + d.Detail
	}
	secs := d.EOMTime.Sub(d.BOMTime).Seconds()
	bytes := d.TestBytes
	if kind == "upload" {
		bytes = d.TestFileLength
	}
	mbps := 0.0
	if secs > 0 {
		mbps = float64(bytes) * 8 / secs / 1e6
	}
	return fmt.Sprintf("Completed: %.1f Mbps · %d bytes em %.2f s · %s", mbps, bytes, secs, d.URL)
}

// interfaceFor finds the local interface owning ip (for interface byte counters).
func interfaceFor(ip net.IP) string {
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
				return i.Name
			}
		}
	}
	return ""
}
func ifCounter(iface, which string) int64 {
	if iface == "" {
		return 0
	}
	b, e := os.ReadFile("/sys/class/net/" + iface + "/statistics/" + which)
	if e != nil {
		return 0
	}
	v, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return v
}

type patternReader struct{ left int64 }

func (r *patternReader) Read(p []byte) (int, error) {
	if r.left <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > r.left {
		n = r.left
	}
	for i := range p[:n] {
		p[i] = byte(i)
	}
	r.left -= n
	return int(n), nil
}

func runTR143(kind string, d DiagState) DiagState {
	res := DiagState{Interface: d.Interface, URL: d.URL, DSCP: d.DSCP, EthernetPriority: d.EthernetPriority, TestFileLength: d.TestFileLength}
	fail := func(state, detail string) DiagState { res.State, res.Detail = state, detail; return res }
	ctx, cancel := context.WithTimeout(context.Background(), tr143MaxTime)
	defer cancel()
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, _ string, c syscall.RawConn) error {
		if d.DSCP == 0 {
			return nil
		}
		var serr error
		c.Control(func(fd uintptr) { serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TOS, d.DSCP<<2) })
		return serr
	}}
	tr := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second, DisableCompression: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	var iface, counter string = "", "rx_bytes"
	if kind == "upload" {
		counter = "tx_bytes"
	}
	var startCount int64
	var mu sync.Mutex
	trace := &httptrace.ClientTrace{
		ConnectStart: func(string, string) { mu.Lock(); res.TCPOpenRequest = time.Now().UTC(); mu.Unlock() },
		ConnectDone:  func(string, string, error) { mu.Lock(); res.TCPOpenResponse = time.Now().UTC(); mu.Unlock() },
		GotConn: func(info httptrace.GotConnInfo) {
			if a, ok := info.Conn.LocalAddr().(*net.TCPAddr); ok {
				iface = interfaceFor(a.IP)
			}
		},
		WroteHeaders: func() {
			mu.Lock()
			res.ROMTime = time.Now().UTC()
			if kind == "upload" { // first data byte follows the headers
				res.BOMTime = res.ROMTime
				startCount = ifCounter(iface, counter)
			}
			mu.Unlock()
		},
		GotFirstResponseByte: func() {
			mu.Lock()
			if kind == "download" {
				res.BOMTime = time.Now().UTC()
				startCount = ifCounter(iface, counter)
			} else {
				res.EOMTime = time.Now().UTC() // server answers after receiving the whole body
			}
			mu.Unlock()
		},
	}
	var body io.Reader
	method := "GET"
	if kind == "upload" {
		method, body = "PUT", &patternReader{left: d.TestFileLength}
	}
	req, e := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), method, d.URL, body)
	if e != nil {
		return fail("Error_InitConnectionFailed", "URL inválida")
	}
	req.Header.Set("User-Agent", "lab-router-tr143/0.1")
	if kind == "upload" {
		req.ContentLength = d.TestFileLength
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, e := client.Do(req)
	if e != nil {
		var dnsErr *net.DNSError
		var opErr *net.OpError
		switch {
		case errors.Is(e, context.DeadlineExceeded):
			return fail("Error_Timeout", e.Error())
		case errors.As(e, &dnsErr), errors.As(e, &opErr) && opErr.Op == "dial":
			return fail("Error_InitConnectionFailed", e.Error())
		case res.ROMTime.IsZero():
			return fail("Error_InitConnectionFailed", e.Error())
		case kind == "upload":
			return fail("Error_TransferFailed", e.Error())
		}
		return fail("Error_NoResponse", e.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fail("Error_TransferFailed", "HTTP "+resp.Status)
	}
	if kind == "download" {
		n, e := io.Copy(io.Discard, resp.Body)
		res.EOMTime = time.Now().UTC()
		res.TestBytes = n
		if e != nil {
			if errors.Is(e, context.DeadlineExceeded) {
				return fail("Error_Timeout", fmt.Sprintf("tempo máximo de %s excedido após %d bytes", tr143MaxTime, n))
			}
			return fail("Error_TransferFailed", e.Error())
		}
		if resp.ContentLength > 0 && n != resp.ContentLength {
			return fail("Error_IncorrectSize", fmt.Sprintf("recebidos %d de %d bytes", n, resp.ContentLength))
		}
	} else {
		io.Copy(io.Discard, resp.Body)
	}
	if end := ifCounter(iface, counter); end >= startCount && startCount > 0 {
		res.TotalBytes = end - startCount
	}
	res.State = "Completed"
	return res
}
