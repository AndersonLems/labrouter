package cwmp

import (
	"github.com/icholy/digest"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDigestConnectionRequest(t *testing.T) {
	m := testModel(t)
	c, _ := m.Service.Store.Config()
	c.CWMP.Enabled = true
	c.CWMP.CRUsername = "request-user"
	c.CWMP.CRPassword = "different-request-secret"
	m.Service.Store.Put("config", c)
	a := New(m)
	server := httptest.NewServer(a.ConnectionHandler())
	defer server.Close()
	r, e := http.Get(server.URL + "/connection-request")
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatal("unauthenticated connection request allowed")
	}
	client := http.Client{Transport: &digest.Transport{Username: c.CWMP.CRUsername, Password: c.CWMP.CRPassword}}
	r, e = client.Get(server.URL + "/connection-request")
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("authenticated request rejected: %d", r.StatusCode)
	}
	select {
	case code := <-a.Wake:
		if code != "6 CONNECTION REQUEST" {
			t.Fatal(code)
		}
	default:
		t.Fatal("request did not start CWMP event")
	}
}
