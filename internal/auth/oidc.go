package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"mariner/internal/tlsconfig"
)

type User struct {
	ID, Name string
	Groups   []string
}
type Session struct {
	User     User
	Password string
	Expires  time.Time
}
type SessionStore interface {
	SaveSession(id, userID, userName, groupsJSON, passwordCiphertext string, expiresAt time.Time) error
	LoadSession(id string) (userID, userName, groupsJSON, passwordCiphertext string, expiresAt, lastSeenAt time.Time, found bool, err error)
	DeleteSession(id string) error
	TouchSession(id string, at time.Time) error
}
type Service struct {
	Provider                *oidc.Provider
	OAuth                   *oauth2.Config
	CookieSecret            string
	GroupsClaim             string
	AudienceClaim, Audience string
	NameClaim               string
	DebugJWT                bool
	LogoutEnabled           bool
	LogoutEndpoint          string
	SessionIdleTimeout      time.Duration
	sessionStore            SessionStore
	sessions                map[string]Session // retained only for tests/standalone use
	mu                      sync.RWMutex
}

func (s *Service) SetSessionStore(store SessionStore) { s.sessionStore = store }

func (s *Service) SetSessionIdleTimeout(timeout time.Duration) { s.SessionIdleTimeout = timeout }

func New(issuer, clientID, clientSecret, redirect, cookieSecret, groupsClaim, audienceClaim, audience, nameClaim string, scopes []string, debugJWT, logoutEnabled bool) (*Service, error) {
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	rootCAs, err := tlsconfig.RootCAs()
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: rootCAs}}}
	providerContext := oidc.ClientContext(context.Background(), httpClient)
	logoutEndpoint, err := discoverLogoutEndpoint(httpClient, issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery request failed: %w", err)
	}
	provider, err := oidc.NewProvider(providerContext, issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery request failed: %w", err)
	}
	return &Service{Provider: provider, OAuth: &oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, Endpoint: provider.Endpoint(), RedirectURL: redirect, Scopes: scopes}, CookieSecret: cookieSecret, GroupsClaim: groupsClaim, AudienceClaim: audienceClaim, Audience: audience, NameClaim: nameClaim, DebugJWT: debugJWT, LogoutEnabled: logoutEnabled, LogoutEndpoint: logoutEndpoint, sessions: map[string]Session{}}, nil
}

