package server

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	rgsdb "github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server"
)

// handleGamePage serves the game HTML for iframe (GET /rgs/game/<gameId>?token=xxx&lang=es&currency=USD).
func (s *Server) handleGamePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "METHOD_NOT_ALLOWED")
		return
	}
	trimmed := strings.TrimPrefix(r.URL.Path, "/rgs/game/")
	pathNoQuery := strings.Trim(strings.Split(trimmed, "?")[0], "/")
	parts := strings.SplitN(pathNoQuery, "/", 2)
	gameID := parts[0]
	subPath := ""
	if len(parts) > 1 {
		subPath = parts[1]
	}
	if gameID == "" {
		writeError(w, http.StatusNotFound, "game id required", "INVALID_PATH")
		return
	}
	// Validate game exists using the games table (source of truth), not just the in‑memory registry.
	db, err := rgsdb.GetDB()
	if err != nil || db == nil {
		writeError(w, http.StatusInternalServerError, "database unavailable", "DB_UNAVAILABLE")
		return
	}
	var enabled bool
	err = db.QueryRowContext(r.Context(), "SELECT enabled FROM games WHERE game_id = $1 AND status = 'ACTIVE'", gameID).Scan(&enabled)
	if err != nil || !enabled {
		writeError(w, http.StatusNotFound, "game not found", "GAME_NOT_FOUND")
		return
	}
	// Any bundle game: serve static files (project_scratch.json, math.json, assets/*) without token
	if subPath != "" && !strings.Contains(subPath, "..") {
		s.serveBundleAsset(w, r, gameID, subPath)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("session_id"))
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "token required", "TOKEN_REQUIRED")
		return
	}
	referer := r.Header.Get("Referer")
	providerID, err := s.validateGamePageRequest(r.Context(), token, gameID, referer)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error(), "VALIDATION_FAILED")
		return
	}
	lang := strings.TrimSpace(r.URL.Query().Get("lang"))
	if lang == "" {
		lang = "es"
	}
	currency := strings.TrimSpace(r.URL.Query().Get("currency"))
	if currency == "" {
		currency = "USD"
	}
	switch gameID {
	case "scratch":
		s.serveScratchGame(w, r, token, lang, currency, providerID)
	case "crash":
		s.serveCrashGame(w, r, token, lang, currency, providerID)
	case "lucky_star":
		s.serveBundleGame(w, r, "lucky_star", token, lang, currency, providerID)
	default:
		if s.bundleGameExists(gameID) {
			s.serveBundleGame(w, r, gameID, token, lang, currency, providerID)
		} else {
			writeError(w, http.StatusNotFound, "game not found", "GAME_NOT_FOUND")
		}
	}
}

