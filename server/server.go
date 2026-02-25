package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	rgsdb "github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server"
	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/config"
	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/gamemath"
	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/games"
	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/operator"
	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/platform"
	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/round"

	"github.com/google/uuid"
)

// Default model_id for scratch game when using stored game math.
const ScratchDefaultModelID = "scratch_match3"

type Server struct {
	cfg        *config.Config
	client     *platform.Client
	operator   *operator.Client
	store      *round.Store
	results    *round.ResultsStore
	crashStore *round.CrashStore
	gameMath   *gamemath.Store
	registry   *games.Registry
}

func New(cfg *config.Config) *Server {
	client := platform.NewClient(cfg.PlatformURL, cfg.GameName, cfg.GameProvider)
	var op *operator.Client
	if cfg.OperatorEndpoint != "" {
		op = operator.NewClient(cfg.OperatorEndpoint, cfg.OperatorSecret)
	}
	srv := &Server{
		cfg:        cfg,
		client:     client,
		operator:   op,
		store:      round.NewStore(cfg.DataDir),
		results:    round.NewResultsStore(cfg.DataDir),
		crashStore: round.NewCrashStore(cfg.DataDir),
		gameMath:   gamemath.NewStore(cfg.DataDir),
		registry:   games.NewRegistry(),
	}
	srv.loadLuckyStarMath()
	return srv
}

// bundleMathFile is the prizeTable part of the Luis bundle math.json format.
type bundleMathFile struct {
	PrizeTable []struct {
		ID          string  `json:"id"`
		Value       float64 `json:"value"`
		Probability float64 `json:"probability"`
		IsWin       bool    `json:"isWin"`
	} `json:"prizeTable"`
}

const luckyStarModelID = "lucky_star"

// loadLuckyStarMath loads rgs/games/lucky_star/math.json (bundle format), converts to GameMath, and registers it.
func (s *Server) loadLuckyStarMath() {
	mathPath := filepath.Join(s.cfg.GamesDir, "lucky_star", "math.json")
	data, err := os.ReadFile(mathPath)
	if err != nil {
		return
	}
	var bundle bundleMathFile
	if err := json.Unmarshal(data, &bundle); err != nil {
		return
	}
	if len(bundle.PrizeTable) == 0 {
		return
	}
	// Convert probability to weight (integer). Scale by 1e9 so we keep resolution.
	// Only include win tiers from the bundle (isWin true); we inject a LOSE tier so the game is fair (house edge).
	var tiers []gamemath.PrizeTier
	for _, p := range bundle.PrizeTable {
		if p.Probability <= 0 || !p.IsWin {
			continue
		}
		weight := int64(p.Probability * 1e9)
		if weight < 1 {
			weight = 1
		}
		tiers = append(tiers, gamemath.PrizeTier{
			Tier:       p.ID,
			Multiplier: p.Value,
			Weight:     weight,
		})
	}
	if len(tiers) == 0 {
		return
	}
	// Inject LOSE tier so the house wins most rounds (professional house edge).
	// ~70% lose / 30% win so the house consistently comes out ahead.
	var totalWinWeight int64
	for _, t := range tiers {
		totalWinWeight += t.Weight
	}
	if totalWinWeight > 0 {
		loseWeight := totalWinWeight * 70 / 30 // 70% lose, 30% win
		tiers = append(tiers, gamemath.PrizeTier{
			Tier:       "LOSE",
			Multiplier: 0,
			Weight:     loseWeight,
		})
	}
	math := &gamemath.GameMath{
		SchemaVersion: 1,
		ModelID:       luckyStarModelID,
		ModelVersion:  "1.0",
		Mechanic:      gamemath.Mechanic{Type: "match_3", MatchCount: 3},
		MathMode:      "UNLIMITED",
		WinLogic:      "SINGLE_WIN",
		PrizeTable:    tiers,
	}
	if err := s.gameMath.Register(math); err != nil {
		log.Printf("lucky_star: failed to register game math: %v", err)
		return
	}
	log.Printf("lucky_star: registered game math with %d tiers from %s", len(tiers), mathPath)
}

