package auth

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
)

type Session struct {
	Authorization string
}

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]Session),
	}
}

func (s *SessionStore) Create(authorization string) (string, error) {
	buf := make([]byte, 32)

	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	sessionID := base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	s.sessions[sessionID] = Session{
		Authorization: authorization,
	}
	s.mu.Unlock()

	return sessionID, nil
}

func (s *SessionStore) Get(sessionID string) (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[sessionID]
	return session, ok
}

func (s *SessionStore) GetFromRequest(r *http.Request) (Session, bool) {
	cookie, err := r.Cookie("kclient_session")
	if err != nil {
		return Session{}, false
	}

	return s.Get(cookie.Value)
}