// validateGamePageRequest validates session, URL (game_id match), and operator iframe allowance.
// Embed policy is read from operators.allowed_embed_domains and operators.embed_referer_required.
func (s *Server) validateGamePageRequest(ctx context.Context, sessionID, urlGameID, referer string) (string, error) {
	db, err := rgsdb.GetDB()
	if err != nil || db == nil {
		return "", err
	}
	var providerCode string
	var sessionGameID string
	var expiresAt time.Time
	var sessionActive bool
	var allowedEmbedJSON []byte
	var embedRefererRequired bool
	var operatorDomain *string
	err = db.QueryRowContext(ctx, `
		SELECT o.code, gs.game_id, gs.expires_at, gs.is_active,
		       o.allowed_embed_domains, COALESCE(o.embed_referer_required, true), o.domain
		FROM game_sessions gs
		JOIN operators o ON gs.operator_id = o.operator_id
		WHERE gs.session_id = $1 AND o.is_active = true
	`, sessionID).Scan(&providerCode, &sessionGameID, &expiresAt, &sessionActive,
		&allowedEmbedJSON, &embedRefererRequired, &operatorDomain)
	if err != nil {
		return "", err
	}
	// Session valid: not expired, active
	if !sessionActive {
		return "", errSessionInactive
	}
	if !expiresAt.IsZero() && time.Now().After(expiresAt) {
		return "", errSessionExpired
	}
	// URL valid: game_id in path must match session's game
	if urlGameID != "" && sessionGameID != "" && urlGameID != sessionGameID {
		return "", errGameIDMismatch
	}
	// Operator iframe allowed: use allowed_embed_domains (jsonb) or fallback to domain
	var allowedHosts []string
	if len(allowedEmbedJSON) > 0 {
		_ = json.Unmarshal(allowedEmbedJSON, &allowedHosts)
	}
	if len(allowedHosts) == 0 && operatorDomain != nil && strings.TrimSpace(*operatorDomain) != "" {
		// Backward compat: treat domain as single allowed embed host
		d := strings.TrimSpace(*operatorDomain)
		d = strings.TrimPrefix(d, "https://")
		d = strings.TrimPrefix(d, "http://")
		d = strings.Split(d, "/")[0]
		if d != "" {
			allowedHosts = []string{strings.ToLower(d)}
		}
	}
	if len(allowedHosts) > 0 {
		refererHost := ""
		if referer != "" {
			u, parseErr := url.Parse(referer)
			if parseErr == nil && u.Host != "" {
				refererHost = strings.ToLower(u.Hostname())
			}
		}
		if refererHost == "" {
			if embedRefererRequired {
				return "", errEmbedNotAllowed
			}
			// embed_referer_required = false: allow direct open
			return providerCode, nil
		}
		allowed := false
		for _, h := range allowedHosts {
			host := strings.ToLower(strings.TrimSpace(h))
			host = strings.TrimPrefix(host, "https://")
			host = strings.TrimPrefix(host, "http://")
			host = strings.Split(host, "/")[0]
			if host == "" {
				continue
			}
			if refererHost == host || strings.HasSuffix(refererHost, "."+host) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", errEmbedNotAllowed
		}
	}
	return providerCode, nil
}

var (
	errSessionInactive = errValidation{msg: "session inactive"}
	errSessionExpired  = errValidation{msg: "session expired"}
	errGameIDMismatch  = errValidation{msg: "game id does not match session"}
	errEmbedNotAllowed = errValidation{msg: "operator not allowed to embed this game"}
)

type errValidation struct{ msg string }

func (e errValidation) Error() string { return e.msg }

// getProviderFromSessionID returns the operator code (provider) for the given session_id from the DB.
func (s *Server) getProviderFromSessionID(ctx context.Context, sessionID string) (string, error) {
	db, err := rgsdb.GetDB()
	if err != nil || db == nil {
		return "", err
	}
	var code string
	err = db.QueryRowContext(ctx, `
		SELECT o.code FROM game_sessions gs
		JOIN operators o ON gs.operator_id = o.operator_id
		WHERE gs.session_id = $1 AND o.is_active = true
	`, sessionID).Scan(&code)
	return code, err
}

// serveScratchGame writes the scratch card game HTML (iframe-ready, token/lang/currency in config).
// All values embedded in HTML/JS are escaped to prevent XSS.
func (s *Server) serveScratchGame(w http.ResponseWriter, r *http.Request, token, lang, currency, providerID string) {
	baseURL := strings.TrimSuffix(s.cfg.RGSBaseURL, "/")
	labels := getScratchLabels(lang)
	labelsJSON, _ := json.Marshal(labels)
	// Escape for HTML attribute (lang) and for JS string literals (prevents XSS from token/URL etc.)
	langSafe := html.EscapeString(lang)
	jsStr := func(s string) string {
		b, _ := json.Marshal(s)
		return string(b)
	}
	tokenJS := jsStr(token)
	baseURLJS := jsStr(baseURL)
	providerIDJS := jsStr(providerID)
	currencyJS := jsStr(currency)
	langJS := jsStr(lang)
	out := scratchGameHTML(langSafe, baseURLJS, providerIDJS, tokenJS, langJS, currencyJS, string(labelsJSON))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Allow embedding only from this RGS and from the platform (different port = different origin).
	platformOrigin := strings.TrimSuffix(s.cfg.PlatformURL, "/")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'self' "+platformOrigin)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(out))
}

func getScratchLabels(lang string) map[string]string {
	labels := map[string]map[string]string{
		"es": {
			"title": "Rasca y Gana", "play": "Jugar", "amount": "Monto", "balance": "Saldo",
			"loading": "Cargando…", "win": "¡Ganaste!", "lose": "No ganaste",
			"error": "Error", "retry": "Reintentar",
		},
		"en": {
			"title": "Scratch & Win", "play": "Play", "amount": "Amount", "balance": "Balance",
			"loading": "Loading…", "win": "You won!", "lose": "No win",
			"error": "Error", "retry": "Retry",
		},
	}
	if l, ok := labels[lang]; ok {
		return l
	}
	return labels["es"]
}