func discoverLogoutEndpoint(client *http.Client, issuer string) (string, error) {
	wellKnown := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	resp, err := client.Get(wellKnown)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discovery returned %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var document struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return "", fmt.Errorf("invalid discovery document: %w", err)
	}
	return document.EndSessionEndpoint, nil
}
func (s *Service) Login(w http.ResponseWriter, r *http.Request) {
	state := random(18)
	s.setCookie(w, "mariner_state", state, 600)
	http.Redirect(w, r, s.OAuth.AuthCodeURL(state), http.StatusFound)
}
func (s *Service) Callback(r *http.Request) (User, string, error) {
	if r.URL.Query().Get("state") != s.cookie(r, "mariner_state") {
		return User{}, "", errors.New("invalid OIDC state")
	}
	token, err := s.OAuth.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		return User{}, "", err
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return User{}, "", errors.New("OIDC provider did not return an ID token")
	}
	if s.DebugJWT {
		log.Printf("oidc: received id_token=%s", raw)
	}
	verifyConfig := &oidc.Config{ClientID: s.OAuth.ClientID}
	if s.AudienceClaim != "aud" {
		verifyConfig.SkipClientIDCheck = true
	}
	idToken, err := s.Provider.Verifier(verifyConfig).Verify(r.Context(), raw)
	if err != nil {
		log.Printf("oidc: ID token validation failed (issuer/client/audience/expiry/signature): %v", err)
		return User{}, "", errors.New("invalid identity token")
	}
	var claims map[string]json.RawMessage
	if err = idToken.Claims(&claims); err != nil {
		return User{}, "", errors.New("identity token has no subject")
	}
	if s.DebugJWT {
		log.Printf("oidc: validated claims=%s", string(mustJSON(claims)))
	}
	if s.AudienceClaim != "aud" && !contains(claimGroups(claims, s.AudienceClaim), s.Audience) {
		log.Printf("oidc: audience mismatch claim=%q expected=%q", s.AudienceClaim, s.Audience)
		return User{}, "", errors.New("invalid identity token audience")
	}
	sub := claimString(claims, "sub")
	if sub == "" {
		log.Printf("oidc: configured subject claim is missing or empty")
		return User{}, "", errors.New("identity token has no subject")
	}
	name := claimString(claims, s.NameClaim)
	if name == "" {
		name = claimString(claims, "email")
	}
	if name == "OIDC user" && s.NameClaim != "" {
		log.Printf("oidc: name claim %q missing; using fallback", s.NameClaim)
	}
	if name == "" {
		name = claimString(claims, "preferred_username")
	}
	if name == "" {
		name = "OIDC user"
	}
	return User{ID: sub, Name: name, Groups: claimGroups(claims, s.GroupsClaim)}, random(24), nil
}
func mustJSON(value any) []byte { encoded, _ := json.Marshal(value); return encoded }
func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
func claimString(claims map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(claims[key], &value)
	return value
}
func claimGroups(claims map[string]json.RawMessage, key string) []string {
	var groups []string
	if err := json.Unmarshal(claims[key], &groups); err == nil {
		return groups
	}
	if group := claimString(claims, key); group != "" {
		return []string{group}
	}
	return nil
}
func (s *Service) StartSession(w http.ResponseWriter, id string, user User) error {
	expires := time.Now().Add(12 * time.Hour)
	if s.sessionStore != nil {
		if err := s.sessionStore.SaveSession(id, user.ID, user.Name, marshalGroups(user.Groups), "", expires); err != nil {
			return err
		}
	} else {
		s.mu.Lock()
		s.sessions[id] = Session{User: user, Expires: expires}
		s.mu.Unlock()
	}
	s.setCookie(w, "mariner_session", id, 43200)
	return nil
}
func (s *Service) Current(r *http.Request) (Session, string, bool) {
	id := s.cookie(r, "mariner_session")
	if s.sessionStore != nil {
		userID, userName, groupsJSON, encryptedPassword, expires, lastSeen, found, err := s.sessionStore.LoadSession(id)
		now := time.Now()
		if err != nil || !found || now.After(expires) || (s.SessionIdleTimeout > 0 && now.Sub(lastSeen) > s.SessionIdleTimeout) {
			if found && (now.After(expires) || (s.SessionIdleTimeout > 0 && now.Sub(lastSeen) > s.SessionIdleTimeout)) {
				_ = s.sessionStore.DeleteSession(id)
			}
			return Session{}, id, false
		}
		if err := s.sessionStore.TouchSession(id, now); err != nil {
			return Session{}, id, false
		}
		groups := []string{}
		if json.Unmarshal([]byte(groupsJSON), &groups) != nil {
			return Session{}, id, false
		}
		password, err := decryptSessionSecret(encryptedPassword, s.CookieSecret)
		if err != nil {
			return Session{}, id, false
		}
		return Session{User: User{ID: userID, Name: userName, Groups: groups}, Password: password, Expires: expires}, id, true
	}
	s.mu.RLock()
	session, ok := s.sessions[id]
	s.mu.RUnlock()
	return session, id, ok && time.Now().Before(session.Expires)
}
func (s *Service) SetPassword(id, password string) error {
	if s.sessionStore != nil {
		session, _, ok := s.CurrentCookie(id)
		if !ok {
			return errors.New("session not found")
		}
		return s.sessionStore.SaveSession(id, session.User.ID, session.User.Name, marshalGroups(session.User.Groups), encryptSessionSecret(password, s.CookieSecret), session.Expires)
	}
	s.mu.Lock()
	session := s.sessions[id]
	session.Password = password
	s.sessions[id] = session
	s.mu.Unlock()
	return nil
}
func (s *Service) Lock(id string) error { return s.SetPassword(id, "") }
func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {
	id := s.cookie(r, "mariner_session")
	if s.sessionStore != nil {
		_ = s.sessionStore.DeleteSession(id)
	} else {
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
	}
	s.setCookie(w, "mariner_session", "", -1)
	if s.LogoutEnabled && s.LogoutEndpoint != "" {
		logoutURL, err := url.Parse(s.LogoutEndpoint)
		if err == nil {
			query := logoutURL.Query()
			query.Set("client_id", s.OAuth.ClientID)
			logoutURL.RawQuery = query.Encode()
			http.Redirect(w, r, logoutURL.String(), http.StatusFound)
			return
		}
		log.Printf("oidc logout redirect unavailable: %v", err)
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Service) CurrentCookie(id string) (Session, string, bool) {
	if s.sessionStore == nil {
		s.mu.RLock()
		session, ok := s.sessions[id]
		s.mu.RUnlock()
		return session, id, ok && time.Now().Before(session.Expires)
	}
	userID, userName, groupsJSON, encryptedPassword, expires, lastSeen, found, err := s.sessionStore.LoadSession(id)
	now := time.Now()
	if err != nil || !found || now.After(expires) || (s.SessionIdleTimeout > 0 && now.Sub(lastSeen) > s.SessionIdleTimeout) {
		return Session{}, id, false
	}
	var groups []string
	if json.Unmarshal([]byte(groupsJSON), &groups) != nil {
		return Session{}, id, false
	}
	password, err := decryptSessionSecret(encryptedPassword, s.CookieSecret)
	if err != nil {
		return Session{}, id, false
	}
	return Session{User: User{ID: userID, Name: userName, Groups: groups}, Password: password, Expires: expires}, id, true
}

func marshalGroups(groups []string) string { raw, _ := json.Marshal(groups); return string(raw) }

func sessionCipher(secret string) (cipher.AEAD, error) {
	sum := sha256.Sum256([]byte("mariner-session-encryption:" + secret))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func encryptSessionSecret(value, secret string) string {
	if value == "" {
		return ""
	}
	gcm, err := sessionCipher(secret)
	if err != nil {
		return ""
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return ""
	}
	return base64.RawStdEncoding.EncodeToString(append(nonce, gcm.Seal(nil, nonce, []byte(value), nil)...))
}
func decryptSessionSecret(encoded, secret string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	payload, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	gcm, err := sessionCipher(secret)
	if err != nil || len(payload) < gcm.NonceSize() {
		return "", errors.New("invalid session secret")
	}
	raw, err := gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], nil)
	return string(raw), err
}

// EncryptForStorage protects short-lived server-side secrets stored in SQL.
func EncryptForStorage(value, secret string) string { return encryptSessionSecret(value, secret) }

// DecryptForStorage decrypts short-lived server-side secrets stored in SQL.
func DecryptForStorage(encoded, secret string) (string, error) { return decryptSessionSecret(encoded, secret) }
func (s *Service) setCookie(w http.ResponseWriter, name, value string, age int) {
	mac := hmac.New(sha256.New, []byte(s.CookieSecret))
	mac.Write([]byte(value))
	signed := value + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{Name: name, Value: signed, Path: "/", MaxAge: age, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}
func (s *Service) cookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	parts := splitCookie(c.Value)
	if len(parts) != 2 {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(s.CookieSecret))
	mac.Write([]byte(parts[0]))
	signature, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if !hmac.Equal(mac.Sum(nil), signature) {
		return ""
	}
	return parts[0]
}
func splitCookie(v string) []string {
	for i := len(v) - 1; i >= 0; i-- {
		if v[i] == '.' {
			return []string{v[:i], v[i+1:]}
		}
	}
	return nil
}
func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
