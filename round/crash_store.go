package round

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CrashRound holds state for one crash game round.
type CrashRound struct {
	RoundID   string    `json:"roundId"`
	BetID     string    `json:"betId"`
	Currency  string    `json:"currency"`
	Amount    float64   `json:"amount"`
	CrashStep int       `json:"crashStep"`
	StartedAt time.Time `json:"startedAt"`
	Settled   bool      `json:"settled"`
}

// CrashStore persists active crash rounds.
type CrashStore struct {
	mu      sync.Mutex
	rounds  map[string]*CrashRound
	dataDir string
}

func NewCrashStore(dataDir string) *CrashStore {
	if dataDir == "" {
		dataDir = "data"
	}
	s := &CrashStore{
		rounds:  make(map[string]*CrashRound),
		dataDir: dataDir,
	}
	s.load()
	return s
}

func (s *CrashStore) path() string {
	return filepath.Join(s.dataDir, "crash_rounds.json")
}

func (s *CrashStore) ensureDir() error {
	return os.MkdirAll(s.dataDir, 0755)
}

func (s *CrashStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path())
	if err != nil {
		return
	}
	var list []*CrashRound
	if err := json.Unmarshal(data, &list); err != nil {
		return
	}
	for _, r := range list {
		if r != nil && r.RoundID != "" && !r.Settled {
			s.rounds[r.RoundID] = r
		}
	}
}

func (s *CrashStore) save() error {
	list := make([]*CrashRound, 0, len(s.rounds))
	for _, r := range s.rounds {
		list = append(list, r)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := s.ensureDir(); err != nil {
		return err
	}
	return os.WriteFile(s.path(), data, 0644)
}

func (s *CrashStore) Create(roundID, betID, currency string, amount float64, crashStep int) *CrashRound {
	r := &CrashRound{
		RoundID:   roundID,
		BetID:     betID,
		Currency:  currency,
		Amount:    amount,
		CrashStep: crashStep,
		StartedAt: time.Now(),
		Settled:   false,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rounds[roundID] = r
	_ = s.save()
	return r
}

func (s *CrashStore) Get(roundID string) (*CrashRound, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rounds[roundID]
	return r, ok
}

func (s *CrashStore) Settle(roundID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.rounds[roundID]; ok {
		r.Settled = true
		_ = s.save()
	}
}

func (s *CrashStore) Delete(roundID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rounds, roundID)
	_ = s.save()
}