// scratchGameHTML returns the full HTML for the scratch card game (self-contained for iframe).
// langSafe is HTML-escaped for the html lang attribute; *_JS are JSON-encoded for safe JS embedding (XSS-safe).
func scratchGameHTML(langSafe, baseURLJS, providerIDJS, tokenJS, langJS, currencyJS, labelsJSON string) string {
	return `<!DOCTYPE html>
<html lang="` + langSafe + `">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Scratch</title>
  <style>
    * { box-sizing: border-box; }
    body { font-family: system-ui, sans-serif; margin: 0; padding: 16px; background: #0c0f17; color: #f5f5f4; min-height: 100vh; display: flex; flex-direction: column; align-items: center; justify-content: center; }
    .card { background: #151a24; border: 1px solid rgba(232,185,35,0.2); border-radius: 16px; padding: 24px; max-width: 360px; width: 100%; text-align: center; }
    h1 { font-size: 1.5rem; margin: 0 0 16px; color: #e8b923; }
    .scratch-wrap { position: relative; width: 100%; max-width: 300px; margin: 0 auto 16px; border-radius: 12px; overflow: hidden; box-shadow: inset 0 0 0 2px rgba(232,185,35,0.2); }
    .scratch-wrap canvas { display: block; width: 100%; height: auto; vertical-align: top; }
    .scratch-overlay { position: absolute; left: 0; top: 0; width: 100%; height: 100%; cursor: crosshair; }
    .scratch-overlay.revealed { opacity: 0; transition: opacity 0.4s ease; pointer-events: none; }
    .result { margin-top: 12px; font-size: 1.25rem; font-weight: 600; }
    .result.win { color: #22c55e; }
    .result.lose { color: #ef4444; }
    .result.hidden { visibility: hidden; }
    .error { color: #ef4444; font-size: 0.875rem; margin-top: 8px; }
    button { background: #e8b923; color: #0c0f17; border: none; border-radius: 999px; padding: 12px 24px; font-size: 1rem; font-weight: 600; cursor: pointer; width: 100%; margin-top: 8px; }
    button:hover { opacity: 0.9; }
    button:disabled { opacity: 0.6; cursor: not-allowed; }
    input { width: 100%; padding: 12px; border: 1px solid rgba(232,185,35,0.3); border-radius: 8px; background: #0c0f17; color: #f5f5f4; font-size: 1rem; margin-top: 8px; }
    label { display: block; text-align: left; font-size: 0.875rem; color: #a8a29e; margin-top: 12px; }
    .scratch-hint { font-size: 0.8rem; color: #78716c; margin-top: 8px; }
    .balance-row { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; font-size: 0.95rem; }
    .balance-value { color: #e8b923; font-weight: 600; }
  </style>
</head>
<body>
  <div class="card">
    <h1>Scratch</h1>
    <div class="balance-row"><span id="lbl-balance">Balance</span><span id="balance-value" class="balance-value">—</span></div>
    <div id="play-area">
      <label id="lbl-amount">Amount</label>
      <input type="number" id="amount" min="1" step="0.01" value="10" placeholder="0.00">
      <button id="btn-play" type="button">Play</button>
    </div>
    <div id="result-area" style="display:none;">
      <div class="scratch-wrap" id="scratch-wrap">
        <canvas id="scratch-content" width="300" height="120"></canvas>
        <canvas id="scratch-overlay" class="scratch-overlay" width="300" height="120"></canvas>
      </div>
      <p class="scratch-hint" id="scratch-hint">Raspa la tarjeta para revelar</p>
      <div class="result hidden" id="result-text"></div>
      <p id="win-amount" style="margin-top:4px;color:#a8a29e;"></p>
      <button id="btn-again" type="button">Play again</button>
    </div>
    <div id="error" class="error" style="display:none;"></div>
  </div>
  <script>
    (function() {
      var baseURL = ` + baseURLJS + `;
      var providerId = ` + providerIDJS + `;
      var token = ` + tokenJS + `;
      var currency = ` + currencyJS + `;
      var lang = ` + langJS + `;
      var labels = ` + labelsJSON + `;

      var playArea = document.getElementById("play-area");
      var resultArea = document.getElementById("result-area");
      var contentCanvas = document.getElementById("scratch-content");
      var overlayCanvas = document.getElementById("scratch-overlay");
      var scratchWrap = document.getElementById("scratch-wrap");
      var scratchHint = document.getElementById("scratch-hint");
      var resultText = document.getElementById("result-text");
      var winAmountEl = document.getElementById("win-amount");
      var errorEl = document.getElementById("error");
      var amountInput = document.getElementById("amount");
      var btnPlay = document.getElementById("btn-play");
      var btnAgain = document.getElementById("btn-again");
      var balanceValueEl = document.getElementById("balance-value");
      var lblBalanceEl = document.getElementById("lbl-balance");

      lblBalanceEl.textContent = labels.balance || "Balance";

      function fetchBalance() {
        var url = baseURL + "/rgs/tx/balance?session_id=" + encodeURIComponent(token) + "&game_code=scratch&device_type=desktop";
        fetch(url).then(function(res) { return res.json(); }).then(function(data) {
          if (data && data.code === 0 && (data.cash_balance != null || data.balance != null)) {
            var bal = data.cash_balance != null ? data.cash_balance : data.balance;
            balanceValueEl.textContent = (typeof bal === "number" ? bal.toFixed(2) : bal) + " " + currency;
          } else {
            balanceValueEl.textContent = "—";
          }
        }).catch(function() { balanceValueEl.textContent = "—"; });
      }
      fetchBalance();

      var BRUSH = 24;
      var REVEAL_AFTER = 40;
      var currentSymbols = [];
      var currentWinAmount = 0;
      var currentWon = false;
      var scratchCount = 0;
      var revealed = false;
      var isScratching = false;

      function showError(msg) {
        errorEl.textContent = msg;
        errorEl.style.display = "block";
      }
      function hideError() {
        errorEl.style.display = "none";
      }
      function setLoading(loading) {
        btnPlay.disabled = loading;
        btnPlay.textContent = loading ? labels.loading : labels.play;
      }

      function drawContent(symbols) {
        var ctx = contentCanvas.getContext("2d");
        var w = contentCanvas.width;
        var h = contentCanvas.height;
        ctx.fillStyle = "#1c2330";
        ctx.fillRect(0, 0, w, h);
        var boxW = 70;
        var gap = 16;
        var totalW = symbols.length * boxW + (symbols.length - 1) * gap;
        var x0 = (w - totalW) / 2 + boxW / 2;
        var y = h / 2;
        ctx.font = "bold 36px system-ui, sans-serif";
        ctx.textAlign = "center";
        ctx.textBaseline = "middle";
        for (var i = 0; i < symbols.length; i++) {
          var x = x0 + i * (boxW + gap);
          ctx.fillStyle = "#0c0f17";
          ctx.fillRect(x - boxW/2 - 4, y - 28, boxW + 8, 56);
          ctx.fillStyle = "#e8b923";
          ctx.fillText(symbols[i], x, y);
        }
      }

      function drawOverlay() {
        var ctx = overlayCanvas.getContext("2d");
        var w = overlayCanvas.width;
        var h = overlayCanvas.height;
        var g = ctx.createLinearGradient(0, 0, w, h);
        g.addColorStop(0, "#9ca3af");
        g.addColorStop(0.5, "#d1d5db");
        g.addColorStop(1, "#6b7280");
        ctx.fillStyle = g;
        ctx.fillRect(0, 0, w, h);
        ctx.strokeStyle = "rgba(0,0,0,0.15)";
        ctx.lineWidth = 2;
        ctx.strokeRect(1, 1, w - 2, h - 2);
      }

      function doReveal() {
        if (revealed) return;
        revealed = true;
        scratchWrap.querySelector(".scratch-overlay").classList.add("revealed");
        scratchHint.style.display = "none";
        resultText.classList.remove("hidden");
        if (currentWon) {
          resultText.textContent = labels.win;
          resultText.className = "result win";
          winAmountEl.textContent = "+" + currentWinAmount + " " + currency;
        } else {
          resultText.textContent = labels.lose;
          resultText.className = "result lose";
          winAmountEl.textContent = "";
        }
      }

      function scratch(x, y) {
        var rect = overlayCanvas.getBoundingClientRect();
        var scaleX = overlayCanvas.width / rect.width;
        var scaleY = overlayCanvas.height / rect.height;
        var cx = (x - rect.left) * scaleX;
        var cy = (y - rect.top) * scaleY;
        var ctx = overlayCanvas.getContext("2d");
        ctx.globalCompositeOperation = "destination-out";
        ctx.beginPath();
        ctx.arc(cx, cy, BRUSH, 0, Math.PI * 2);
        ctx.fill();
        ctx.globalCompositeOperation = "source-over";
        scratchCount++;
        if (scratchCount >= REVEAL_AFTER) doReveal();
      }

      function getXY(e) {
        if (e.touches && e.touches.length) return { x: e.touches[0].clientX, y: e.touches[0].clientY };
        return { x: e.clientX, y: e.clientY };
      }

      function initScratch() {
        drawContent(currentSymbols);
        drawOverlay();
        scratchCount = 0;
        revealed = false;
        isScratching = false;
        resultText.classList.add("hidden");
        resultText.className = "result";
        winAmountEl.textContent = "";
        scratchHint.style.display = "block";
        scratchWrap.querySelector(".scratch-overlay").classList.remove("revealed");
      }

      function onScratchStart(e) {
        if (currentSymbols.length === 0 || resultArea.style.display === "none") return;
        e.preventDefault();
        isScratching = true;
        var xy = getXY(e);
        scratch(xy.x, xy.y);
      }
      function onScratchMove(e) {
        if (!isScratching) return;
        e.preventDefault();
        var xy = getXY(e);
        scratch(xy.x, xy.y);
      }
      function onScratchEnd() { isScratching = false; }
      overlayCanvas.addEventListener("mousedown", onScratchStart);
      overlayCanvas.addEventListener("mousemove", onScratchMove);
      overlayCanvas.addEventListener("mouseup", onScratchEnd);
      overlayCanvas.addEventListener("mouseleave", onScratchEnd);
      overlayCanvas.addEventListener("touchstart", onScratchStart, { passive: false });
      overlayCanvas.addEventListener("touchmove", onScratchMove, { passive: false });
      overlayCanvas.addEventListener("touchend", onScratchEnd);
      overlayCanvas.style.cursor = "crosshair";

      function play() {
        hideError();
        var amount = parseFloat(amountInput.value);
        if (isNaN(amount) || amount <= 0) {
          showError("Invalid amount");
          return;
        }
        var roundId = (typeof crypto !== "undefined" && crypto.randomUUID) ? crypto.randomUUID() : (Date.now().toString(36) + Math.random().toString(36).slice(2));
        setLoading(true);
        var url = baseURL + "/rgs/providers/" + providerId + "/games/scratch/round/start";
        fetch(url, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ session_id: token, currency: currency, bet_amount: amount, round_id: roundId })
        })
        .then(function(res) { return res.json().then(function(data) { return { res: res, data: data }; }); })
        .then(function(_ref) {
          var res = _ref.res;
          var data = _ref.data;
          setLoading(false);
          if (data && data.error) {
            showError(data.error);
            fetchBalance();
            return;
          }
          if (!res.ok) {
            showError(data.error || data.message || "Request failed");
            fetchBalance();
            return;
          }
          currentSymbols = data.symbols || [];
          currentWinAmount = data.winAmount || 0;
          currentWon = currentWinAmount > 0;
          playArea.style.display = "none";
          resultArea.style.display = "block";
          initScratch();
        })
        .catch(function(err) {
          setLoading(false);
          showError(labels.error + ": " + (err.message || "Network error"));
        });
      }

      btnPlay.addEventListener("click", play);
      btnAgain.addEventListener("click", function() {
        resultArea.style.display = "none";
        playArea.style.display = "block";
        hideError();
        fetchBalance();
      });
    })();
  </script>
</body>
</html>`
}

