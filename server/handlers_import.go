package server

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	rgsdb "github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server"
	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/gamemath"
)

// importZipRequest is the structure used only for logging / JSON responses.
// The actual ZIP payload is the raw HTTP body.
type importZipResponse struct {
	OK      bool   `json:"ok"`
	GameID  string `json:"game_id,omitempty"`
	Message string `json:"message,omitempty"`
}

// projectScratchJSON is the shape of project_scratch.json in the bundle (displayName, gameId).
type projectScratchJSON struct {
	DisplayName string `json:"displayName"`
	GameID      string `json:"gameId"`
}

// handleImportZip accepts a ZIP containing a standalone bundle exported from GameCrafter
// (index.html + assets + math.json, project_scratch.json, etc.). The DB generates a unique
// numeric game_id (sequence). Name and internal_name come from project_scratch.json
// (displayName → name, gameId → internal_name). integration_partner is set to "GameCrafter".
//
// Request:
//
//	POST /rgs/admin/games/import-zip
//	Content-Type: application/zip (body is the ZIP bytes)
func (s *Server) handleImportZip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, importZipResponse{
			OK:      false,
			Message: "method not allowed",
		})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, importZipResponse{
			OK:      false,
			Message: fmt.Sprintf("failed to read body: %v", err),
		})
		return
	}
	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, importZipResponse{
			OK:      false,
			Message: "empty request body",
		})
		return
	}

	// Name and internal_name from project_scratch.json in the ZIP
	var displayName, internalName string
	if proj, err := parseProjectScratchFromZip(body); err == nil {
		displayName = strings.TrimSpace(proj.DisplayName)
		internalName = strings.TrimSpace(proj.GameID)
	}
	if displayName == "" {
		displayName = "Imported Game"
	}
	if internalName == "" {
		internalName = "imported"
	}

	// game_id: always DB-generated numeric (sequence)
	gameID, err := s.nextNumericGameID(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, importZipResponse{
			OK:      false,
			Message: fmt.Sprintf("failed to generate game_id: %v", err),
		})
		return
	}

	if err := s.extractGameBundleZip(gameID, body); err != nil {
		writeJSON(w, http.StatusBadRequest, importZipResponse{
			OK:      false,
			GameID:  gameID,
			Message: err.Error(),
		})
		return
	}

	// Upsert into game_crafter games table: name=displayName, internal_name=internalName, integration_partner=GameCrafter
	if err := s.upsertGameInDB(r.Context(), gameID, displayName, internalName, "default", "GameCrafter"); err != nil {
		log.Printf("import: game_id=%s upsert games table: %v", gameID, err)
	}

	for _, providerID := range s.registry.ListProviders() {
		games, _ := s.registry.ListGames(providerID)
		found := false
		for _, g := range games {
			if g == gameID {
				found = true
				break
			}
		}
		if !found {
			s.registry.Register(providerID, append(games, gameID))
		}
	}

	writeJSON(w, http.StatusOK, importZipResponse{
		OK:      true,
		GameID:  gameID,
		Message: "bundle imported",
	})
}

// parseProjectScratchFromZip finds project_scratch.json in the ZIP and returns displayName and gameId.
func parseProjectScratchFromZip(zipBytes []byte) (*projectScratchJSON, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, err
	}
	var found *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(strings.ReplaceAll(f.Name, "\\", "/"))
		if base == "project_scratch.json" {
			found = f
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("project_scratch.json not found in zip")
	}
	rc, err := found.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	var proj projectScratchJSON
	if err := json.Unmarshal(data, &proj); err != nil {
		return nil, err
	}
	return &proj, nil
}

// nextNumericGameID returns the next value from game_numeric_id_seq (DB-generated unique numeric game_id).
// Ensures the sequence exists in the connected DB (so RGS works with container or any Postgres).
func (s *Server) nextNumericGameID(ctx context.Context) (string, error) {
	db, err := rgsdb.GetDB()
	if err != nil || db == nil {
		return "", fmt.Errorf("no db: %w", err)
	}
	_, _ = db.ExecContext(ctx, "CREATE SEQUENCE IF NOT EXISTS game_numeric_id_seq START WITH 130300000")
	var next int64
	err = db.QueryRowContext(ctx, "SELECT nextval('game_numeric_id_seq')").Scan(&next)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", next), nil
}

// extractGameBundleZip extracts the given ZIP bytes into GamesDir/<game_id>/,
// replacing any existing files in that directory.
func (s *Server) extractGameBundleZip(gameID string, zipBytes []byte) error {
	readerAt := bytes.NewReader(zipBytes)
	zr, err := zip.NewReader(readerAt, int64(len(zipBytes)))
	if err != nil {
		return fmt.Errorf("invalid zip: %w", err)
	}

	targetRoot := filepath.Join(s.cfg.GamesDir, gameID)
	if err := os.RemoveAll(targetRoot); err != nil {
		return fmt.Errorf("clear existing bundle: %w", err)
	}
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return fmt.Errorf("create bundle dir: %w", err)
	}

	for _, f := range zr.File {
		// Skip directories; we create them as needed.
		if f.FileInfo().IsDir() {
			continue
		}

		relName := sanitizeBundlePath(f.Name)
		if relName == "" {
			continue
		}

		destPath := filepath.Join(targetRoot, relName)
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fmt.Errorf("mkdir for %s: %w", destPath, err)
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}
		dst, err := os.Create(destPath)
		if err != nil {
			rc.Close()
			return fmt.Errorf("create dest %s: %w", destPath, err)
		}
		if _, err := io.Copy(dst, rc); err != nil {
			rc.Close()
			dst.Close()
			return fmt.Errorf("copy %s: %w", f.Name, err)
		}
		rc.Close()
		dst.Close()
	}

	// Parse math.json and register game math keyed by game_id for round APIs.
	if err := s.registerBundleMath(gameID, targetRoot); err != nil {
		log.Printf("import: game_id=%s register math: %v (bundle still imported)", gameID, err)
	}
	return nil
}

