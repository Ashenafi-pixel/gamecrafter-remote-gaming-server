package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	rgsdb "github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server"
	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/gamemath"
	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/games/crash"
	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/games/scratch"
	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/round"

	"github.com/google/uuid"
)

// LaunchRequest is the body for POST .../launch.
type LaunchRequest struct {
	Token    string `json:"token"`
	Lang     string `json:"lang"`
	Currency string `json:"currency"`
}

// LaunchResponse is the response for launch (iframe URL and config).
type LaunchResponse struct {
	IframeURL string            `json:"iframeUrl"`
	Config    map[string]string `json:"config"`
	Error     string            `json:"error,omitempty"`
}

// handleProviderRoute routes GET/POST /rgs/providers/<provider>/games/<gameId>/...
func (s *Server) handleProviderRoute(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/rgs/providers/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) < 4 {
		writeError(w, http.StatusNotFound, "invalid path", "INVALID_PATH")
		return
	}
	providerID, gameID := parts[0], parts[2]
	if parts[1] != "games" {
		writeError(w, http.StatusNotFound, "invalid path", "INVALID_PATH")
		return
	}
	if !s.registry.HasGame(providerID, gameID) {
		writeError(w, http.StatusNotFound, "game not found", "GAME_NOT_FOUND")
		return
	}
	if len(parts) == 4 && parts[3] == "launch" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		s.handleLaunch(w, r, providerID, gameID)
		return
	}
	if len(parts) == 4 && parts[3] == "math" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
			return
		}
		s.handleRegisterGameMath(w, r, gameID)
		return
	}
	if len(parts) == 5 && parts[3] == "round" {
		switch parts[4] {
		case "start":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
				return
			}
			if gameID == "crash" {
				s.handleCrashRoundStart(w, r, providerID)
				return
			}
			// Any other game: use generic scratch round (math from gamemath.Store keyed by gameId, or legacy default).
			s.handleScratchRoundStart(w, r, providerID, gameID)
			return
		case "cashout":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
				return
			}
			if gameID == "crash" {
				s.handleCrashCashout(w, r, providerID)
				return
			}
		case "status":
			if r.Method != http.MethodGet {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
				return
			}
			if gameID == "crash" {
				s.handleCrashStatus(w, r)
				return
			}
		}
	}
	writeError(w, http.StatusNotFound, "invalid path", "INVALID_PATH")
}

func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request, providerID, gameID string) {
	var req LaunchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body", "INVALID_BODY")
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" {
		writeError(w, http.StatusUnauthorized, "token required", "TOKEN_REQUIRED")
		return
	}
	// Validate token by calling platform balance
	if _, status, err := s.client.GetBalance(req.Token); err != nil {
		code := status
		if code == 0 {
			code = http.StatusBadGateway
		}
		writeError(w, code, err.Error(), "TOKEN_INVALID")
		return
	}
	lang := strings.TrimSpace(req.Lang)
	if lang == "" {
		lang = "es"
	}
	currency := strings.TrimSpace(req.Currency)
	if currency == "" {
		currency = "USD"
	}
	baseURL := strings.TrimSuffix(s.cfg.RGSBaseURL, "/")
	iframeURL, err := s.registry.GetLaunchURL(baseURL, providerID, gameID, req.Token, lang, currency)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "LAUNCH_ERROR")
		return
	}
	writeJSON(w, http.StatusOK, LaunchResponse{
		IframeURL: iframeURL,
		Config: map[string]string{
			"lang":     lang,
			"currency": currency,
		},
	})
}

// ScratchRoundStartRequest is the body for scratch round/start.
// Accepts API-doc names (session_id, bet_amount, round_id) and legacy (token, amount, roundId).
type ScratchRoundStartRequest struct {
	// API-doc (Operator_API_Documentation.md) parameter names
	SessionID  string  `json:"session_id"`
	BetAmount  float64 `json:"bet_amount"`
	RoundID    string  `json:"round_id"`
	GameCode   string  `json:"game_code"`
	DeviceType string  `json:"device_type"`
	Currency   string  `json:"currency"`
	// Legacy / alternate names (same meaning)
	Token   string  `json:"token"`
	Amount  float64 `json:"amount"`
	RoundId string  `json:"roundId"`
}

// ScratchRoundStartResponse is the response for scratch round start.
type ScratchRoundStartResponse struct {
	RoundID      string    `json:"roundId"`
	Symbols      [3]string `json:"symbols"`
	WinAmount    float64   `json:"winAmount"`
	BalanceDelta float64   `json:"balanceDelta"`   // winAmount - bet (positive if win, negative if lose)
	Tier         string    `json:"tier,omitempty"` // prize tier id (e.g. for lucky_star: prize_1770242082499_ro6ph9pnk)
	Error        string    `json:"error,omitempty"`
}