func getCrashLabels(lang string) map[string]string {
	labels := map[string]map[string]string{
		"es": {
			"title": "Crash", "bet": "Apostar", "amount": "Monto", "cashout": "Cobrar",
			"loading": "Cargando…", "crashed": "¡Cayó!", "cashed": "Cobrado",
			"error": "Error", "playAgain": "Jugar de nuevo", "multiplier": "Multiplicador",
		},
		"en": {
			"title": "Crash", "bet": "Bet", "amount": "Amount", "cashout": "Cash out",
			"loading": "Loading…", "crashed": "Crashed!", "cashed": "Cashed out",
			"error": "Error", "playAgain": "Play again", "multiplier": "Multiplier",
		},
	}
	if l, ok := labels[lang]; ok {
		return l
	}
	return labels["es"]
}

func (s *Server) serveCrashGame(w http.ResponseWriter, r *http.Request, token, lang, currency, providerID string) {
	baseURL := strings.TrimSuffix(s.cfg.RGSBaseURL, "/")
	labels := getCrashLabels(lang)
	labelsJSON, _ := json.Marshal(labels)
	langSafe := html.EscapeString(lang)
	jsStr := func(s string) string {
		b, _ := json.Marshal(s)
		return string(b)
	}
	tokenJS := jsStr(token)
	baseURLJS := jsStr(baseURL)
	providerIDJS := jsStr(providerID)
	currencyJS := jsStr(currency)
	langJS := jsStr(lang)
	out := crashGameHTML(langSafe, baseURLJS, providerIDJS, tokenJS, langJS, currencyJS, string(labelsJSON))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'self' "+strings.TrimSuffix(s.cfg.PlatformURL, "/"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(out))
}

