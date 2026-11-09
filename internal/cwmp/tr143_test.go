package cwmp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func waitTR143(t *testing.T, m Model, kind string) DiagState {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if d := m.diagState(kind); d.State != "Requested" {
			return d
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s diagnostic never finished", kind)
	return DiagState{}
}

func hasEvent(m Model, code string) bool {
	for _, e := range m.Service.Store.Events() {
		if e.Code == code {
			return true
		}
	}
	return false
}

func param(t *testing.T, m Model, name string) string {
	t.Helper()
	ps, e := m.Parameters()
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range ps {
		if p.Name == name {
			return p.Value
		}
	}
	t.Fatalf("parameter %s not reported", name)
	return ""
}

func TestTR143DownloadAndUpload(t *testing.T) {
	m := testModel(t)
	const size = 3 << 20
	var uploaded int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			w.Header().Set("Content-Length", strconv.Itoa(size))
			w.Write(make([]byte, size))
		case "PUT":
			uploaded, _ = io.Copy(io.Discard, r.Body)
		}
	}))
	defer srv.Close()

	// Objects and capabilities must be visible to the ACS.
	if param(t, m, DL+"DiagnosticsState") != "None" || param(t, m, Root+"Capabilities.PerformanceDiagnostic.DownloadTransports") != "HTTP" {
		t.Fatal("TR-143 objects not reported")
	}
	ctx := context.Background()
	if e := m.Set(ctx, []Parameter{{Name: DL + "DownloadURL", Type: "string", Value: srv.URL + "/file"}, {Name: DL + "DiagnosticsState", Type: "string", Value: "Requested"}}, "k1"); e != nil {
		t.Fatal(e)
	}
	if param(t, m, DL+"DiagnosticsState") != "Requested" {
		t.Fatal("state not Requested after SPV")
	}
	m.RunPendingTR143()
	d := waitTR143(t, m, "download")
	if d.State != "Completed" || d.TestBytes != size || d.BOMTime.IsZero() || d.EOMTime.Before(d.BOMTime) || d.ROMTime.After(d.BOMTime) {
		t.Fatalf("download result: %+v", d)
	}
	if v, _ := strconv.ParseFloat(param(t, m, DL+"X_LAB_ThroughputMbps"), 64); v <= 0 {
		t.Fatal("X_LAB_ThroughputMbps not computed")
	}
	if param(t, m, DL+"TestBytesReceived") != strconv.Itoa(size) || param(t, m, DL+"DiagnosticsState") != "Completed" {
		t.Fatal("download parameters not updated")
	}
	if !hasEvent(m, "8 DIAGNOSTICS COMPLETE") {
		t.Fatal("8 DIAGNOSTICS COMPLETE not queued")
	}

	if e := m.Set(ctx, []Parameter{{Name: UL + "UploadURL", Type: "string", Value: srv.URL + "/up"}, {Name: UL + "TestFileLength", Type: "unsignedInt", Value: "1048576"}, {Name: UL + "DiagnosticsState", Type: "string", Value: "Requested"}}, "k2"); e != nil {
		t.Fatal(e)
	}
	m.RunPendingTR143()
	u := waitTR143(t, m, "upload")
	if u.State != "Completed" || uploaded != 1048576 || u.EOMTime.Before(u.BOMTime) {
		t.Fatalf("upload result: %+v (server got %d)", u, uploaded)
	}
}

func TestTR143ErrorsAndValidation(t *testing.T) {
	m := testModel(t)
	ctx := context.Background()
	// Only "Requested" may be written to DiagnosticsState.
	if e := m.Set(ctx, []Parameter{{Name: DL + "DiagnosticsState", Type: "string", Value: "Completed"}}, ""); e == nil {
		t.Fatal("DiagnosticsState=Completed accepted")
	}
	// Requested without URL is rejected, nothing persisted.
	if e := m.Set(ctx, []Parameter{{Name: DL + "DiagnosticsState", Type: "string", Value: "Requested"}}, ""); e == nil || m.diagState("download").State != "None" {
		t.Fatal("Requested without URL accepted")
	}
	// Unreachable server → Error_InitConnectionFailed, event still sent.
	l := httptest.NewServer(http.NotFoundHandler())
	dead := l.URL
	l.Close()
	if e := m.Set(ctx, []Parameter{{Name: DL + "DownloadURL", Type: "string", Value: dead}, {Name: DL + "DiagnosticsState", Type: "string", Value: "Requested"}}, ""); e != nil {
		t.Fatal(e)
	}
	m.RunPendingTR143()
	if d := waitTR143(t, m, "download"); d.State != "Error_InitConnectionFailed" || !hasEvent(m, "8 DIAGNOSTICS COMPLETE") {
		t.Fatalf("unreachable server: %+v", d)
	}
	// HTTP error status → Error_TransferFailed.
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	m.Set(ctx, []Parameter{{Name: DL + "DownloadURL", Type: "string", Value: srv.URL}, {Name: DL + "DiagnosticsState", Type: "string", Value: "Requested"}}, "")
	m.RunPendingTR143()
	if d := waitTR143(t, m, "download"); d.State != "Error_TransferFailed" {
		t.Fatalf("404: %+v", d)
	}
	// Changing a parameter without Requested resets the state to None.
	m.Set(ctx, []Parameter{{Name: DL + "DSCP", Type: "unsignedInt", Value: "10"}}, "")
	if m.diagState("download").State != "None" {
		t.Fatal("state not reset to None")
	}
}
