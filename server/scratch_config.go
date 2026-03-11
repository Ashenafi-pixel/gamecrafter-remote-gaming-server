package server

import (
	"context"
	"encoding/json"
	"log"

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

// loadScratchConfigs loads scratch game configs from scratch_games into memory.
func (s *Server) loadScratchConfigs() {
	db, err := rgsdb.GetDB()
	if err != nil || db == nil {
		return
	}
	rows, err := db.QueryContext(context.Background(), `
		SELECT game_id, mechanic, symbol_config
		FROM scratch_games
	`)
	if err != nil {
		log.Printf("scratch config: query failed: %v", err)
		return
	}
	defer rows.Close()

	if s.scratchConfigs == nil {
		s.scratchConfigs = make(map[string]*ScratchConfig)
	}

	for rows.Next() {
		var gameID string
		var mechJSON, symJSON []byte
		if err := rows.Scan(&gameID, &mechJSON, &symJSON); err != nil {
			log.Printf("scratch config: scan: %v", err)
			continue
		}
		var mech ScratchMechanic
		if err := json.Unmarshal(mechJSON, &mech); err != nil {
			log.Printf("scratch config: bad mechanic for %s: %v", gameID, err)
			continue
		}
		var wrapper struct {
			Symbols []ScratchSymbol `json:"symbols"`
		}
		if err := json.Unmarshal(symJSON, &wrapper); err != nil {
			log.Printf("scratch config: bad symbols for %s: %v", gameID, err)
			continue
		}
		s.scratchConfigs[gameID] = &ScratchConfig{
			GameID:   gameID,
			Mechanic: mech,
			Symbols:  wrapper.Symbols,
		}
	}
}
