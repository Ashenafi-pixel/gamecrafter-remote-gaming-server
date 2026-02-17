package gamemath

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Store persists game math by model_id.
type Store struct {
	mu      sync.RWMutex
	math    map[string]*GameMath
	dataDir string
}

func NewStore(dataDir string) *Store {
	if dataDir == "" {
		dataDir = "data"
	}
	s := &Store{
		math:    make(map[string]*GameMath),
		dataDir: dataDir,
	}
	s.load()
	return s
}

func (s *Store) path() string {
	return filepath.Join(s.dataDir, "game_math.json")
}

func (s *Store) ensureDir() error {
	return os.MkdirAll(s.dataDir, 0755)
}

type storedEntry struct {
	ModelID string    `json:"model_id"`
	Math    *GameMath `json:"math"`
}

func (s *Store) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path())
	if err != nil {
		return
	}
	var list []storedEntry
	if err := json.Unmarshal(data, &list); err != nil {
		return
	}
	for _, e := range list {
		if e.ModelID != "" && e.Math != nil {
			s.math[e.ModelID] = e.Math
		}
	}
}

// saveLocked writes the store to disk. Caller must hold s.mu.
func (s *Store) saveLocked() error {
	list := make([]storedEntry, 0, len(s.math))
	for id, m := range s.math {
		list = append(list, storedEntry{ModelID: id, Math: m})
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dataDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(s.path(), data, 0644)
}

// Register stores game math by its model_id. Overwrites if exists.
func (s *Store) Register(math *GameMath) error {
	if math == nil || math.ModelID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.math[math.ModelID] = math
	return s.saveLocked()
}

// Get returns game math for the given model_id, or nil.
func (s *Store) Get(modelID string) *GameMath {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.math[modelID]
	if !ok {
		return nil
	}
	return m
}
