package scratch

import (
	"crypto/rand"
	"math/big"

	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/gamemath"
)

// Symbol is a scratch card symbol.
const (
	SymbolCherry = "cherry"
	SymbolLemon  = "lemon"
	SymbolStar   = "star"
	SymbolSeven  = "seven"
)

var symbols = []string{SymbolCherry, SymbolLemon, SymbolStar, SymbolSeven}

// Outcome is the result of a scratch round (3 symbols, win amount).
type Outcome struct {
	Symbols   [3]string `json:"symbols"`
	WinAmount float64   `json:"winAmount"`
	Match     bool      `json:"match"` // true if all 3 match
	Tier      string    `json:"tier,omitempty"`
}

// Multiplier when 3 match (legacy fallback).
const WinMultiplier = 2.0

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

// Generate produces 3 random symbols and win amount using legacy logic (4 symbols, match 3 = 2x).
func Generate(betAmount float64) Outcome {
	var s [3]string
	for i := range s {
		s[i] = symbols[secureIntn(len(symbols))]
	}
	match := s[0] == s[1] && s[1] == s[2]
	winAmount := 0.0
	if match {
		winAmount = betAmount * WinMultiplier
	}
	return Outcome{
		Symbols:   s,
		WinAmount: winAmount,
		Match:     match,
	}
}

// GenerateWithMath produces outcome from stored game math: weighted tier selection, then multiplier * bet.
// For display: LOSE = 3 different symbols; WIN tier = 3 same symbol.
func GenerateWithMath(betAmount float64, math *gamemath.GameMath) (Outcome, bool) {
	if math == nil {
		return Outcome{}, false
	}
	tier, ok := math.PickTier()
	if !ok {
		return Outcome{}, false
	}
	winAmount := betAmount * tier.Multiplier
	var s [3]string
	if tier.Tier == "LOSE" || tier.Multiplier == 0 {
		for i := range s {
			s[i] = symbols[secureIntn(len(symbols))]
		}
		for s[0] == s[1] && s[1] == s[2] {
			s[2] = symbols[secureIntn(len(symbols))]
		}
	} else {
		sym := symbols[secureIntn(len(symbols))]
		s[0], s[1], s[2] = sym, sym, sym
	}
	return Outcome{
		Symbols:   s,
		WinAmount: winAmount,
		Match:     winAmount > 0,
		Tier:      tier.Tier,
	}, true
}
