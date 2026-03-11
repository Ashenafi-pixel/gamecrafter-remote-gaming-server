package server

import (
	"context"
	"encoding/json"
	"log"

	rgsdb "github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server"
	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/gamemath"
)

// loadGameMathFromDB loads ACTIVE game math definitions from the game_math table (if it exists)
// and registers them into the in-memory gamemath.Store.
func (s *Server) loadGameMathFromDB() {
	db, err := rgsdb.GetDB()
	if err != nil || db == nil {
		return
	}
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, `
		SELECT model_id, math
		FROM game_math
		WHERE status = 'ACTIVE'
	`)
	if err != nil {
		// Table might not exist yet; fail silently in that case.
		log.Printf("game_math: query failed (may be missing table): %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var modelID string
		var mathJSON []byte
		if err := rows.Scan(&modelID, &mathJSON); err != nil {
			log.Printf("game_math: scan row: %v", err)
			continue
		}
		if len(mathJSON) == 0 || modelID == "" {
			continue
		}
		var gm gamemath.GameMath
		if err := json.Unmarshal(mathJSON, &gm); err != nil {
			log.Printf("game_math: unmarshal math for model_id=%s: %v", modelID, err)
			continue
		}
		// Ensure ModelID is set consistently.
		if gm.ModelID == "" {
			gm.ModelID = modelID
		}
		if err := s.gameMath.Register(&gm); err != nil {
			log.Printf("game_math: register model_id=%s: %v", modelID, err)
		}
	}
}
