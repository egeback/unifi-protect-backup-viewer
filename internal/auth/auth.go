// Package auth gates the app behind a single shared login, using a
// bcrypt-hashed password and HMAC-signed session cookies. No user database,
// no roles — this is a home NVR viewer, not a multi-tenant app.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName = "session"
	sessionTTL = 30 * 24 * time.Hour
)

type Manager struct {
	user         string
	passwordHash []byte
	secret       []byte
}

func New(user, passwordHash, secret string) *Manager {
	return &Manager{user: user, passwordHash: []byte(passwordHash), secret: []byte(secret)}
}

// LoginHandler checks the submitted credentials and, on success, sets a
// signed session cookie.
func (m *Manager) LoginHandler(w http.ResponseWriter, r *http.Request) {
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	userOK := subtle.ConstantTimeCompare([]byte(creds.Username), []byte(m.user)) == 1
	passOK := bcrypt.CompareHashAndPassword(m.passwordHash, []byte(creds.Password)) == nil
	if !userOK || !passOK {
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    m.sign(creds.Username, time.Now().Add(sessionTTL)),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		Expires:  time.Now().Add(sessionTTL),
	})
	w.WriteHeader(http.StatusOK)
}

// LogoutHandler clears the session cookie.
func (m *Manager) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusOK)
}

// RequireSession wraps a handler so it 401s (for /api/*) or redirects to
// /login (everything else) when there's no valid session cookie.
func (m *Manager) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil || !m.valid(cookie.Value) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Manager) sign(username string, expires time.Time) string {
	payload := username + "|" + strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(payload))
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (m *Manager) valid(cookieValue string) bool {
	_, err := m.verify(cookieValue)
	return err == nil
}

func (m *Manager) verify(cookieValue string) (username string, err error) {
	parts := strings.SplitN(cookieValue, ".", 2)
	if len(parts) != 2 {
		return "", errors.New("malformed session cookie")
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}

	mac := hmac.New(sha256.New, m.secret)
	mac.Write(payloadRaw)
	expected := mac.Sum(nil)
	if !hmac.Equal(sig, expected) {
		return "", errors.New("invalid session signature")
	}

	payload := string(payloadRaw)
	fields := strings.SplitN(payload, "|", 2)
	if len(fields) != 2 {
		return "", errors.New("malformed session payload")
	}
	expiryUnix, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return "", err
	}
	if time.Now().Unix() > expiryUnix {
		return "", errors.New("session expired")
	}
	return fields[0], nil
}

// HashPassword is a small helper for operators generating AUTH_PASSWORD_HASH.
func HashPassword(plaintext string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	return string(h), err
}
