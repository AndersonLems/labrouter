package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/bcrypt"
	"lab-router/internal/model"
	"os"
	"path/filepath"
	"time"
)

type Store struct {
	DB   *sql.DB
	aead cipher.AEAD
}
type Record struct {
	ID     int64  `json:"id"`
	Time   string `json:"time"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Result string `json:"result"`
}

func Open(dir string) (*Store, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	keypath := filepath.Join(dir, "master.key")
	key, e := os.ReadFile(keypath)
	if os.IsNotExist(e) {
		key = make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return nil, e
		}
		f, err := os.OpenFile(keypath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		_, e = f.Write(key)
		f.Close()
	}
	if e != nil {
		return nil, e
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite3", filepath.Join(dir, "lab-router.db")+"?_busy_timeout=10000&_journal_mode=WAL&_foreign_keys=on")
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	_, e = db.Exec(`CREATE TABLE IF NOT EXISTS requests(id TEXT PRIMARY KEY,time TEXT,mechanism TEXT,peer TEXT,state TEXT,session_id TEXT,error TEXT);CREATE TABLE IF NOT EXISTS mechanisms(id INTEGER PRIMARY KEY,time TEXT,actor TEXT,action TEXT,result TEXT);CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY,value BLOB NOT NULL);CREATE TABLE IF NOT EXISTS audit(id INTEGER PRIMARY KEY,time TEXT,actor TEXT,action TEXT,result TEXT);CREATE TABLE IF NOT EXISTS sessions(id INTEGER PRIMARY KEY,time TEXT,actor TEXT,action TEXT,result TEXT);CREATE TABLE IF NOT EXISTS events(id INTEGER PRIMARY KEY,code TEXT,key TEXT);CREATE TABLE IF NOT EXISTS backups(id TEXT PRIMARY KEY,time TEXT,value BLOB);`)
	if e != nil {
		return nil, e
	}
	os.Chmod(filepath.Join(dir, "lab-router.db"), 0600)
	return &Store{db, aead}, nil
}
func (s *Store) seal(b []byte) []byte {
	n := make([]byte, s.aead.NonceSize())
	if _, e := rand.Read(n); e != nil {
		panic(e)
	}
	return s.aead.Seal(n, n, b, nil)
}
func (s *Store) unseal(b []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(b) < n {
		return nil, fmt.Errorf("dado cifrado inválido")
	}
	return s.aead.Open(nil, b[:n], b[n:], nil)
}
func (s *Store) Put(key string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = s.DB.Exec("INSERT INTO kv VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, s.seal(b))
	return e
}
func (s *Store) Get(key string, v any) error {
	var b []byte
	if e := s.DB.QueryRow("SELECT value FROM kv WHERE key=?", key).Scan(&b); e != nil {
		return e
	}
	b, e := s.unseal(b)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func (s *Store) Config() (model.Config, error) {
	var c model.Config
	e := s.Get("config", &c)
	return c, e
}
func (s *Store) Init(password string) error {
	if _, e := s.Config(); e == sql.ErrNoRows {
		if password == "" {
			return fmt.Errorf("defina LAB_ADMIN_PASSWORD na primeira inicialização")
		}
		c := model.Default()
		if e = s.Put("config", c); e != nil {
			return e
		}
		h, e := bcrypt.GenerateFromPassword([]byte(password), 12)
		if e != nil {
			return e
		}
		if e = s.Put("admin", string(h)); e != nil {
			return e
		}
		s.Event("0 BOOTSTRAP", "")
	}
	return nil
}
func (s *Store) Authenticate(p string) bool {
	var h string
	if s.Get("admin", &h) != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(h), []byte(p)) == nil
}
func (s *Store) Record(table, actor, action, result string) {
	if table != "audit" && table != "sessions" && table != "mechanisms" {
		return
	}
	s.DB.Exec("INSERT INTO "+table+"(time,actor,action,result) VALUES(?,?,?,?)", time.Now().UTC().Format(time.RFC3339), actor, action, result)
}
func (s *Store) Records(table string) []Record {
	out := []Record{}
	if table != "audit" && table != "sessions" && table != "mechanisms" {
		return out
	}
	rows, e := s.DB.Query("SELECT id,time,actor,action,result FROM " + table + " ORDER BY id DESC LIMIT 200")
	if e != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r Record
		if rows.Scan(&r.ID, &r.Time, &r.Actor, &r.Action, &r.Result) == nil {
			out = append(out, r)
		}
	}
	return out
}
func (s *Store) Event(code, key string) {
	s.DB.Exec("INSERT INTO events(code,key) SELECT ?,? WHERE NOT EXISTS(SELECT 1 FROM events WHERE code=? AND key=?)", code, key, code, key)
}

type Event struct {
	ID   int64
	Code string
	Key  string
}

func (s *Store) Events() []Event {
	out := []Event{}
	r, e := s.DB.Query("SELECT id,code,key FROM events ORDER BY id")
	if e != nil {
		return out
	}
	defer r.Close()
	for r.Next() {
		var v Event
		r.Scan(&v.ID, &v.Code, &v.Key)
		out = append(out, v)
	}
	return out
}
func (s *Store) Ack(events []Event) {
	for _, e := range events {
		s.DB.Exec("DELETE FROM events WHERE id=?", e.ID)
	}
}
func (s *Store) Backup() (string, error) {
	c, e := s.Config()
	if e != nil {
		return "", e
	}
	id := model.Token()
	b, _ := json.Marshal(c)
	_, e = s.DB.Exec("INSERT INTO backups VALUES(?,?,?)", id, time.Now().UTC().Format(time.RFC3339), s.seal(b))
	return id, e
}
func (s *Store) Backups() []map[string]string {
	out := []map[string]string{}
	r, e := s.DB.Query("SELECT id,time FROM backups ORDER BY time DESC")
	if e != nil {
		return out
	}
	defer r.Close()
	for r.Next() {
		var id, t string
		r.Scan(&id, &t)
		out = append(out, map[string]string{"id": id, "time": t})
	}
	return out
}
func (s *Store) LoadBackup(id string) (model.Config, error) {
	var c model.Config
	var b []byte
	if e := s.DB.QueryRow("SELECT value FROM backups WHERE id=?", id).Scan(&b); e != nil {
		return c, e
	}
	b, e := s.unseal(b)
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	return c, e
}