func (s *Server) Run() error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /api/openai/images", s.handleOpenAIImages)
	mux.HandleFunc("GET /rgs/balance", s.getBalance)
	mux.HandleFunc("POST /rgs/round/start", s.roundStart)
	mux.HandleFunc("POST /rgs/round/end", s.roundEnd)
	// Multi-provider: launch, game round start/cashout, crash status
	mux.HandleFunc("GET /rgs/providers/", s.handleProviderRoute)
	mux.HandleFunc("POST /rgs/providers/", s.handleProviderRoute)
	mux.HandleFunc("GET /rgs/game/", s.handleGamePage)
	mux.HandleFunc("GET /game/launch", s.handleGameLaunch)
	mux.HandleFunc("GET /rgs/tx/balance", s.handleTxBalance)
	mux.HandleFunc("GET /rgs/games/list", s.handleGamesList)
	// Admin: import standalone HTML + assets bundles generated from GameCrafter.
	mux.HandleFunc("POST /rgs/admin/games/import-zip", s.handleImportZip)

	port := s.cfg.RGSPort
	if port <= 0 {
		port = 8081
	}
	addr := ":" + strconv.Itoa(port)
	log.Printf("RGS listening on %s (platform: %s)", addr, s.cfg.PlatformURL)
	return http.ListenAndServe(addr, cors(requestLogger(mux)))
}

func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// requestLogger logs method and path for each request (no body or secrets).
func requestLogger(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("RGS %s %s", r.Method, r.URL.Path)
		h.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "rgs"})
}

// ImageRequest is the proxy request body for OpenAI image generation.
type ImageRequest struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	N       int    `json:"n"`
	Size    string `json:"size"`
	Quality string `json:"quality"`
}

// handleOpenAIImages implements POST /api/openai/images and proxies to OpenAI Images API.
func (s *Server) handleOpenAIImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		http.Error(w, "OPENAI_API_KEY not configured", http.StatusInternalServerError)
		return
	}
	orgID := strings.TrimSpace(os.Getenv("OPENAI_ORG_ID"))

	var req ImageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "prompt is required"})
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-image-1.5"
	}
	if req.N == 0 {
		req.N = 1
	}
	if strings.TrimSpace(req.Size) == "" {
		req.Size = "1024x1024"
	}
	if strings.TrimSpace(req.Quality) == "" {
		req.Quality = "standard"
	}

	payload := map[string]any{
		"model":   req.Model,
		"prompt":  req.Prompt,
		"n":       req.N,
		"size":    req.Size,
		"quality": req.Quality,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "failed to encode request", http.StatusInternalServerError)
		return
	}

	httpClient := &http.Client{Timeout: 120 * time.Second}
	upstreamReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://api.openai.com/v1/images/generations", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "failed to build upstream request", http.StatusInternalServerError)
		return
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+apiKey)
	upstreamReq.Header.Set("Content-Type", "application/json")
	if orgID != "" {
		upstreamReq.Header.Set("OpenAI-Organization", orgID)
	}

	resp, err := httpClient.Do(upstreamReq)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":   "OpenAI API error: request failed",
			"details": err.Error(),
		})
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "failed to read OpenAI response", http.StatusBadGateway)
		return
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("openai images error: status=%d body=%s", resp.StatusCode, string(respBody))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":   "OpenAI API error: " + resp.Status,
			"details": string(respBody),
		})
		return
	}

	// Pass OpenAI JSON response through unchanged.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBody)
}

type roundStartRequest struct {
	Token    string  `json:"token"`
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
	RoundID  string  `json:"roundId"`
}

type roundStartResponse struct {
	RoundID       string `json:"roundId"`
	CurrentNumber int    `json:"currentNumber"`
	BetID         string `json:"betId"`
	Error         string `json:"error,omitempty"`
}

func (s *Server) roundStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req roundStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, roundStartResponse{Error: "invalid body"})
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" {
		writeJSON(w, http.StatusUnauthorized, roundStartResponse{Error: "token required"})
		return
	}
	if req.Currency == "" {
		req.Currency = "USD"
	}
	if req.Amount <= 0 {
		writeJSON(w, http.StatusBadRequest, roundStartResponse{Error: "amount must be positive"})
		return
	}
	if req.RoundID == "" {
		req.RoundID = uuid.New().String()
	}

	betID, status, err := s.client.Bet(req.Token, req.Currency, req.Amount, "", "")
	if err != nil {
		code := status
		if code == 0 {
			code = http.StatusBadGateway
		}
		writeJSON(w, code, roundStartResponse{Error: err.Error()})
		return
	}

	round := s.store.Create(req.RoundID, betID, req.Currency, req.Amount)
	writeJSON(w, http.StatusOK, roundStartResponse{
		RoundID:       round.RoundID,
		CurrentNumber: round.CurrentNumber,
		BetID:         round.BetID,
	})
}

type roundEndRequest struct {
	Token   string `json:"token"`
	RoundID string `json:"roundId"`
	Choice  string `json:"choice"` // "higher" or "lower"
}

