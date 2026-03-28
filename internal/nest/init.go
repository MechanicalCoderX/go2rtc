package nest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AlexxIT/go2rtc/internal/api"
	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/nest"
)

// nestAuthBase is the Google SDM authorization URL template (project_id is interpolated).
const nestAuthBase = "https://nestservices.google.com/partnerconnections/%s/auth"

// pendingAuth holds the OAuth credentials for an in-flight authorization request.
type pendingAuth struct {
	ClientID     string
	ClientSecret string
	ProjectID    string
	RedirectURI  string
	created      time.Time
}

var (
	pendingMu sync.Mutex
	pending   = map[string]pendingAuth{} // state → credentials

	resultsMu sync.Mutex
	results   = map[string][]*api.Source{} // state → discovered sources
)

func Init() {
	streams.HandleFunc("nest", func(source string) (core.Producer, error) {
		return nest.Dial(source)
	})

	api.HandleFunc("api/nest", apiNest)
	api.HandleFunc("api/nest/callback", apiNestCallback)
	api.HandleFunc("api/nest/status", apiNestStatus)

	go cleanupExpired()
}

// apiNest handles two cases:
//
//  1. refresh_token provided → existing direct flow (backward compatible).
//     Returns sources JSON immediately.
//
//  2. No refresh_token → OAuth2 authorization code flow.
//     Returns {"authorize_url": "...", "state": "..."}.
//     Frontend opens the URL in a popup; after the user authenticates,
//     poll api/nest/status?state=... until sources appear.
func apiNest(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	clientID := query.Get("client_id")
	clientSecret := query.Get("client_secret")
	refreshToken := query.Get("refresh_token")
	projectID := query.Get("project_id")

	// --- Backward-compatible path: refresh_token already known ---
	if refreshToken != "" {
		nestAPI, err := nest.NewAPI(clientID, clientSecret, refreshToken)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		respondSources(w, nestAPI, projectID, query)
		return
	}

	// --- OAuth2 path: initiate authorization code flow ---
	if clientID == "" || clientSecret == "" || projectID == "" {
		http.Error(w, "client_id, client_secret, and project_id are required", http.StatusBadRequest)
		return
	}

	state, err := newState()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	redirectURI := fmt.Sprintf("%s://%s/api/nest/callback", scheme, r.Host)

	pendingMu.Lock()
	pending[state] = pendingAuth{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		ProjectID:    projectID,
		RedirectURI:  redirectURI,
		created:      time.Now(),
	}
	pendingMu.Unlock()

	params := url.Values{
		"redirect_uri":  {redirectURI},
		"access_type":   {"offline"},
		"response_type": {"code"},
		"scope":         {"https://www.googleapis.com/auth/sdm.service"},
		"client_id":     {clientID},
		"state":         {state},
	}
	authURL := fmt.Sprintf(nestAuthBase, projectID) + "?" + params.Encode()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"authorize_url": authURL,
		"state":         state,
		"redirect_uri":  redirectURI,
	})
}

// apiNestCallback is the OAuth2 redirect URI handler. Google sends the user
// here with ?code=...&state=... after a successful login. It exchanges the
// code for a refresh token, lists devices, stores the sources under the state
// key, then closes the popup.
func apiNestCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	state := query.Get("state")
	code := query.Get("code")
	errParam := query.Get("error")

	closePopup := func(msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w,
			`<!DOCTYPE html><html><body><p>%s</p>`+
				`<script>window.close()</script></body></html>`, msg)
	}

	if errParam != "" {
		closePopup("Authorization denied: " + errParam + ". You may close this window.")
		return
	}
	if state == "" || code == "" {
		http.Error(w, "missing state or code", http.StatusBadRequest)
		return
	}

	pendingMu.Lock()
	p, ok := pending[state]
	if ok {
		delete(pending, state)
	}
	pendingMu.Unlock()

	if !ok {
		closePopup("Session expired or not found. Please try again.")
		return
	}

	refreshToken, err := nest.ExchangeAuthCode(p.ClientID, p.ClientSecret, code, p.RedirectURI)
	if err != nil {
		closePopup("Failed to exchange authorization code: " + err.Error())
		return
	}

	nestAPI, err := nest.NewAPI(p.ClientID, p.ClientSecret, refreshToken)
	if err != nil {
		closePopup("Failed to authenticate: " + err.Error())
		return
	}

	devices, err := nestAPI.GetDevices(p.ProjectID)
	if err != nil {
		closePopup("Failed to list devices: " + err.Error())
		return
	}

	q := url.Values{
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
		"refresh_token": {refreshToken},
		"project_id":    {p.ProjectID},
	}
	var items []*api.Source
	for _, device := range devices {
		q.Set("device_id", device.DeviceID)
		q.Set("protocols", strings.Join(device.Protocols, ","))
		items = append(items, &api.Source{
			Name: device.Name,
			URL:  "nest:?" + q.Encode(),
		})
	}

	resultsMu.Lock()
	results[state] = items
	resultsMu.Unlock()

	closePopup("Login successful! You may close this window.")
}

// apiNestStatus is polled by the frontend after opening the Google auth popup.
// Returns 204 while the OAuth flow is still in progress, or the sources JSON
// once the callback has completed.
func apiNestStatus(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" {
		http.Error(w, "missing state", http.StatusBadRequest)
		return
	}

	resultsMu.Lock()
	items, ok := results[state]
	if ok {
		delete(results, state)
	}
	resultsMu.Unlock()

	if !ok {
		w.WriteHeader(http.StatusNoContent) // 204: still waiting
		return
	}

	api.ResponseSources(w, items)
}

// respondSources lists devices for an already-authenticated API and writes the
// sources response. Used by both the direct (refresh_token) and OAuth paths.
func respondSources(w http.ResponseWriter, nestAPI *nest.API, projectID string, query url.Values) {
	devices, err := nestAPI.GetDevices(projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var items []*api.Source
	for _, device := range devices {
		query.Set("device_id", device.DeviceID)
		query.Set("protocols", strings.Join(device.Protocols, ","))
		items = append(items, &api.Source{
			Name: device.Name,
			URL:  "nest:?" + query.Encode(),
		})
	}

	api.ResponseSources(w, items)
}

// newState generates a cryptographically random 16-byte hex state string for
// CSRF protection in the OAuth flow.
func newState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// cleanupExpired removes stale pending and result entries older than 10 minutes.
func cleanupExpired() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		cutoff := time.Now().Add(-10 * time.Minute)

		pendingMu.Lock()
		for state, p := range pending {
			if p.created.Before(cutoff) {
				delete(pending, state)
			}
		}
		pendingMu.Unlock()

		// Results are consumed on first poll (every 500ms in the UI), so any
		// entry surviving a 5-minute cleanup cycle was never claimed — clear it.
		resultsMu.Lock()
		for state := range results {
			delete(results, state)
		}
		resultsMu.Unlock()
	}
}
