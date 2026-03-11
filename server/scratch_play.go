package server

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	rgsdb "github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server"
	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/games/scratch"
)

// ScratchPlayRequest is the request body for POST /api/scratch/play.
type ScratchPlayRequest struct {
	Token      string  `json:"token"`
	SessionID  string  `json:"session_id,omitempty"` // optional; fallback to token if empty
	GameID     string  `json:"gameId"`
	BetAmount  float64 `json:"betAmount"`
	Currency   string  `json:"currency"`
	OperatorID int     `json:"operatorId,omitempty"`
	DeviceType string  `json:"deviceType,omitempty"`
}

// ScratchResolvedOutcome is the GameCrafter-compatible scratch outcome payload.
type ScratchResolvedOutcome struct {
	RoundID          string   `json:"roundId"`
	IsWin            bool     `json:"isWin"`
	TierID           string   `json:"tierId"`
	FinalPrize       float64  `json:"finalPrize"`
	PresentationSeed int64    `json:"presentationSeed,omitempty"`
	RevealMap        []string `json:"revealMap"`
}

type ScratchSymbolsResponse struct {
	GameID  string          `json:"gameId"`
	Symbols []ScratchSymbol `json:"symbols"`
}

func (s *Server) getScratchConfig(gameID string) *ScratchConfig {
	if s.scratchConfigs == nil {
		return nil
	}
	if c, ok := s.scratchConfigs[gameID]; ok {
		return c
	}
	return nil
}

