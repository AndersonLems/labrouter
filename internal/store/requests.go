package store

import (
	"fmt"
	"lab-router/internal/model"
	"time"
)

type Request struct {
	ID        string `json:"id"`
	Time      string `json:"time"`
	Mechanism string `json:"mechanism"`
	Peer      string `json:"peer"`
	State     string `json:"state"`
	SessionID string `json:"session_id"`
	Error     string `json:"error"`
}

func (s *Store) AddRequest(mechanism, peer string) (string, error) {
	var count int
	if e := s.DB.QueryRow("SELECT count(*) FROM requests WHERE state='queued'").Scan(&count); e != nil {
		return "", e
	}
	if count >= 8 {
		return "", fmt.Errorf("fila de Connection Requests cheia")
	}
	id := model.Token()
	_, e := s.DB.Exec("INSERT INTO requests VALUES(?,?,?,?,?,?,?)", id, time.Now().UTC().Format(time.RFC3339Nano), mechanism, peer, "queued", "", "")
	return id, e
}
func (s *Store) PendingRequests() bool {
	var count int
	s.DB.QueryRow("SELECT count(*) FROM requests WHERE state='queued'").Scan(&count)
	return count > 0
}
func (s *Store) BeginRequests(id string) error {
	_, e := s.DB.Exec("UPDATE requests SET state='session_started',session_id=? WHERE state='queued'", id)
	return e
}
func (s *Store) FinishRequests(id string, err error) {
	state, message := "cwmp_success", ""
	if err != nil {
		state = "cwmp_failed"
		message = err.Error()
	}
	s.DB.Exec("UPDATE requests SET state=?,error=? WHERE session_id=? AND state='session_started'", state, message, id)
}
func (s *Store) Requests(mechanism string) []Request {
	out := []Request{}
	rows, e := s.DB.Query("SELECT id,time,mechanism,peer,state,session_id,error FROM requests WHERE mechanism=? ORDER BY time DESC LIMIT 100", mechanism)
	if e != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r Request
		if rows.Scan(&r.ID, &r.Time, &r.Mechanism, &r.Peer, &r.State, &r.SessionID, &r.Error) == nil {
			out = append(out, r)
		}
	}
	return out
}
