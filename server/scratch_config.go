package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	rgsdb "github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server"
)

// ScratchMechanic defines the mechanic and grid for a scratch game.
type ScratchMechanic struct {
	Type       string `json:"type"`        // "match_n", "target_match", "symbol_hunt"
	MatchCount int    `json:"match_count"` // for match_n
	Rows       int    `json:"rows"`
	Cols       int    `json:"cols"`
}

// ScratchSymbol describes a symbol used in a scratch game grid.
type ScratchSymbol struct {
	ID       string `json:"id"`
	Category string `json:"category"` // "win", "dud", "top", etc.
	Image    string `json:"image"`    // image path or URL (for symbol API / frontend)
}

// ScratchConfig is the full per-game config loaded from the DB.
type ScratchConfig struct {
	GameID   string          `json:"game_id"`
	Mechanic ScratchMechanic `json:"mechanic"`
	Symbols  []ScratchSymbol `json:"symbols"`
}

// loadScratchConfigFromDB loads scratch config for a single game_id from scratch_games.
func loadScratchConfigFromDB(gameID string) (*ScratchConfig, error) {
	db, err := rgsdb.GetDB()
	if err != nil || db == nil {
		return nil, fmt.Errorf("no db: %w", err)
	}
	var mechJSON, symJSON []byte
	err = db.QueryRowContext(context.Background(), `
		SELECT mechanic, symbol_config
		FROM scratch_games
		WHERE game_id = $1
	`, gameID).Scan(&mechJSON, &symJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var mech ScratchMechanic
	if err := json.Unmarshal(mechJSON, &mech); err != nil {
		return nil, err
	}
	var wrapper struct {
		Symbols []ScratchSymbol `json:"symbols"`
	}
	if err := json.Unmarshal(symJSON, &wrapper); err != nil {
		return nil, err
	}
	return &ScratchConfig{
		GameID:   gameID,
		Mechanic: mech,
		Symbols:  wrapper.Symbols,
	}, nil
}