// handleScratchSymbols returns symbol configuration (IDs + images) for a scratch game.
// GET /api/scratch/symbols?gameId=<GAME_ID>
func (s *Server) handleScratchSymbols(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	gameID := strings.TrimSpace(r.URL.Query().Get("gameId"))
	if gameID == "" {
		http.Error(w, "gameId is required", http.StatusBadRequest)
		return
	}
	cfg := s.getScratchConfig(gameID)
	if cfg == nil {
		http.Error(w, "unknown gameId", http.StatusNotFound)
		return
	}
	resp := ScratchSymbolsResponse{
		GameID:  cfg.GameID,
		Symbols: cfg.Symbols,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleScratchPlay implements POST /api/scratch/play.
// It reuses the existing scratch math + wallet integration used by provider routes,
// but returns a GameCrafter-style ResolvedOutcome payload.
func (s *Server) handleScratchPlay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ScratchPlayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		req.SessionID = req.Token
	}
	if req.SessionID == "" {
		http.Error(w, "token or session_id is required", http.StatusUnauthorized)
		return
	}
	req.GameID = strings.TrimSpace(req.GameID)
	if req.GameID == "" {
		req.GameID = "scratch"
	}
	if req.BetAmount <= 0 {
		http.Error(w, "betAmount must be positive", http.StatusBadRequest)
		return
	}
	if req.Currency == "" {
		req.Currency = "USD"
	}
	deviceType := strings.TrimSpace(req.DeviceType)
	if deviceType == "" {
		deviceType = "desktop"
	}

	roundID := uuid.New().String()

	// Generate outcome using existing game math (or legacy scratch fallback).
	var outcome scratch.Outcome
	modelID := req.GameID
	if math := s.gameMath.Get(modelID); math != nil {
		if o, ok := scratch.GenerateWithMath(req.BetAmount, math); ok {
			outcome = o
		} else {
			outcome = scratch.Generate(req.BetAmount)
		}
	} else {
		outcome = scratch.Generate(req.BetAmount)
	}

	// Wallet integration: use operator transaction API when configured, otherwise platform client.
	var finalPrize = outcome.WinAmount
	if s.operator != nil {
		db, err := rgsdb.GetDB()
		if err != nil || db == nil {
			http.Error(w, "database unavailable", http.StatusBadGateway)
			return
		}
		ctx := r.Context()
		var userID, accountID string
		var dbGameCode string
		var operatorID int
		err = db.QueryRowContext(ctx, `
        SELECT gs.user_id, u.username, gs.game_id, gs.operator_id
        FROM game_sessions gs
        JOIN users u ON gs.user_id = u.id
        WHERE gs.session_id = $1
      `, req.SessionID).Scan(&userID, &accountID, &dbGameCode, &operatorID)
		if err != nil {
			http.Error(w, "invalid session", http.StatusUnauthorized)
			return
		}
		gameCode := req.GameID
		if gameCode == "scratch" && dbGameCode != "" {
			gameCode = dbGameCode
		}
		txIDDebit := uuid.New().String()
		txIDCredit := uuid.New().String()
		respDebit, err := s.operator.Debit(userID, req.SessionID, roundID, txIDDebit, gameCode, deviceType, "1.0", req.BetAmount, "")
		if err != nil || respDebit.Code != 0 {
			errMsg := "debit failed"
			if respDebit != nil && respDebit.Message != "" {
				errMsg = respDebit.Message
			} else if err != nil {
				errMsg = err.Error()
			}
			http.Error(w, errMsg, http.StatusBadGateway)
			return
		}
		respCredit, err := s.operator.Credit(userID, req.SessionID, roundID, txIDCredit, gameCode, deviceType, "1.0", "completed", "", finalPrize)
		if err != nil || respCredit.Code != 0 {
			errMsg := "credit failed"
			if respCredit != nil && respCredit.Message != "" {
				errMsg = respCredit.Message
			} else if err != nil {
				errMsg = err.Error()
			}
			http.Error(w, errMsg, http.StatusBadGateway)
			return
		}
		// Log combined wallet transaction (similar to provider scratch handler).
		netResult := finalPrize - req.BetAmount
		if netResult == 0 {
			netResult = -req.BetAmount
		}
		_, _ = db.ExecContext(ctx, `
        INSERT INTO rgs_wallet_transactions (
          transaction_id,
          account_id,
          session_id,
          round_id,
          game_id,
          type,
          status,
          amount,
          currency,
          bet_amount,
          win_amount,
          net_result,
          user_id,
          operator_id
        ) VALUES (
          $1, $2, $3, $4, $5, 'debit_and_credit', 'completed',
          $6, $7, $8, $9, $10, $11, $12
        )
      `,
			txIDDebit,
			accountID,
			req.SessionID,
			roundID,
			gameCode,
			req.BetAmount,
			req.Currency,
			req.BetAmount,
			finalPrize,
			netResult,
			userID,
			operatorID,
		)
	} else if s.client != nil {
		// Fallback to platform client if operator transaction API is not configured.
		betID, status, err := s.client.Bet(req.SessionID, req.Currency, req.BetAmount, "Scratch", "")
		if err != nil {
			code := status
			if code == 0 {
				code = http.StatusBadGateway
			}
			http.Error(w, err.Error(), code)
			return
		}
		if finalPrize > 0 {
			_, err = s.client.Win(req.SessionID, req.Currency, finalPrize, "Scratch", "")
			if err != nil {
				_, _ = s.client.Rollback(req.SessionID, betID)
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
		}
	}

	// Build revealMap according to mechanic config (Variant A/B/C). Fallback to 1x3 if no config.
	cfg := s.getScratchConfig(req.GameID)
	revealMap := buildRevealMapFromOutcome(cfg, &outcome)

	resp := ScratchResolvedOutcome{
		RoundID:          roundID,
		IsWin:            finalPrize > 0,
		TierID:           outcome.Tier,
		FinalPrize:       finalPrize,
		PresentationSeed: time.Now().UnixNano(),
		RevealMap:        revealMap,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// --- revealMap generation helpers ---

func buildRevealMapFromOutcome(cfg *ScratchConfig, outcome *scratch.Outcome) []string {
	if cfg == nil {
		// Fallback: 1x3 grid from simple outcome.
		return []string{outcome.Symbols[0], outcome.Symbols[1], outcome.Symbols[2]}
	}
	switch cfg.Mechanic.Type {
	case "match_n":
		return generateMatchNRevealMap(cfg, outcome)
	case "symbol_hunt":
		return generateSymbolHuntRevealMap(cfg, outcome)
	case "target_match":
		return generateTargetMatchRevealMap(cfg, outcome)
	default:
		return []string{outcome.Symbols[0], outcome.Symbols[1], outcome.Symbols[2]}
	}
}

func randomInt(n int) int {
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

func randomFrom(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[randomInt(len(list))]
}

func randomDistinctIndices(total, count int) []int {
	if total <= 0 || count <= 0 {
		return nil
	}
	if count > total {
		count = total
	}
	indices := make([]int, total)
	for i := 0; i < total; i++ {
		indices[i] = i
	}
	// Fisher–Yates shuffle first count elements
	for i := 0; i < count; i++ {
		j := i + randomInt(total-i)
		indices[i], indices[j] = indices[j], indices[i]
	}
	return indices[:count]
}

// Variant A: Match-N (e.g. 3x3 Match-3/2/4).
func generateMatchNRevealMap(cfg *ScratchConfig, outcome *scratch.Outcome) []string {
	rows, cols := cfg.Mechanic.Rows, cfg.Mechanic.Cols
	if rows <= 0 {
		rows = 3
	}
	if cols <= 0 {
		cols = 3
	}
	total := rows * cols
	if total <= 0 {
		total = 9
	}

	var winSyms, dudSyms []string
	var topSym string
	for _, s := range cfg.Symbols {
		switch s.Category {
		case "win":
			winSyms = append(winSyms, s.ID)
		case "dud":
			dudSyms = append(dudSyms, s.ID)
		case "top":
			topSym = s.ID
		}
	}
	if len(dudSyms) == 0 {
		dudSyms = winSyms
	}
	if len(dudSyms) == 0 && topSym != "" {
		dudSyms = []string{topSym}
	}

	grid := make([]string, total)
	isWin := outcome.WinAmount > 0
	matchCount := cfg.Mechanic.MatchCount
	if matchCount <= 0 {
		matchCount = 3
	}

	if isWin {
		// For now, pick any winning symbol; later, link tiers to specific symbols via config/math.
		winSym := ""
		if len(winSyms) > 0 {
			winSym = winSyms[randomInt(len(winSyms))]
		} else if topSym != "" {
			winSym = topSym
		} else if len(dudSyms) > 0 {
			winSym = dudSyms[0]
		}
		idxs := randomDistinctIndices(total, matchCount)
		for _, i := range idxs {
			grid[i] = winSym
		}
		for i := 0; i < total; i++ {
			if grid[i] != "" {
				continue
			}
			grid[i] = randomFrom(dudSyms)
		}
	} else {
		// Simple safe loss ticket: all duds, avoid top symbol.
		for i := 0; i < total; i++ {
			grid[i] = randomFrom(dudSyms)
		}
	}
	return grid
}

// Variant C: Symbol Hunt / Pick One – placeholder implementation:
// win uses a special symbol, loss uses only duds. Extend with per-tier payout mapping later.
func generateSymbolHuntRevealMap(cfg *ScratchConfig, outcome *scratch.Outcome) []string {
	rows, cols := cfg.Mechanic.Rows, cfg.Mechanic.Cols
	if rows <= 0 {
		rows = 3
	}
	if cols <= 0 {
		cols = 3
	}
	total := rows * cols
	if total <= 0 {
		total = 9
	}
	var specialSyms, dudSyms []string
	for _, s := range cfg.Symbols {
		switch s.Category {
		case "top", "win":
			specialSyms = append(specialSyms, s.ID)
		case "dud":
			dudSyms = append(dudSyms, s.ID)
		}
	}
	if len(dudSyms) == 0 {
		dudSyms = specialSyms
	}
	grid := make([]string, total)
	isWin := outcome.WinAmount > 0
	if isWin && len(specialSyms) > 0 {
		// Place at least one special symbol, rest duds.
		winSym := specialSyms[randomInt(len(specialSyms))]
		idxs := randomDistinctIndices(total, 1)
		for _, i := range idxs {
			grid[i] = winSym
		}
		for i := 0; i < total; i++ {
			if grid[i] != "" {
				continue
			}
			grid[i] = randomFrom(dudSyms)
		}
	} else {
		// Loss: all duds (no top/special symbol).
		for i := 0; i < total; i++ {
			grid[i] = randomFrom(dudSyms)
		}
	}
	return grid
}

// Variant B: Target Match – placeholder: treat as loss-only safe grid for now.
// Extend with explicit winning/your-number zones when you introduce such games.
func generateTargetMatchRevealMap(cfg *ScratchConfig, outcome *scratch.Outcome) []string {
	rows, cols := cfg.Mechanic.Rows, cfg.Mechanic.Cols
	if rows <= 0 {
		rows = 3
	}
	if cols <= 0 {
		cols = 3
	}
	total := rows * cols
	if total <= 0 {
		total = 9
	}
	var dudSyms []string
	for _, s := range cfg.Symbols {
		if s.Category == "dud" {
			dudSyms = append(dudSyms, s.ID)
		}
	}
	if len(dudSyms) == 0 {
		for _, s := range cfg.Symbols {
			dudSyms = append(dudSyms, s.ID)
		}
	}
	grid := make([]string, total)
	for i := 0; i < total; i++ {
		grid[i] = randomFrom(dudSyms)
	}
	return grid
}
