package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedPersistence(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Init("administrator-test-password"); e != nil {
		t.Fatal(e)
	}
	c, _ := s.Config()
	c.CWMP.Password = "very-distinctive-ACS-secret"
	s.Put("config", c)
	id, e := s.Backup()
	if e != nil {
		t.Fatal(e)
	}
	s.DB.Close()
	for _, file := range []string{"lab-router.db", "lab-router.db-wal"} {
		b, _ := os.ReadFile(filepath.Join(dir, file))
		if bytes.Contains(b, []byte(c.CWMP.Password)) {
			t.Fatal("plaintext secret on disk")
		}
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	got, e := s.Config()
	if e != nil || got.CWMP.Serial != c.CWMP.Serial {
		t.Fatal("identity not stable")
	}
	restored, e := s.LoadBackup(id)
	if e != nil || restored.CWMP.Password != c.CWMP.Password {
		t.Fatal("backup not decryptable")
	}
	if !s.Authenticate("administrator-test-password") || s.Authenticate("bad") {
		t.Fatal("authentication failed")
	}
}
