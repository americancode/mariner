package auth

import (
	"context"
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
	sessions                map[string]Session
	mu                      sync.RWMutex
}

func New(issuer, clientID, clientSecret, redirect, cookieSecret, groupsClaim, audienceClaim, audience, nameClaim string, debugJWT, logoutEnabled bool) (*Service, error) {
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
	return &Service{Provider: provider, OAuth: &oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, Endpoint: provider.Endpoint(), RedirectURL: redirect, Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}, CookieSecret: cookieSecret, GroupsClaim: groupsClaim, AudienceClaim: audienceClaim, Audience: audience, NameClaim: nameClaim, DebugJWT: debugJWT, LogoutEnabled: logoutEnabled, LogoutEndpoint: logoutEndpoint, sessions: map[string]Session{}}, nil
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
func (s *Service) StartSession(w http.ResponseWriter, id string, user User) {
	s.mu.Lock()
	s.sessions[id] = Session{User: user, Expires: time.Now().Add(12 * time.Hour)}
	s.mu.Unlock()
	s.setCookie(w, "mariner_session", id, 43200)
}
func (s *Service) Current(r *http.Request) (Session, string, bool) {
	id := s.cookie(r, "mariner_session")
	s.mu.RLock()
	session, ok := s.sessions[id]
	s.mu.RUnlock()
	return session, id, ok && time.Now().Before(session.Expires)
}
func (s *Service) SetPassword(id, password string) {
	s.mu.Lock()
	session := s.sessions[id]
	session.Password = password
	s.sessions[id] = session
	s.mu.Unlock()
}
func (s *Service) Lock(id string) { s.SetPassword(id, "") }
func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {
	id := s.cookie(r, "mariner_session")
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
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
func (s *Service) setCookie(w http.ResponseWriter, name, value string, age int) {
	mac := hmac.New(sha256.New, []byte(s.CookieSecret))
	mac.Write([]byte(value))
	signed := value + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{Name: name, Value: signed, Path: "/", MaxAge: age, HttpOnly: true, SameSite: http.SameSiteLaxMode})
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
