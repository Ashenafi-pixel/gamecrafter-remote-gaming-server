package gamemath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_RegisterGet(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	math := &GameMath{
		ModelID:   "scratch_match3",
		PrizeTable: []PrizeTier{{Tier: "LOSE", Multiplier: 0, Weight: 100}},
	}
	if err := s.Register(math); err != nil {
		t.Fatal(err)
	}
	got := s.Get("scratch_match3")
	if got == nil {
		t.Fatal("Get scratch_match3 returned nil")
	}
	if got.ModelID != "scratch_match3" || len(got.PrizeTable) != 1 {
		t.Errorf("got %+v", got)
	}
	if s.Get("nonexistent") != nil {
		t.Error("Get nonexistent should return nil")
	}
}

func TestStore_RegisterOverwrite(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	s.Register(&GameMath{ModelID: "m1", PrizeTable: []PrizeTier{{Tier: "A", Weight: 1}}})
	s.Register(&GameMath{ModelID: "m1", PrizeTable: []PrizeTier{{Tier: "B", Weight: 2}}})
	got := s.Get("m1")
	if got == nil || got.PrizeTable[0].Tier != "B" {
		t.Errorf("expected overwrite: %+v", got)
	}
}

func TestStore_Persistence(t *testing.T) {
	dir := t.TempDir()

	math := &GameMath{
		ModelID: "persist_test",
		PrizeTable: []PrizeTier{
			{Tier: "LOSE", Multiplier: 0, Weight: 50},
			{Tier: "WIN", Multiplier: 2, Weight: 50},
		},
	}
	s1 := NewStore(dir)
	if err := s1.Register(math); err != nil {
		t.Fatal(err)
	}

	s2 := NewStore(dir)
	got := s2.Get("persist_test")
	if got == nil {
		t.Fatal("after reload, Get returned nil")
	}
	if len(got.PrizeTable) != 2 || got.PrizeTable[0].Tier != "LOSE" {
		t.Errorf("reloaded math: %+v", got)
	}
}

func TestStore_RegisterNilOrEmptyModelID(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	if err := s.Register(nil); err != nil {
		t.Errorf("Register(nil) should not error: %v", err)
	}
	if err := s.Register(&GameMath{ModelID: "", PrizeTable: []PrizeTier{{Weight: 1}}}); err != nil {
		t.Errorf("Register(empty model_id) should not error: %v", err)
	}
	if s.Get("") != nil {
		t.Error("Get empty string should return nil")
	}
}

func TestStore_LoadFromFile(t *testing.T) {
	// Write using store then load in a fresh store to verify file format round-trip
	dir := t.TempDir()
	s1 := NewStore(dir)
	math := &GameMath{
		ModelID: "from_file",
		PrizeTable: []PrizeTier{{Tier: "X", Multiplier: 1, Weight: 10}},
	}
	if err := s1.Register(math); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "game_math.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "game_math.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	s2 := NewStore(dir2)
	got := s2.Get("from_file")
	if got == nil {
		t.Fatal("load from file: Get returned nil")
	}
	if got.ModelID != "from_file" || len(got.PrizeTable) != 1 || got.PrizeTable[0].Tier != "X" {
		t.Errorf("loaded: %+v", got)
	}
}
