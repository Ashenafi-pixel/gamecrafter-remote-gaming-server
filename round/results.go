package round

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Result records a settled round for audit (same style as platform transactions.json).
// Symbols and WinAmount are used for scratch (instant) games for idempotent replay.
type Result struct {
	RoundID      string    `json:"roundId"`
	BetID        string    `json:"betId"`
	Outcome      string    `json:"outcome"` // "win", "lose", "push"
	NextNumber   int       `json:"nextNumber"`
	BalanceDelta float64   `json:"balanceDelta"`
	SettledAt    time.Time `json:"settledAt"`
	// Scratch (optional): for idempotent round/start replay
	Symbols   []string  `json:"symbols,omitempty"`
	WinAmount float64  `json:"winAmount,omitempty"`
}

// ResultsStore appends settled round results to data/round_results.json.
type ResultsStore struct {
	mu      sync.Mutex
	dataDir string
}

func NewResultsStore(dataDir string) *ResultsStore {
	if dataDir == "" {
		dataDir = "data"
	}
	return &ResultsStore{dataDir: dataDir}
}

func (rs *ResultsStore) path() string {
	return filepath.Join(rs.dataDir, "round_results.json")
}

func (rs *ResultsStore) ensureDir() error {
	return os.MkdirAll(rs.dataDir, 0755)
}

// Append adds a settled round result to the JSON file (append to array, same as platform ledger).
func (rs *ResultsStore) Append(r *Result) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if err := rs.ensureDir(); err != nil {
		return err
	}
	path := rs.path()
	var list []*Result
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &list)
	}
	if list == nil {
		list = []*Result{}
	}
	list = append(list, r)
	data, err = json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// GetByRoundID returns a settled result by round ID if found (for idempotent replay).
func (rs *ResultsStore) GetByRoundID(roundID string) (*Result, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if err := rs.ensureDir(); err != nil {
		return nil, err
	}
	path := rs.path()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var list []*Result
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].RoundID == roundID {
			return list[i], nil
		}
	}
	return nil, nil
}
