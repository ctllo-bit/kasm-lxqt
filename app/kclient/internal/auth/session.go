package auth

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

type Session struct {
	Authorization string
	ExpiresAt     time.Time
}

type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]Session
}

const (
	sessionCookieName = "kclient_session"
	sessionTTL        = 24 * time.Hour
)

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
	defer s.mu.Unlock()

	s.sessions[sessionID] = Session{
		Authorization: authorization,
		ExpiresAt:     time.Now().Add(sessionTTL),
	}

	return sessionID, nil
}

func (s *SessionStore) Get(sessionID string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return Session{}, false
	}

	if time.Now().After(sess.ExpiresAt) {
		delete(s.sessions, sessionID)
		return Session{}, false
	}

	return sess, true
}

func (s *SessionStore) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, sessionID)
}

func (s *SessionStore) GetFromRequest(r *http.Request) (Session, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return Session{}, false
	}

	return s.Get(cookie.Value)
}
