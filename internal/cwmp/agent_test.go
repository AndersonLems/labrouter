package cwmp

import (
	"context"
	"fmt"
	"io"
	"lab-router/internal/model"
	"lab-router/internal/network"
	"lab-router/internal/service"
	"lab-router/internal/store"
	"lab-router/internal/system"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func testModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	s, e := store.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	if e = s.Init("test-admin-password"); e != nil {
		t.Fatal(e)
	}
	return Model{Service: &service.ConfigService{Store: s, Dir: dir, Live: true, ApplyNetwork: func(model.Config) error { return nil }}, ReadWireless: func(string) system.Wireless { return system.Wireless{} }, ReadStations: func() []Station { return nil }, ReadLeases: func() []system.Lease { return nil }, ReadPPPoE: func() network.PPPoEStatus { return network.PPPoEStatus{State: "Unconfigured", LastError: "ERROR_NONE"} }}
}
func TestRealSOAPSessionReadWrite(t *testing.T) {
	m := testModel(t)
	applied := ""
	m.Service.ApplyNetwork = func(c model.Config) error { applied = c.WiFi.SSID; return nil }
	var mu sync.Mutex
	step := 0
	acs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		if step == 0 {
			_, rpc, e := Parse(b)
			if e != nil || rpc.Name.Local != "Inform" {
				t.Errorf("no SOAP Inform: %s", b)
			}
			if len(r.Cookies()) != 0 {
				t.Error("unexpected cookies")
			}
			http.SetCookie(w, &http.Cookie{Name: "cwmp-session", Value: "test", Path: "/"})
			w.Header().Set("Content-Type", "text/xml")
			w.Write(Envelope("1", "<cwmp:InformResponse><MaxEnvelopes>1</MaxEnvelopes></cwmp:InformResponse>"))
		} else {
			if _, e := r.Cookie("cwmp-session"); e != nil {
				t.Error("CWMP HTTP session lost cookie")
			}
			switch step {
			case 1:
				if len(b) != 0 || r.Header.Get("SOAPAction") != "" {
					t.Error("expected empty POST")
				}
				w.Write(Envelope("rpc-read", "<cwmp:GetParameterValues><ParameterNames soap-enc:arrayType=\"xsd:string[1]\"><string>"+WLAN+"SSID</string></ParameterNames></cwmp:GetParameterValues>"))
			case 2:
				id, rpc, e := Parse(b)
				if e != nil || id != "rpc-read" || rpc.Name.Local != "GetParameterValuesResponse" {
					t.Error("invalid read response")
				}
				w.Write(Envelope("rpc-write", "<cwmp:SetParameterValues>"+valuesXML([]Parameter{{Name: WLAN + "SSID", Type: "string", Value: "CWMP-TEST-REAL"}})+"<ParameterKey>ssid-test</ParameterKey></cwmp:SetParameterValues>"))
			case 3:
				id, rpc, e := Parse(b)
				if e != nil || id != "rpc-write" || rpc.Name.Local != "SetParameterValuesResponse" || rpc.Value("Status") != "0" {
					t.Errorf("invalid write response: %s", b)
				}
				w.Write(Envelope("rpc-confirm", "<cwmp:GetParameterValues><ParameterNames><string>"+WLAN+"SSID</string></ParameterNames></cwmp:GetParameterValues>"))
			case 4:
				if !strings.Contains(string(b), "CWMP-TEST-REAL") {
					t.Error("updated value not returned")
				}
				w.WriteHeader(204)
			default:
				t.Error("unexpected HTTP exchange")
			}
		}
		step++
	}))
	defer acs.Close()
	c, _ := m.Service.Store.Config()
	c.CWMP.URL = acs.URL
	c.CWMP.Enabled = true
	m.Service.Store.Put("config", c)
	a := New(m)
	if e := a.Session(context.Background()); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if step != 5 || applied != "CWMP-TEST-REAL" {
		t.Fatalf("sequence/apply incomplete: %d %s", step, applied)
	}
	c, _ = m.Service.Store.Config()
	if c.WiFi.SSID != "CWMP-TEST-REAL" || c.ParameterKey != "ssid-test" {
		t.Fatal("SPV persistence failed")
	}
	if a.State.LastSuccess.IsZero() {
		t.Fatal("success timestamp missing")
	}
}
func TestSOAPFaultsAndAtomicWrite(t *testing.T) {
	m := testModel(t)
	c, _ := m.Service.Store.Config()
	for _, tc := range []struct {
		name string
		ps   []Parameter
		code int
	}{{"unknown", []Parameter{{Name: "Device.WiFi.SSID.1.SSID", Type: "string", Value: "x"}}, 9005}, {"readonly", []Parameter{{Name: Root + "DeviceInfo.SerialNumber", Type: "string", Value: "x"}}, 9008}, {"wrongtype", []Parameter{{Name: WLAN + "Enable", Type: "string", Value: "1"}}, 9006}, {"invalid", []Parameter{{Name: WLAN + "SSID", Type: "string", Value: "valid"}, {Name: MS + "PeriodicInformInterval", Type: "unsignedInt", Value: "0"}}, 9007}} {
		t.Run(tc.name, func(t *testing.T) {
			e := m.Set(context.Background(), tc.ps, "")
			f, ok := e.(*Fault)
			if !ok || f.Code != tc.code {
				t.Fatalf("wanted %d got %v", tc.code, e)
			}
			after, _ := m.Service.Store.Config()
			if after.WiFi.SSID != c.WiFi.SSID {
				t.Fatal("partial SPV committed")
			}
		})
	}
	m.Service.ApplyNetwork = func(model.Config) error { return fmt.Errorf("hardware failure") }
	e := m.Set(context.Background(), []Parameter{{Name: WLAN + "SSID", Type: "string", Value: "new"}}, "")
	if f, ok := e.(*Fault); !ok || f.Code != 9002 {
		t.Fatal("network failure not surfaced")
	}
}
func TestRejectFakeInformResponse(t *testing.T) {
	m := testModel(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>ACS login page</html>")) }))
	defer server.Close()
	c, _ := m.Service.Store.Config()
	c.CWMP.Enabled = true
	c.CWMP.URL = server.URL
	m.Service.Store.Put("config", c)
	a := New(m)
	if a.Session(context.Background()) == nil || !a.State.LastSuccess.IsZero() {
		t.Fatal("HTTP 200 confused with SOAP success")
	}
	if len(m.Service.Store.Events()) == 0 {
		t.Fatal("events lost on failed Inform")
	}
}
func TestGetParameterNamesAndUnsupported(t *testing.T) {
	m := testModel(t)
	for _, tc := range []struct{ body, want string }{{"<cwmp:GetParameterNames><ParameterPath>" + WLAN + "</ParameterPath><NextLevel>1</NextLevel></cwmp:GetParameterNames>", "<Name>" + WLAN + "SSID</Name>"}, {"<cwmp:GetRPCMethods/>", "<string>SetParameterValues</string>"}} {
		_, rpc, e := Parse(Envelope("x", tc.body))
		if e != nil {
			t.Fatal(e)
		}
		body, _, e := m.Handle(context.Background(), rpc)
		if e != nil || !strings.Contains(body, tc.want) {
			t.Fatalf("bad RPC response %s %v", body, e)
		}
	}
	_, rpc, _ := Parse(Envelope("x", "<cwmp:Download/>"))
	if _, _, e := m.Handle(context.Background(), rpc); e == nil {
		t.Fatal("unsupported method accepted")
	}
}