func crashGameHTML(langSafe, baseURLJS, providerIDJS, tokenJS, langJS, currencyJS, labelsJSON string) string {
	return `<!DOCTYPE html>
<html lang="` + langSafe + `">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Crash</title>
  <style>
    * { box-sizing: border-box; }
    body { font-family: system-ui, sans-serif; margin: 0; padding: 16px; background: #0c0f17; color: #f5f5f4; min-height: 100vh; display: flex; flex-direction: column; align-items: center; justify-content: center; }
    .card { background: #151a24; border: 1px solid rgba(232,185,35,0.2); border-radius: 16px; padding: 24px; max-width: 360px; width: 100%; text-align: center; }
    h1 { font-size: 1.5rem; margin: 0 0 16px; color: #e8b923; }
    .mult { font-size: 2rem; font-weight: 700; color: #22c55e; margin: 16px 0; }
    .mult.crashed { color: #ef4444; }
    button { background: #e8b923; color: #0c0f17; border: none; border-radius: 999px; padding: 12px 24px; font-size: 1rem; font-weight: 600; cursor: pointer; width: 100%; margin-top: 8px; }
    button:hover { opacity: 0.9; }
    button:disabled { opacity: 0.6; cursor: not-allowed; }
    button.danger { background: #ef4444; color: #fff; }
    input { width: 100%; padding: 12px; border: 1px solid rgba(232,185,35,0.3); border-radius: 8px; background: #0c0f17; color: #f5f5f4; font-size: 1rem; margin-top: 8px; }
    label { display: block; text-align: left; font-size: 0.875rem; color: #a8a29e; margin-top: 12px; }
    .error { color: #ef4444; font-size: 0.875rem; margin-top: 8px; }
    .result { margin-top: 12px; font-size: 1.1rem; }
  </style>
</head>
<body>
  <div class="card">
    <h1>Crash</h1>
    <div id="play-area">
      <label id="lbl-amount">Amount</label>
      <input type="number" id="amount" min="1" step="0.01" value="10" placeholder="0.00">
      <button id="btn-bet" type="button">Bet</button>
    </div>
    <div id="game-area" style="display:none;">
      <div class="mult" id="mult">1.00x</div>
      <button id="btn-cashout" type="button" class="danger">Cash out</button>
    </div>
    <div id="result-area" style="display:none;">
      <div class="mult" id="result-mult">1.00x</div>
      <div class="result" id="result-text"></div>
      <p id="win-amount" style="margin-top:8px;color:#a8a29e;"></p>
      <button id="btn-again" type="button">Play again</button>
    </div>
    <div id="error" class="error" style="display:none;"></div>
  </div>
  <script>
    (function() {
      var baseURL = ` + baseURLJS + `;
      var providerId = ` + providerIDJS + `;
      var token = ` + tokenJS + `;
      var currency = ` + currencyJS + `;
      var lang = ` + langJS + `;
      var labels = ` + labelsJSON + `;

      var playArea = document.getElementById("play-area");
      var gameArea = document.getElementById("game-area");
      var resultArea = document.getElementById("result-area");
      var multEl = document.getElementById("mult");
      var btnBet = document.getElementById("btn-bet");
      var btnCashout = document.getElementById("btn-cashout");
      var btnAgain = document.getElementById("btn-again");
      var resultMultEl = document.getElementById("result-mult");
      var resultText = document.getElementById("result-text");
      var winAmountEl = document.getElementById("win-amount");
      var amountInput = document.getElementById("amount");
      var errorEl = document.getElementById("error");

      var roundId = null;
      var startedAtMs = 0;
      var stepInterval = null;
      var statusInterval = null;
      var currentStep = 0;

      function showError(msg) {
        errorEl.textContent = msg;
        errorEl.style.display = "block";
      }
      function hideError() {
        errorEl.style.display = "none";
      }
      function multFromStep(s) { return (1 + s * 0.01).toFixed(2); }

      function startRound() {
        hideError();
        var amount = parseFloat(amountInput.value);
        if (isNaN(amount) || amount <= 0) {
          showError("Invalid amount");
          return;
        }
        roundId = (typeof crypto !== "undefined" && crypto.randomUUID) ? crypto.randomUUID() : (Date.now().toString(36) + Math.random().toString(36).slice(2));
        btnBet.disabled = true;
        btnBet.textContent = labels.loading;
        fetch(baseURL + "/rgs/providers/" + providerId + "/games/crash/round/start", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ token: token, currency: currency, amount: amount, roundId: roundId })
        })
        .then(function(res) { return res.json(); })
        .then(function(data) {
          btnBet.disabled = false;
          btnBet.textContent = labels.bet;
          if (data.error) {
            showError(data.error || "Request failed");
            return;
          }
          startedAtMs = data.startedAtMs || Date.now();
          playArea.style.display = "none";
          gameArea.style.display = "block";
          currentStep = 0;
          multEl.textContent = multFromStep(0) + "x";
          multEl.classList.remove("crashed");
          btnCashout.disabled = false;
          btnCashout.textContent = labels.cashout;
          if (stepInterval) clearInterval(stepInterval);
          if (statusInterval) clearInterval(statusInterval);
          stepInterval = setInterval(function() {
            var elapsed = Date.now() - startedAtMs;
            currentStep = Math.floor(elapsed / 100);
            multEl.textContent = multFromStep(currentStep) + "x";
          }, 50);
          statusInterval = setInterval(function() {
            fetch(baseURL + "/rgs/providers/" + providerId + "/games/crash/round/status?roundId=" + encodeURIComponent(roundId) + "&token=" + encodeURIComponent(token))
            .then(function(r) { return r.json(); })
            .then(function(data) {
              if (data.crashed) {
                clearInterval(stepInterval);
                clearInterval(statusInterval);
                multEl.textContent = (data.crashMultiplier || multFromStep(data.crashStep || 0)) + "x";
                multEl.classList.add("crashed");
                gameArea.style.display = "none";
                resultArea.style.display = "block";
                resultMultEl.textContent = (data.crashMultiplier || multFromStep(data.crashStep || 0)) + "x";
                resultMultEl.className = "mult crashed";
                resultText.textContent = labels.crashed;
                winAmountEl.textContent = "";
              }
            });
          }, 500);
        })
        .catch(function(err) {
          btnBet.disabled = false;
          btnBet.textContent = labels.bet;
          showError(labels.error + ": " + (err.message || "Network error"));
        });
      }

      function cashout() {
        if (!roundId) return;
        btnCashout.disabled = true;
        fetch(baseURL + "/rgs/providers/" + providerId + "/games/crash/round/cashout", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ token: token, roundId: roundId, step: currentStep })
        })
        .then(function(res) { return res.json(); })
        .then(function(data) {
          if (stepInterval) clearInterval(stepInterval);
          if (statusInterval) clearInterval(statusInterval);
          gameArea.style.display = "none";
          resultArea.style.display = "block";
          if (data.cashedOut) {
            resultMultEl.textContent = multFromStep(currentStep) + "x";
            resultMultEl.className = "mult";
            resultText.textContent = labels.cashed;
            resultText.style.color = "#22c55e";
            winAmountEl.textContent = "+" + (data.winAmount || 0) + " " + currency;
          } else {
            resultMultEl.textContent = (data.crashStep != null ? multFromStep(data.crashStep) : multFromStep(currentStep)) + "x";
            resultMultEl.className = "mult crashed";
            resultText.textContent = labels.crashed;
            resultText.style.color = "#ef4444";
            winAmountEl.textContent = "";
          }
        })
        .catch(function(err) {
          btnCashout.disabled = false;
          showError(labels.error + ": " + (err.message || "Network error"));
        });
      }

      btnBet.addEventListener("click", startRound);
      btnCashout.addEventListener("click", cashout);
      btnAgain.addEventListener("click", function() {
        resultArea.style.display = "none";
        playArea.style.display = "block";
        roundId = null;
        hideError();
      });
    })();
  </script>
</body>
</html>`
}

