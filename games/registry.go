package games

import (
	"fmt"
	rgsdb "github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server"
	"net/url"
	"sync"
)

type Registry struct {
	mu        sync.RWMutex
	providers map[string]*Provider
}

type Provider struct {
	ID    string   `json:"id"`
	Games []string `json:"games"`
}

func NewRegistry() *Registry {
	r := &Registry{providers: make(map[string]*Provider)}
	_ = loadFromDB(r)
	return r
}

func (r *Registry) Register(providerID string, gameIDs []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[providerID] = &Provider{ID: providerID, Games: gameIDs}
}

func (r *Registry) HasGame(providerID, gameID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[providerID]
	if !ok {
		return false
	}
	for _, g := range p.Games {
		if g == gameID {
			return true
		}
	}
	return false
}

func (r *Registry) ListProviders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.providers))
	for id := range r.providers {
		out = append(out, id)
	}
	return out
}

func (r *Registry) ListGames(providerID string) ([]string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[providerID]
	if !ok {
		return nil, false
	}
	return p.Games, true
}

func (r *Registry) GetLaunchURL(baseURL, providerID, gameID, token, lang, currency string) (string, error) {
	if !r.HasGame(providerID, gameID) {
		return "", fmt.Errorf("unknown game %s for provider %s", gameID, providerID)
	}
	// Game page path: /rgs/game/<gameID>. Query params encoded to avoid injection and broken URLs.
	path := "/rgs/game/" + gameID
	u := baseURL + path
	sep := "?"
	if token != "" {
		u += sep + "token=" + url.QueryEscape(token)
		sep = "&"
	}
	if lang != "" {
		u += sep + "lang=" + url.QueryEscape(lang)
		sep = "&"
	}
	if currency != "" {
		u += sep + "currency=" + url.QueryEscape(currency)
		sep = "&"
	}
	return u, nil
}

// loadFromDB loads the game catalog from the game_crafter games table (container DB).
// Uses: provider, game_id, status, enabled. Empty provider is treated as "default".
func loadFromDB(r *Registry) error {
	db, err := rgsdb.GetDB()
	if err != nil {
		return err
	}
	if db == nil {
		return fmt.Errorf("no db")
	}
	rows, err := db.Query(`SELECT COALESCE(NULLIF(TRIM(provider), ''), 'default') AS provider, game_id, status, enabled FROM games WHERE status = 'ACTIVE' AND enabled = true`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		provider string
		code     string
		status   string
		enabled  bool
	}
	var list []row
	for rows.Next() {
		var p, c, s string
		var e bool
		if err := rows.Scan(&p, &c, &s, &e); err != nil {
			return err
		}
		if c == "" {
			continue
		}
		list = append(list, row{provider: p, code: c, status: s, enabled: e})
	}
	m := make(map[string][]string)
	for _, g := range list {
		m[g.provider] = append(m[g.provider], g.code)
	}
	for pid, games := range m {
		r.Register(pid, games)
	}
	return nil
}