type roundEndResponse struct {
	Outcome      string  `json:"outcome"` // "win", "lose", "push"
	NextNumber   int     `json:"nextNumber"`
	BalanceDelta float64 `json:"balanceDelta"` // +amount on win, -amount on lose, 0 on push
	Error        string  `json:"error,omitempty"`
}

func (s *Server) roundEnd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req roundEndRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, roundEndResponse{Error: "invalid body"})
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	req.Choice = strings.ToLower(strings.TrimSpace(req.Choice))
	if req.Token == "" {
		writeJSON(w, http.StatusUnauthorized, roundEndResponse{Error: "token required"})
		return
	}
	if req.RoundID == "" {
		writeJSON(w, http.StatusBadRequest, roundEndResponse{Error: "roundId required"})
		return
	}
	if req.Choice != "higher" && req.Choice != "lower" {
		writeJSON(w, http.StatusBadRequest, roundEndResponse{Error: "choice must be higher or lower"})
		return
	}

	rnd, ok := s.store.Get(req.RoundID)
	if !ok {
		writeJSON(w, http.StatusNotFound, roundEndResponse{Error: "round not found or already settled"})
		return
	}
	defer s.store.Delete(req.RoundID)

	nextNum := round.NextNumber()
	current := rnd.CurrentNumber
	win := (req.Choice == "higher" && nextNum > current) || (req.Choice == "lower" && nextNum < current)
	push := nextNum == current

	var outcome string
	var delta float64
	if push {
		status, err := s.client.Rollback(req.Token, rnd.BetID)
		if err != nil {
			code := status
			if code == 0 {
				code = http.StatusBadGateway
			}
			writeJSON(w, code, roundEndResponse{Error: err.Error()})
			return
		}
		outcome = "push"
		delta = 0
	} else if win {
		_, err := s.client.Win(req.Token, rnd.Currency, rnd.Amount, "", "")
		if err != nil {
			writeJSON(w, http.StatusBadGateway, roundEndResponse{Error: err.Error()})
			return
		}
		outcome = "win"
		delta = rnd.Amount
	} else {
		outcome = "lose"
		delta = -rnd.Amount
	}

	// Persist round result to JSON (same as platform transactions)
	_ = s.results.Append(&round.Result{
		RoundID:      req.RoundID,
		BetID:        rnd.BetID,
		Outcome:      outcome,
		NextNumber:   nextNum,
		BalanceDelta: delta,
		SettledAt:    time.Now(),
	})

	writeJSON(w, http.StatusOK, roundEndResponse{
		Outcome:      outcome,
		NextNumber:   nextNum,
		BalanceDelta: delta,
	})
}

func (s *Server) getBalance(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization")
	if token != "" && strings.HasPrefix(token, "Bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token required"})
		return
	}
	balances, status, err := s.client.GetBalance(token)
	if err != nil {
		code := status
		if code == 0 {
			code = http.StatusBadGateway
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"balances": balances})
}

// handleGamesList returns active and enabled games from the games table (GET /rgs/games/list).
func (s *Server) handleGamesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	db, err := rgsdb.GetDB()
	if err != nil || db == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database unavailable"})
		return
	}
	rows, err := db.QueryContext(r.Context(), `
		SELECT game_id, COALESCE(name, ''), COALESCE(internal_name, ''), COALESCE(provider, '') 
		FROM games 
		WHERE status = 'ACTIVE' AND enabled = true 
		ORDER BY game_id
	`)
	if err != nil {
		log.Printf("rgs/games/list: query failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list games"})
		return
	}
	defer rows.Close()
	type gameRow struct {
		GameID       string `json:"game_id"`
		Name         string `json:"name"`
		InternalName string `json:"internal_name,omitempty"`
		Provider     string `json:"provider,omitempty"`
		Banner       string `json:"banner,omitempty"`
	}
	var list []gameRow
	for rows.Next() {
		var g gameRow
		if err := rows.Scan(&g.GameID, &g.Name, &g.InternalName, &g.Provider); err != nil {
			log.Printf("rgs/games/list: scan row: %v", err)
			continue
		}
		if u := s.resolveGameBanner(g.GameID); u != "" {
			g.Banner = u
		}
		list = append(list, g)
	}
	if list == nil {
		list = []gameRow{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"games": list})
}

