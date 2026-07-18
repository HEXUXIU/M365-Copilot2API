package web

import (
	"encoding/json"
	"net/http"
)

func (s *Server) conversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jsonOut(w, map[string]any{"conversations": s.sessions.list()})
}

func (s *Server) deleteConversation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.ID == "" {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if !s.sessions.delete(body.ID) {
		http.Error(w, "conversation not found", http.StatusNotFound)
		return
	}
	jsonOut(w, map[string]string{"status": "deleted"})
}
