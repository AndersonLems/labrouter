package service

import (
	"context"
	"fmt"
	"lab-router/internal/model"
	"lab-router/internal/store"
	"testing"
)

func TestApplyFailureDoesNotPersist(t *testing.T) {
	dir := t.TempDir()
	s, e := store.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	s.Init("test-admin-password")
	before, _ := s.Config()
	svc := ConfigService{Store: s, Dir: dir, Live: true, ApplyNetwork: func(model.Config) error { return fmt.Errorf("driver refused") }}
	e = svc.Update(context.Background(), "test", func(c *model.Config) error { c.WiFi.SSID = "must-not-persist"; return nil })
	if e == nil {
		t.Fatal("apply failure ignored")
	}
	after, _ := s.Config()
	if after.WiFi.SSID != before.WiFi.SSID {
		t.Fatal("configuration committed before real AP confirmation")
	}
}
func TestObservationRefusesNetwork(t *testing.T) {
	dir := t.TempDir()
	s, _ := store.Open(dir)
	defer s.DB.Close()
	s.Init("test-admin-password")
	svc := ConfigService{Store: s, Dir: dir}
	if svc.Update(context.Background(), "test", func(c *model.Config) error { c.WiFi.SSID = "changed"; return nil }) == nil {
		t.Fatal("observation wrote network")
	}
}