func (s *Server) handleScratchRoundStart(w http.ResponseWriter, r *http.Request, providerID, gameID string) {
	var req ScratchRoundStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body", "INVALID_BODY")
		return
	}
	// Normalize: prefer API-doc names (session_id, bet_amount, round_id), fallback to legacy (token, amount, roundId)
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(req.Token)
	}
	// Extra fallback: allow session_id from query string (compatible with external launchers)
	if sessionID == "" {
		sessionID = strings.TrimSpace(r.URL.Query().Get("session_id"))
	}
	if sessionID == "" {
		writeError(w, http.StatusUnauthorized, "session_id or token required", "TOKEN_REQUIRED")
		return
	}
	if len(sessionID) > 512 {
		writeError(w, http.StatusBadRequest, "session_id too long", "INVALID_REQUEST")
		return
	}
	currency := strings.TrimSpace(req.Currency)
	if currency == "" {
		currency = "USD"
	}
	betAmount := req.BetAmount
	if betAmount <= 0 {
		betAmount = req.Amount
	}
	if betAmount <= 0 {
		writeError(w, http.StatusBadRequest, "bet_amount or amount must be positive", "INVALID_AMOUNT")
		return
	}
	const maxBet = 1e6
	if betAmount > maxBet {
		writeError(w, http.StatusBadRequest, "bet_amount exceeds maximum", "INVALID_AMOUNT")
		return
	}
	roundID := strings.TrimSpace(req.RoundID)
	if roundID == "" {
		roundID = strings.TrimSpace(req.RoundId)
	}
	if roundID == "" {
		roundID = uuid.New().String()
	}
	deviceType := strings.TrimSpace(req.DeviceType)
	if deviceType == "" {
		deviceType = "desktop"
	}
	if deviceType != "desktop" && deviceType != "mobile" {
		deviceType = "desktop"
	}
	gameCode := strings.TrimSpace(req.GameCode)
	if gameCode == "" {
		gameCode = gameID
	}

	if existing, err := s.results.GetByRoundID(roundID); err == nil && existing != nil {
		var syms [3]string
		for i := 0; i < 3 && i < len(existing.Symbols); i++ {
			syms[i] = existing.Symbols[i]
		}
		writeJSON(w, http.StatusOK, ScratchRoundStartResponse{
			RoundID:      existing.RoundID,
			Symbols:      syms,
			WinAmount:    existing.WinAmount,
			BalanceDelta: existing.BalanceDelta,
		})
		return
	}

	var outcome scratch.Outcome
	// Resolve math by game_id from URL so any imported bundle (with registered math) works.
	modelID := gameID
	if math := s.gameMath.Get(modelID); math != nil {
		if o, ok := scratch.GenerateWithMath(betAmount, math); ok {
			outcome = o
		} else {
			outcome = scratch.Generate(betAmount)
		}
	} else {
		outcome = scratch.Generate(betAmount)
	}
	var balanceDelta float64
	if s.operator != nil {
		db, err := rgsdb.GetDB()
		if err != nil || db == nil {
			writeError(w, http.StatusBadGateway, "database unavailable", "TECHNICAL_ERROR")
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
      `, sessionID).Scan(&userID, &accountID, &dbGameCode, &operatorID)
		if err != nil {
			writeJSON(w, http.StatusOK, ScratchRoundStartResponse{
				RoundID:      roundID,
				Symbols:      outcome.Symbols,
				WinAmount:    outcome.WinAmount,
				BalanceDelta: 0,
				Tier:         outcome.Tier,
				Error:        "invalid session",
			})
			return
		}
		if gameCode == "scratch" && dbGameCode != "" {
			gameCode = dbGameCode
		}
		winAmount := outcome.WinAmount
		// API doc flow: Debit (place bet) then Credit (settle with win amount). Each has its own tx_id.
		txIDDebit := uuid.New().String()
		txIDCredit := uuid.New().String()
		respDebit, err := s.operator.Debit(userID, sessionID, roundID, txIDDebit, gameCode, deviceType, "1.0", betAmount, "")
		if err != nil || respDebit.Code != 0 {
			errMsg := "debit failed"
			if respDebit != nil && respDebit.Message != "" {
				errMsg = respDebit.Message
			} else if err != nil {
				errMsg = err.Error()
			}
			writeJSON(w, http.StatusOK, ScratchRoundStartResponse{
				RoundID:      roundID,
				Symbols:      outcome.Symbols,
				WinAmount:    0,
				BalanceDelta: 0,
				Tier:         outcome.Tier,
				Error:        errMsg,
			})
			return
		}
		respCredit, err := s.operator.Credit(userID, sessionID, roundID, txIDCredit, gameCode, deviceType, "1.0", "completed", "", winAmount)
		if err != nil || respCredit.Code != 0 {
			errMsg := "credit failed"
			if respCredit != nil && respCredit.Message != "" {
				errMsg = respCredit.Message
			} else if err != nil {
				errMsg = err.Error()
			}
			writeJSON(w, http.StatusOK, ScratchRoundStartResponse{
				RoundID:      roundID,
				Symbols:      outcome.Symbols,
				WinAmount:    0,
				BalanceDelta: 0,
				Tier:         outcome.Tier,
				Error:        errMsg,
			})
			return
		}
		netResult := winAmount - betAmount
		if netResult == 0 {
			netResult = -betAmount
		}
		// Use rgs_wallet_transactions (game_crafter wallet_transactions is for crypto only)
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
			sessionID,
			roundID,
			gameCode,
			betAmount,
			currency,
			betAmount,
			winAmount,
			netResult,
			userID,
			operatorID,
		)
		if winAmount > 0 {
			balanceDelta = winAmount - betAmount
		} else {
			balanceDelta = -betAmount
		}
	} else {
		betID, status, err := s.client.Bet(sessionID, currency, betAmount, "Scratch", "")
		if err != nil {
			code := status
			if code == 0 {
				code = http.StatusBadGateway
			}
			writeError(w, code, err.Error(), "BET_FAILED")
			return
		}
		if outcome.Match {
			_, err = s.client.Win(sessionID, currency, outcome.WinAmount, "Scratch", "")
			if err != nil {
				_, _ = s.client.Rollback(sessionID, betID)
				writeError(w, http.StatusBadGateway, err.Error(), "WIN_FAILED")
				return
			}
			balanceDelta = outcome.WinAmount - betAmount
		} else {
			balanceDelta = -betAmount
		}
	}

	outcomeStr := "lose"
	if outcome.Match {
		outcomeStr = "win"
	}
	symbolsSlice := outcome.Symbols[:]
	_ = s.results.Append(&round.Result{
		RoundID:      roundID,
		BetID:        "",
		Outcome:      outcomeStr,
		NextNumber:   0,
		BalanceDelta: balanceDelta,
		SettledAt:    time.Now(),
		Symbols:      symbolsSlice,
		WinAmount:    outcome.WinAmount,
	})

	writeJSON(w, http.StatusOK, ScratchRoundStartResponse{
		RoundID:      roundID,
		Symbols:      outcome.Symbols,
		WinAmount:    outcome.WinAmount,
		BalanceDelta: balanceDelta,
		Tier:         outcome.Tier,
	})
}

// Crash round/start
type CrashRoundStartRequest struct {
	Token    string  `json:"token"`
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
	RoundID  string  `json:"roundId"`
}

type CrashRoundStartResponse struct {
	RoundID     string `json:"roundId"`
	StartedAtMs int64  `json:"startedAtMs"`
	Error       string `json:"error,omitempty"`
}

func (s *Server) handleCrashRoundStart(w http.ResponseWriter, r *http.Request, providerID string) {
	var req CrashRoundStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body", "INVALID_BODY")
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" {
		writeError(w, http.StatusUnauthorized, "token required", "TOKEN_REQUIRED")
		return
	}
	if req.Currency == "" {
		req.Currency = "USD"
	}
	if req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "amount must be positive", "INVALID_AMOUNT")
		return
	}
	roundID := strings.TrimSpace(req.RoundID)
	if roundID == "" {
		roundID = uuid.New().String()
	}

	betID, status, err := s.client.Bet(req.Token, req.Currency, req.Amount, "Crash", "")
	if err != nil {
		code := status
		if code == 0 {
			code = http.StatusBadGateway
		}
		writeError(w, code, err.Error(), "BET_FAILED")
		return
	}

	crashStep := crash.GenerateCrashStep()
	cr := s.crashStore.Create(roundID, betID, req.Currency, req.Amount, crashStep)
	writeJSON(w, http.StatusOK, CrashRoundStartResponse{
		RoundID:     cr.RoundID,
		StartedAtMs: cr.StartedAt.UnixMilli(),
	})
}

// Crash round/cashout
type CrashCashoutRequest struct {
	Token   string `json:"token"`
	RoundID string `json:"roundId"`
	Step    int    `json:"step"`
}

type CrashCashoutResponse struct {
	RoundID      string  `json:"roundId"`
	CashedOut    bool    `json:"cashedOut"`
	WinAmount    float64 `json:"winAmount"`
	BalanceDelta float64 `json:"balanceDelta"`
	Crashed      bool    `json:"crashed"`
	CrashStep    int     `json:"crashStep,omitempty"`
	Error        string  `json:"error,omitempty"`
}

func (s *Server) handleCrashCashout(w http.ResponseWriter, r *http.Request, providerID string) {
	var req CrashCashoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body", "INVALID_BODY")
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" {
		writeError(w, http.StatusUnauthorized, "token required", "TOKEN_REQUIRED")
		return
	}
	req.RoundID = strings.TrimSpace(req.RoundID)
	if req.RoundID == "" {
		writeError(w, http.StatusBadRequest, "roundId required", "INVALID_ROUND")
		return
	}

	cr, ok := s.crashStore.Get(req.RoundID)
	if !ok {
		writeError(w, http.StatusNotFound, "round not found", "ROUND_NOT_FOUND")
		return
	}
	if cr.Settled {
		writeError(w, http.StatusConflict, "round already settled", "ROUND_SETTLED")
		return
	}

	// Current step from elapsed time (100ms per step)
	elapsed := time.Since(cr.StartedAt).Milliseconds()
	currentStep := int(elapsed / 100)
	if currentStep < 0 {
		currentStep = 0
	}

	if req.Step > currentStep {
		writeError(w, http.StatusBadRequest, "cannot cash out in the future", "INVALID_STEP")
		return
	}
	if req.Step < 0 {
		req.Step = 0
	}

	if req.Step >= cr.CrashStep {
		// Crashed before cash out - lose
		s.crashStore.Settle(req.RoundID)
		writeJSON(w, http.StatusOK, CrashCashoutResponse{
			RoundID:      req.RoundID,
			CashedOut:    false,
			Crashed:      true,
			CrashStep:    cr.CrashStep,
			WinAmount:    0,
			BalanceDelta: -cr.Amount,
		})
		return
	}

	// Cash out successful
	mult := crash.Multiplier(req.Step)
	winAmount := cr.Amount * mult
	_, err := s.client.Win(req.Token, cr.Currency, winAmount, "Crash", "")
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "WIN_FAILED")
		return
	}
	s.crashStore.Settle(req.RoundID)

	writeJSON(w, http.StatusOK, CrashCashoutResponse{
		RoundID:      req.RoundID,
		CashedOut:    true,
		Crashed:      false,
		WinAmount:    winAmount,
		BalanceDelta: winAmount - cr.Amount,
	})
}

// Crash round/status (GET)
func (s *Server) handleCrashStatus(w http.ResponseWriter, r *http.Request) {
	roundID := strings.TrimSpace(r.URL.Query().Get("roundId"))
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if roundID == "" || token == "" {
		writeError(w, http.StatusBadRequest, "roundId and token required", "INVALID_REQUEST")
		return
	}

	cr, ok := s.crashStore.Get(roundID)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"currentStep": 0,
			"crashed":     false,
			"roundId":     roundID,
		})
		return
	}

	elapsed := time.Since(cr.StartedAt).Milliseconds()
	currentStep := int(elapsed / 100)
	if currentStep < 0 {
		currentStep = 0
	}

	crashed := currentStep >= cr.CrashStep
	if crashed {
		s.crashStore.Settle(roundID)
	}
	resp := map[string]interface{}{
		"roundId":     roundID,
		"currentStep": currentStep,
		"multiplier":  crash.Multiplier(currentStep),
		"crashed":     crashed,
	}
	if crashed {
		resp["crashStep"] = cr.CrashStep
		resp["crashMultiplier"] = crash.Multiplier(cr.CrashStep)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleRegisterGameMath stores game math for a game (POST .../games/:gameId/math). Body = full game math JSON.
func (s *Server) handleRegisterGameMath(w http.ResponseWriter, r *http.Request, gameID string) {
	var math gamemath.GameMath
	if err := json.NewDecoder(r.Body).Decode(&math); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body", "INVALID_BODY")
		return
	}
	if math.ModelID == "" {
		writeError(w, http.StatusBadRequest, "model_id required", "INVALID_BODY")
		return
	}
	if len(math.PrizeTable) == 0 {
		writeError(w, http.StatusBadRequest, "prize_table required", "INVALID_BODY")
		return
	}
	if err := s.gameMath.Register(&math); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "REGISTER_FAILED")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"model_id": math.ModelID,
		"message":  "game math registered",
	})
}
