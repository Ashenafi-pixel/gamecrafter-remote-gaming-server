package gamemath

import (
	"crypto/rand"
	"math/big"
)

// GameMath is the stored game math payload (schema_version 1).
type GameMath struct {
	SchemaVersion int          `json:"schema_version"`
	ModelID       string       `json:"model_id"`
	ModelVersion  string       `json:"model_version"`
	Mechanic      Mechanic     `json:"mechanic"`
	MathMode      string       `json:"math_mode"`
	WinLogic      string       `json:"win_logic"`
	PrizeTable    []PrizeTier  `json:"prize_table"`
	Stats         *GameStats   `json:"stats,omitempty"`
	Integrity     *Integrity   `json:"integrity,omitempty"`
}

type Mechanic struct {
	Type       string `json:"type"`
	MatchCount int    `json:"match_count,omitempty"`
}

type PrizeTier struct {
	Tier       string  `json:"tier"`
	Multiplier float64 `json:"multiplier"`
	Weight     int64   `json:"weight"`
}

type GameStats struct {
	ComputedRTP float64 `json:"computed_rtp"`
	HitRate     float64 `json:"hit_rate"`
	Variance    float64 `json:"variance"`
}

type Integrity struct {
	ContentHash string `json:"content_hash"`
}

// PickTier selects a tier from the prize table by weight using CSPRNG.
// Returns the chosen PrizeTier and true, or zero value and false if table is empty/invalid.
func (g *GameMath) PickTier() (PrizeTier, bool) {
	if g == nil || len(g.PrizeTable) == 0 {
		return PrizeTier{}, false
	}
	var total int64
	for _, t := range g.PrizeTable {
		if t.Weight <= 0 {
			continue
		}
		total += t.Weight
	}
	if total <= 0 {
		return PrizeTier{}, false
	}
	max := big.NewInt(total)
	v, err := rand.Int(rand.Reader, max)
	if err != nil {
		return PrizeTier{}, false
	}
	idx := v.Int64()
	var cum int64
	for i := range g.PrizeTable {
		t := &g.PrizeTable[i]
		if t.Weight <= 0 {
			continue
		}
		cum += t.Weight
		if idx < cum {
			return *t, true
		}
	}
	return g.PrizeTable[len(g.PrizeTable)-1], true
}
