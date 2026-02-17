package round

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const minNum, maxNum = 1, 10

// secureIntn returns a uniform random int in [0, n) using crypto/rand (CSPRNG).
func secureIntn(n int) int {
	if n <= 0 {
		return 0
	}
	max := big.NewInt(int64(n))
	v, err := rand.Int(rand.Reader, max)
	if err != nil {
		return 0
	}
	return int(v.Int64())
}

// Round holds state for one game round (e.g. Hi/Lo). Persisted to JSON.
type Round struct {
	RoundID       string    `json:"roundId"`
	BetID         string    `json:"betId"`
	Currency      string    `json:"currency"`
	Amount        float64   `json:"amount"`
	CurrentNumber int       `json:"currentNumber"`
	CreatedAt     time.Time `json:"createdAt"`
}

// NextNumber returns a random number in [minNum, maxNum] for Hi/Lo.
// Uses cryptographically secure RNG (crypto/rand) for fair, unpredictable outcomes.
func NextNumber() int {
	size := maxNum - minNum + 1
	return minNum + secureIntn(size)
}

// Store holds active rounds and persists to rounds.json (same style as platform data/*.json).
type Store struct {
	mu      sync.Mutex
	rounds  map[string]*Round
	dataDir string
}

func NewStore(dataDir string) *Store {
	if dataDir == "" {
		dataDir = "data"
	}
	s := &Store{
		rounds:  make(map[string]*Round),
		dataDir: dataDir,
	}
	s.load()
	return s
}

func (s *Store) roundsPath() string {
	return filepath.Join(s.dataDir, "rounds.json")
}

func (s *Store) ensureDir() error {
	return os.MkdirAll(s.dataDir, 0755)
}

func (s *Store) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.roundsPath())
	if err != nil {
		if !os.IsNotExist(err) {
			// log or ignore
		}
		return
	}
	var list []*Round
	if err := json.Unmarshal(data, &list); err != nil {
		return
	}
	for _, r := range list {
		if r != nil && r.RoundID != "" {
			s.rounds[r.RoundID] = r
		}
	}
}

func (s *Store) save() error {
	list := make([]*Round, 0, len(s.rounds))
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
	return os.WriteFile(s.roundsPath(), data, 0644)
}

func (s *Store) Create(roundID, betID, currency string, amount float64) *Round {
	num := NextNumber()
	r := &Round{
		RoundID:       roundID,
		BetID:         betID,
		Currency:      currency,
		Amount:        amount,
		CurrentNumber: num,
		CreatedAt:     time.Now(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rounds[roundID] = r
	_ = s.save()
	return r
}

func (s *Store) Get(roundID string) (*Round, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rounds[roundID]
	return r, ok
}

func (s *Store) Delete(roundID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rounds, roundID)
	_ = s.save()
}