// bundleRoot returns the filesystem path to a game bundle (s.cfg.GamesDir/gameID).
func (s *Server) bundleRoot(gameID string) string {
	return filepath.Join(s.cfg.GamesDir, gameID)
}

// bundleGameExists returns true if the game has a bundle directory with index.html.
func (s *Server) bundleGameExists(gameID string) bool {
	indexPath := filepath.Join(s.bundleRoot(gameID), "index.html")
	info, err := os.Stat(indexPath)
	return err == nil && !info.IsDir()
}

// serveBundleAsset serves a static file from any game bundle (e.g. project_scratch.json, assets/img_123.png).
// Used by lucky_star and any other game that has a bundle under rgs/games/<gameID>/.
func (s *Server) serveBundleAsset(w http.ResponseWriter, r *http.Request, gameID, subPath string) {
	root := s.bundleRoot(gameID)
	cleanSub := filepath.Clean(subPath)
	if strings.HasPrefix(cleanSub, "..") || strings.Contains(cleanSub, "..") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	fpath := filepath.Join(root, cleanSub)
	rel, err := filepath.Rel(root, fpath)
	if err != nil || strings.HasPrefix(rel, "..") || strings.Contains(rel, "..") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	f, err := os.Open(fpath)
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(filepath.Ext(fpath))
	switch ext {
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".jpg", ".jpeg":
		w.Header().Set("Content-Type", "image/jpeg")
	case ".gif":
		w.Header().Set("Content-Type", "image/gif")
	case ".mp3":
		w.Header().Set("Content-Type", "audio/mpeg")
	case ".json":
		w.Header().Set("Content-Type", "application/json")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// serveBundleGame serves a game bundle's index.html with RGS config injected.
// Used by lucky_star and any other game that has rgs/games/<gameID>/index.html and optional assets/.
func (s *Server) serveBundleGame(w http.ResponseWriter, r *http.Request, gameID, token, lang, currency, providerID string) {
	root := s.bundleRoot(gameID)
	indexPath := filepath.Join(root, "index.html")
	htmlBytes, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "game bundle not found", "BUNDLE_NOT_FOUND")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load game", "TECHNICAL_ERROR")
		return
	}
	baseURL := strings.TrimSuffix(s.cfg.RGSBaseURL, "/")
	configScript := `<script>
window.RGS_CONFIG = {
  token: ` + jsonString(token) + `,
  baseURL: ` + jsonString(baseURL) + `,
  currency: ` + jsonString(currency) + `,
  lang: ` + jsonString(lang) + `,
  gameCode: ` + jsonString(gameID) + `,
  providerId: ` + jsonString(providerID) + `
};
</script>
`
	htmlStr := string(htmlBytes)
	if idx := strings.Index(htmlStr, "<body>"); idx >= 0 {
		insertAt := idx + len("<body>")
		htmlStr = htmlStr[:insertAt] + configScript + htmlStr[insertAt:]
	} else {
		htmlStr = configScript + htmlStr
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	platformOrigin := strings.TrimSuffix(s.cfg.PlatformURL, "/")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'self' "+platformOrigin)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(htmlStr))
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
