package gamemath

import (
	"testing"
)

func TestPickTier_NilOrEmpty(t *testing.T) {
	var g *GameMath
	if _, ok := g.PickTier(); ok {
		t.Fatal("nil GameMath should return false")
	}
	g = &GameMath{PrizeTable: nil}
	if _, ok := g.PickTier(); ok {
		t.Fatal("nil prize table should return false")
	}
	g = &GameMath{PrizeTable: []PrizeTier{}}
	if _, ok := g.PickTier(); ok {
		t.Fatal("empty prize table should return false")
	}
	g = &GameMath{PrizeTable: []PrizeTier{{Tier: "LOSE", Multiplier: 0, Weight: 0}}}
	if _, ok := g.PickTier(); ok {
		t.Fatal("all-zero weights should return false")
	}
}

func TestPickTier_SingleTier(t *testing.T) {
	g := &GameMath{
		PrizeTable: []PrizeTier{{Tier: "T1", Multiplier: 2, Weight: 100}},
	}
	for i := 0; i < 20; i++ {
		tier, ok := g.PickTier()
		if !ok {
			t.Fatal("single tier with weight should return true")
		}
		if tier.Tier != "T1" || tier.Multiplier != 2 {
			t.Errorf("got tier %q mult %v", tier.Tier, tier.Multiplier)
		}
	}
}

func TestPickTier_Distribution(t *testing.T) {
	// Weights: LOSE 70%, T1 20%, T2 10% (out of 100)
	g := &GameMath{
		PrizeTable: []PrizeTier{
			{Tier: "LOSE", Multiplier: 0, Weight: 70},
			{Tier: "T1", Multiplier: 2, Weight: 20},
			{Tier: "T2", Multiplier: 5, Weight: 10},
		},
	}
	const rounds = 100_000
	count := map[string]int{}
	for i := 0; i < rounds; i++ {
		tier, ok := g.PickTier()
		if !ok {
			t.Fatal("PickTier failed")
		}
		count[tier.Tier]++
	}
	tol := 0.02 // 2% tolerance
	if p := float64(count["LOSE"]) / rounds; p < 0.68 || p > 0.72 {
		t.Errorf("LOSE proportion %.4f want ~0.70 (tol ±%.0f%%)", p, tol*100)
	}
	if p := float64(count["T1"]) / rounds; p < 0.18 || p > 0.22 {
		t.Errorf("T1 proportion %.4f want ~0.20 (tol ±%.0f%%)", p, tol*100)
	}
	if p := float64(count["T2"]) / rounds; p < 0.08 || p > 0.12 {
		t.Errorf("T2 proportion %.4f want ~0.10 (tol ±%.0f%%)", p, tol*100)
	}
}

func TestPickTier_SkipsZeroWeight(t *testing.T) {
	g := &GameMath{
		PrizeTable: []PrizeTier{
			{Tier: "A", Multiplier: 1, Weight: 0},
			{Tier: "B", Multiplier: 2, Weight: 100},
		},
	}
	for i := 0; i < 50; i++ {
		tier, ok := g.PickTier()
		if !ok {
			t.Fatal("PickTier failed")
		}
		if tier.Tier != "B" {
			t.Errorf("expected only B, got %q", tier.Tier)
		}
	}
}

func TestPickTier_ScratchMatch3Weights(t *testing.T) {
	// Real-world style prize table (scratch_match3)
	g := &GameMath{
		ModelID: "scratch_match3",
		PrizeTable: []PrizeTier{
			{Tier: "LOSE", Multiplier: 0, Weight: 824414},
			{Tier: "T1", Multiplier: 2, Weight: 164798},
			{Tier: "T2", Multiplier: 5, Weight: 10646},
			{Tier: "T3", Multiplier: 20, Weight: 142},
			{Tier: "T4", Multiplier: 100, Weight: 2},
		},
	}
	const rounds = 200_000
	count := map[string]int{}
	for i := 0; i < rounds; i++ {
		tier, ok := g.PickTier()
		if !ok {
			t.Fatal("PickTier failed")
		}
		count[tier.Tier]++
	}
	total := int64(824414 + 164798 + 10646 + 142 + 2)
	expect := map[string]float64{
		"LOSE": float64(824414) / float64(total),
		"T1":   float64(164798) / float64(total),
		"T2":   float64(10646) / float64(total),
		"T3":   float64(142) / float64(total),
		"T4":   float64(2) / float64(total),
	}
	tol := 0.015
	for tier, wantP := range expect {
		gotP := float64(count[tier]) / float64(rounds)
		if gotP < wantP-tol || gotP > wantP+tol {
			t.Errorf("tier %q: proportion %.4f want ~%.4f (tol ±%.0f%%)", tier, gotP, wantP, tol*100)
		}
	}
}