// upsertGameInDB inserts or updates the game in the game_crafter games table (container DB).
// Uses: name, status, enabled, game_id, internal_name, provider, integration_partner.
func (s *Server) upsertGameInDB(ctx context.Context, gameID, displayName, internalName, provider, integrationPartner string) error {
	db, err := rgsdb.GetDB()
	if err != nil || db == nil {
		return fmt.Errorf("no db: %w", err)
	}
	if internalName == "" {
		internalName = gameID
	}
	var existingID string
	err = db.QueryRowContext(ctx, "SELECT id::text FROM games WHERE game_id = $1", gameID).Scan(&existingID)
	switch {
	case err == sql.ErrNoRows:
		_, err = db.ExecContext(ctx, `
      INSERT INTO games (name, status, enabled, game_id, internal_name, provider, integration_partner)
      VALUES ($1, 'ACTIVE', true, $2, $3, $4, $5)
    `, displayName, gameID, internalName, provider, integrationPartner)
		return err
	case err != nil:
		return err
	default:
		_, err = db.ExecContext(ctx, `
      UPDATE games SET name = $1, internal_name = $2, provider = $3, integration_partner = $4, updated_at = CURRENT_TIMESTAMP WHERE game_id = $5
    `, displayName, internalName, provider, integrationPartner, gameID)
		return err
	}
}

// sanitizeBundlePath normalises a path inside a ZIP to a safe, relative path under a bundle directory.
func sanitizeBundlePath(name string) string {
	clean := filepath.Clean(strings.TrimLeft(name, "./\\"))
	if clean == "" || clean == "." {
		return ""
	}
	// Prevent directory traversal.
	if strings.HasPrefix(clean, "..") || strings.Contains(clean, "../") || strings.Contains(clean, `..\`) {
		return ""
	}
	return clean
}

// rgsMathFile is the JSON shape of math.json produced by GameCrafter (RGS schema).
type rgsMathFile struct {
	SchemaVersion int                  `json:"schema_version"`
	ModelID       string               `json:"model_id"`
	ModelVersion  string               `json:"model_version"`
	Mechanic      rgsBundleMechanic    `json:"mechanic"`
	MathMode      string               `json:"math_mode"`
	WinLogic      string               `json:"win_logic"`
	PrizeTable    []rgsBundlePrizeTier `json:"prize_table"`
	Stats         *rgsBundleStats      `json:"stats,omitempty"`
	Integrity     *rgsBundleIntegrity  `json:"integrity,omitempty"`
}

type rgsBundleMechanic struct {
	Type            string      `json:"type"`
	MatchCount      int         `json:"match_count,omitempty"`
	GridSize        interface{} `json:"grid_size,omitempty"`
	FailProbability float64     `json:"fail_probability,omitempty"`
}

type rgsBundlePrizeTier struct {
	Tier        string  `json:"tier"`
	Multiplier  float64 `json:"multiplier"`
	Weight      float64 `json:"weight"` // may be float in JSON; we convert to int64
	Probability float64 `json:"probability,omitempty"`
}

type rgsBundleStats struct {
	ComputedRTP float64 `json:"computed_rtp"`
	HitRate     float64 `json:"hit_rate"`
	Variance    float64 `json:"variance"`
	MaxWin      float64 `json:"max_win"`
}

type rgsBundleIntegrity struct {
	ContentHash string `json:"content_hash"`
}

// registerBundleMath reads math.json from the extracted bundle dir, parses it as RGS schema,
// converts to gamemath.GameMath keyed by gameID, and registers with s.gameMath.
func (s *Server) registerBundleMath(gameID, targetRoot string) error {
	mathPath := filepath.Join(targetRoot, "math.json")
	data, err := os.ReadFile(mathPath)
	if err != nil {
		return fmt.Errorf("read math.json: %w", err)
	}
	var raw rgsMathFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse math.json: %w", err)
	}
	if len(raw.PrizeTable) == 0 {
		return fmt.Errorf("math.json: prize_table is empty")
	}
	// Convert to gamemath.GameMath; use gameID as model ID so round APIs resolve by game_id.
	math := &gamemath.GameMath{
		SchemaVersion: raw.SchemaVersion,
		ModelID:       gameID,
		ModelVersion:  raw.ModelVersion,
		Mechanic: gamemath.Mechanic{
			Type:       raw.Mechanic.Type,
			MatchCount: raw.Mechanic.MatchCount,
		},
		MathMode:   raw.MathMode,
		WinLogic:   raw.WinLogic,
		PrizeTable: make([]gamemath.PrizeTier, 0, len(raw.PrizeTable)),
	}
	for _, p := range raw.PrizeTable {
		w := int64(p.Weight)
		if w <= 0 && p.Probability > 0 {
			w = int64(p.Probability * 1e9)
		}
		if w <= 0 {
			w = 1
		}
		math.PrizeTable = append(math.PrizeTable, gamemath.PrizeTier{
			Tier:       p.Tier,
			Multiplier: p.Multiplier,
			Weight:     w,
		})
	}
	if raw.Stats != nil {
		math.Stats = &gamemath.GameStats{
			ComputedRTP: raw.Stats.ComputedRTP,
			HitRate:     raw.Stats.HitRate,
			Variance:    raw.Stats.Variance,
		}
	}
	if raw.Integrity != nil {
		math.Integrity = &gamemath.Integrity{ContentHash: raw.Integrity.ContentHash}
	}
	return s.gameMath.Register(math)
}