type gameLaunchResponse struct {
	Success   bool   `json:"success"`
	GameURL   string `json:"game_url,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
	Message   string `json:"message,omitempty"`
}

// handleGameLaunch implements GET /game/launch per Operator_API_Documentation.md:
// required query params (country, currency, device_type, game_id, game_mode, language, partner_id, player_id),
// optional (reality_check_elapsed, reality_check_interval, home_url, exit_url, history_url);
// response: success, game_url, session_id (or error_code, message on failure).
func (s *Server) handleGameLaunch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusOK, gameLaunchResponse{
			Success:   false,
			ErrorCode: "method_not_allowed",
			Message:   "method not allowed",
		})
		return
	}
	q := r.URL.Query()
	country := strings.TrimSpace(q.Get("country"))
	currency := strings.TrimSpace(q.Get("currency"))
	deviceType := strings.TrimSpace(q.Get("device_type"))
	gameID := strings.TrimSpace(q.Get("game_id"))
	gameMode := strings.TrimSpace(q.Get("game_mode"))
	language := strings.TrimSpace(q.Get("language"))
	partnerID := strings.TrimSpace(q.Get("partner_id"))
	playerID := strings.TrimSpace(q.Get("player_id"))
	if country == "" || currency == "" || deviceType == "" || gameID == "" || gameMode == "" || language == "" || partnerID == "" || playerID == "" {
		writeJSON(w, http.StatusOK, gameLaunchResponse{
			Success:   false,
			ErrorCode: "invalid_parameter",
			Message:   "missing required parameter",
		})
		return
	}
	if deviceType != "desktop" && deviceType != "mobile" {
		writeJSON(w, http.StatusOK, gameLaunchResponse{
			Success:   false,
			ErrorCode: "invalid_parameter",
			Message:   "invalid device_type",
		})
		return
	}
	if gameMode != "demo" && gameMode != "real" {
		writeJSON(w, http.StatusOK, gameLaunchResponse{
			Success:   false,
			ErrorCode: "invalid_parameter",
			Message:   "invalid game_mode",
		})
		return
	}
	db, err := rgsdb.GetDB()
	if err != nil || db == nil {
		writeJSON(w, http.StatusOK, gameLaunchResponse{
			Success:   false,
			ErrorCode: "general_error",
			Message:   "database unavailable",
		})
		return
	}
	ctx := context.Background()
	operatorID, err := strconv.Atoi(partnerID)
	if err != nil || operatorID < 100000 || operatorID > 999999 {
		writeJSON(w, http.StatusOK, gameLaunchResponse{
			Success:   false,
			ErrorCode: "invalid_parameter",
			Message:   "partner_id must be a 6-digit operator id",
		})
		return
	}
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT true FROM operators WHERE operator_id = $1 AND is_active = true", operatorID).Scan(&exists); err != nil || !exists {
		writeJSON(w, http.StatusOK, gameLaunchResponse{
			Success:   false,
			ErrorCode: "unauthorized",
			Message:   "invalid or inactive partner_id",
		})
		return
	}
	// Use game_crafter games table: enabled, game_id, status
	var enabled bool
	err = db.QueryRowContext(ctx, "SELECT enabled FROM games WHERE game_id = $1 AND status = 'ACTIVE'", gameID).Scan(&enabled)
	if err != nil || !enabled {
		writeJSON(w, http.StatusOK, gameLaunchResponse{
			Success:   false,
			ErrorCode: "game_unavailable",
			Message:   "game unavailable",
		})
		return
	}
	username := playerID
	if len(username) > 20 {
		username = username[:20]
	}
	// Resolve or create user in game_crafter users table (no ON CONFLICT on username)
	var userID string
	err = db.QueryRowContext(ctx, "SELECT id::text FROM users WHERE username = $1 LIMIT 1", username).Scan(&userID)
	if err != nil {
		// Insert new user; game_crafter users require street_address, country, state, city, postal_code
		err = db.QueryRowContext(ctx, `
      INSERT INTO users (username, password, country, state, city, postal_code, street_address, operator_id)
      VALUES ($1, $2, $3, $4, $5, $6, '', $7)
      RETURNING id::text
    `,
			username,
			"external",
			country,
			"NA",
			"NA",
			"00000",
			operatorID,
		).Scan(&userID)
	}
	if err != nil {
		writeJSON(w, http.StatusOK, gameLaunchResponse{
			Success:   false,
			ErrorCode: "general_error",
			Message:   "failed to resolve player",
		})
		return
	}
	_, _ = db.ExecContext(ctx, "UPDATE users SET operator_id = $1 WHERE id = $2", operatorID, userID)
	_, _ = db.ExecContext(ctx, `
      INSERT INTO player_accounts (account_id, user_id, currency, operator_id)
      VALUES ($1, $2, $3, $4)
      ON CONFLICT (account_id) DO UPDATE
      SET user_id = EXCLUDED.user_id,
          currency = EXCLUDED.currency,
          operator_id = EXCLUDED.operator_id,
          updated_at = now(),
          last_activity = now()
    `,
		playerID,
		userID,
		currency,
		operatorID,
	)
	baseLaunchURL := strings.TrimSuffix(s.cfg.RGSBaseURL, "/") + "/rgs/game/" + gameID
	homeURL := q.Get("home_url")
	exitURL := q.Get("exit_url")
	historyURL := q.Get("history_url")
	sessionID := partnerID + "_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	_, err = db.ExecContext(ctx, `
      INSERT INTO game_sessions (
        session_id, user_id, game_id, device_type, game_mode, operator_id, launch_url, home_url, exit_url, history_url, reality_check_elapsed, reality_check_interval
      )
      VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, COALESCE($11::int, 0), COALESCE($12::int, 60))
    `,
		sessionID,
		userID,
		gameID,
		deviceType,
		gameMode,
		operatorID,
		baseLaunchURL,
		homeURL,
		exitURL,
		historyURL,
		q.Get("reality_check_elapsed"),
		q.Get("reality_check_interval"),
	)
	if err != nil {
		writeJSON(w, http.StatusOK, gameLaunchResponse{
			Success:   false,
			ErrorCode: "general_error",
			Message:   "failed to create session",
		})
		return
	}
	// Build long Groove-style game URL with full context in query string (session, operator, game, locale, redirects).
	params := make(url.Values)
	params.Set("session_id", sessionID)
	params.Set("launchtoken", sessionID)
	params.Set("partner_id", partnerID)
	params.Set("casinoid", partnerID)
	params.Set("game_id", gameID)
	params.Set("gameid", gameID)
	params.Set("languagecode", language)
	params.Set("lang", language)
	params.Set("currencycode", currency)
	params.Set("currency", currency)
	params.Set("clienttype", deviceType)
	params.Set("device_type", deviceType)
	params.Set("playmode", gameMode)
	params.Set("game_mode", gameMode)
	if homeURL != "" {
		params.Set("homeurl", homeURL)
		params.Set("home_url", homeURL)
	}
	if exitURL != "" {
		params.Set("exiturl", exitURL)
		params.Set("exit_url", exitURL)
	}
	if historyURL != "" {
		params.Set("historyurl", historyURL)
		params.Set("history_url", historyURL)
	}
	if rcElapsed := strings.TrimSpace(q.Get("reality_check_elapsed")); rcElapsed != "" {
		params.Set("reality_check_elapsed", rcElapsed)
	}
	if rcInterval := strings.TrimSpace(q.Get("reality_check_interval")); rcInterval != "" {
		params.Set("reality_check_interval", rcInterval)
	}
	params.Set("country", country)
	gameURL := baseLaunchURL + "?" + params.Encode()
	writeJSON(w, http.StatusOK, gameLaunchResponse{
		Success:   true,
		GameURL:   gameURL,
		SessionID: sessionID,
	})
}

func (s *Server) handleTxBalance(w http.ResponseWriter, r *http.Request) {
	if s.operator == nil {
		// Operator endpoint not configured: return a JSON error with HTTP 200 so game UIs
		// can degrade gracefully (show balance as unavailable) instead of network failure.
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"code":    1,
			"status":  "Technical error",
			"message": "operator endpoint not configured",
		})
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	gameCode := strings.TrimSpace(r.URL.Query().Get("game_code"))
	deviceType := strings.TrimSpace(r.URL.Query().Get("device_type"))
	if deviceType == "" {
		deviceType = "desktop"
	}
	if sessionID == "" || gameCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"code":    13,
			"status":  "Parameter required",
			"message": "session_id and game_code are required",
		})
		return
	}
	db, err := rgsdb.GetDB()
	if err != nil || db == nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"code":    1,
			"status":  "Technical error",
			"message": "database unavailable",
		})
		return
	}
	ctx := context.Background()
	var userID string
	err = db.QueryRowContext(ctx, "SELECT user_id FROM game_sessions WHERE session_id = $1", sessionID).Scan(&userID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"code":    2,
			"status":  "Session invalid",
			"message": "session not found",
		})
		return
	}
	resp, err := s.operator.Balance(userID, sessionID, gameCode, deviceType, "1.0")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"code":    1,
			"status":  "Technical error",
			"message": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(resp.Body))
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
