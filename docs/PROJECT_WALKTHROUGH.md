# Project Walkthrough: GameCrafter Remote Gaming Server (RGS)

This document walks you through what this project is, how it is structured, and how the main flows work.

---

## 1. What This Project Is

**RGS (Remote Gaming Server)** is a **Go backend** that:

- **Runs game logic** for several game types: Hi/Lo, Scratch, Crash, and “bundle” games (e.g. Lucky Star and AI-generated games).
- **Talks to your casino “platform”** (a separate app) for **wallet operations**: get balance, place bet, credit win, rollback bet.
- Optionally talks to an **Operator API** for session-based transactions (used when players launch games via `/game/launch` with `session_id`).
- Can use a **PostgreSQL database** for: game catalog, game sessions, users, operators, and (with the importer) registering new games.

It does **not** implement login or auth UI; your platform (e.g. Next.js) does that and issues JWTs. The RGS only uses those JWTs (or session IDs) when calling the platform or operator.

---

## 2. High-Level Architecture

```
┌─────────────────────┐     JWT / session      ┌──────────────────────┐
│  Casino frontend    │ ──────────────────────►│  RGS (this project)  │
│  (your website)    │                         │  Go server            │
└─────────────────────┘                         └──────────┬───────────┘
        │                                                  │
        │ login, get JWT                                   │ bet / win / rollback
        ▼                                                  │ balance
┌─────────────────────┐                                    ▼
│  Platform backend   │◄─────────────────────────────────────
│  (Next.js / etc.)   │   GET/POST /api/balance*
│  - /api/auth/login  │
│  - /api/balance*    │
└─────────────────────┘

Optional:
┌─────────────────────┐     session_id          ┌──────────────────────┐
│  Operator API       │◄────────────────────────│  RGS                  │
│  (your backend)     │   balance / debit /     │  (OPERATOR_ENDPOINT   │
│  /api/operator/...  │   credit                 │   set)                │
└─────────────────────┘                          └──────────────────────┘
```

- **Frontend** → calls RGS for game actions (balance, start round, end round, launch game, etc.).
- **RGS** → calls **platform** for wallet (when using JWT) or **operator** for wallet (when using session from `/game/launch`).
- **Platform** → implements login (`/api/auth/login`) and balance APIs (`/api/balance`, `/api/balance/bet`, `/api/balance/win`, `/api/balance/rollback`).

---

## 3. Repository Layout

| Path | Purpose |
|------|--------|
| `cmd/server/main.go` | Entry point: load `.env`, load config, create server, run HTTP server. |
| `cmd/game_importer/main.go` | CLI to import an AI-generated game ZIP (manifest + assets) into storage and DB. |
| `config/config.go` | Reads env (PLATFORM_URL, PORT, RGS_BASE_URL, etc.) into a `Config` struct. |
| `server/server.go` | Builds the HTTP mux, registers all routes, CORS, request logging. |
| `server/handlers_*.go` | Handlers for health, balance, rounds, game pages, provider routes, launch, import. |
| `platform/client.go` | HTTP client that calls the platform’s `/api/balance*` with JWT. |
| `operator/client.go` | Optional client for Operator Transaction API (balance, debit, credit with signature). |
| `round/store.go` | Hi/Lo round state: in-memory map + JSON file; secure random “next number”. |
| `round/results.go` | Append round results to a JSON file (audit). |
| `games/registry.go` | Game catalog: provider → game IDs, loaded from DB; builds launch URLs. |
| `gamemath/` | Game math (e.g. prize tables) for scratch/bundle games. |
| `games/scratch/`, `games/crash/` | Scratch and Crash game logic. |
| `db.go` | `GetDB()`: single PostgreSQL connection (from `DATABASE_URL`), simple protocol for poolers. |

---

## 4. Configuration (Environment)

The server uses **environment variables** (and `.env` via `godotenv` in `main.go`). Important ones:

| Variable | Default | Meaning |
|----------|---------|--------|
| `PLATFORM_URL` | `http://localhost:3000` | Base URL of your platform (balance APIs). |
| `RGS_PORT` or `PORT` | `8081` | Port the RGS listens on. |
| `RGS_BASE_URL` | `http://localhost:8081` | Public URL of this RGS (for iframe/game URLs). |
| `GAME_NAME` | `Hi/Lo` | Game name sent to platform in bet/win metadata. |
| `GAME_PROVIDER` | `Crypto LATAM` | Provider name sent to platform. |
| `RGS_DATA_DIR` | `data` | Directory for round/result JSON files. |
| `RGS_GAMES_DIR` | `games` | Root directory for game bundles (e.g. scratch, crash, lucky_star). |
| `DATABASE_URL` | (none) | PostgreSQL connection string. If unset, DB-dependent features are disabled. |
| `OPERATOR_ENDPOINT` | (none) | If set, RGS uses Operator API for session-based balance/debit/credit. |
| `OPERATOR_SECRET` | (none) | Secret for signing operator requests. |

See `env.example` for a full list and comments.

---

## 5. Main HTTP Routes

These are the routes the RGS exposes (from `server/server.go` and handlers).

### Health and utility

- **GET /health**  
  Returns `{"status":"ok","service":"rgs"}`. No auth.

### Platform-style (JWT)

- **GET /rgs/balance**  
  Returns user balances. Send platform JWT: `Authorization: Bearer <token>` or `?token=<token>`.  
  RGS forwards to platform `GET /api/balance`.

- **POST /rgs/round/start**  
  Hi/Lo: start a round. Body: `{ "token", "currency", "amount", optional "roundId" }`.  
  RGS calls platform `POST /api/balance/bet`, then creates a round and returns `roundId`, `currentNumber`, `betId`.

- **POST /rgs/round/end**  
  Hi/Lo: end round. Body: `{ "token", "roundId", "choice": "higher"|"lower" }`.  
  RGS resolves outcome, then calls platform **win** or **rollback**, returns outcome and balance delta.

### Game listing and launch

- **GET /rgs/games/list**  
  Returns list of active/enabled games from DB (game_id, name, provider, etc.). No auth required for listing.

- **GET /game/launch**  
  Operator-style launch: query params include `partner_id`, `player_id`, `game_id`, `currency`, `language`, `device_type`, `game_mode`, etc.  
  RGS checks operator in DB, ensures game exists, creates/updates user and session, then returns **game_url** (iframe URL with `session_id`, `launchtoken`, etc.) and **session_id**.  
  The game then uses **session_id** with the Operator API (or RGS proxy) for balance/bets.

### Game page (iframe)

- **GET /rgs/game/<gameId>**  
  Serves the game UI for that game (e.g. scratch, crash, lucky_star, or any bundle game).  
  Query params: `token` or `session_id`, optional `lang`, `currency`.  
  Validates game exists and (when applicable) session/operator/embed policy, then serves HTML (or asset) for that game.  
  Subpaths like `/rgs/game/<gameId>/project_scratch.json` serve bundle assets.

### Provider-style (multi-provider) routes

- **POST /rgs/providers/<provider>/games/<gameId>/launch**  
  Returns iframe URL and config for that provider/game (token, lang, currency in body).

- **POST /rgs/providers/<provider>/games/<gameId>/round/start**  
  Start a round for that game (e.g. Scratch or Crash).  
  For Crash: start crash round. For others: scratch-style round using game math.

- **POST /rgs/providers/<provider>/games/<gameId>/round/cashout**  
  Crash only: cash out at current multiplier.

- **GET /rgs/providers/.../games/<gameId>/round/status**  
  Crash only: current crash status.

### Operator / session balance

- **GET /rgs/tx/balance**  
  Query: `session_id`, `game_code`, optional `device_type`.  
  Used by game UIs that have a session from `/game/launch`.  
  If `OPERATOR_ENDPOINT` is set, RGS calls the operator Balance API; otherwise returns a “not configured” JSON error.

### Admin

- **POST /rgs/admin/games/import-zip**  
  Import a game bundle (ZIP with manifest). Used to register new games into the catalog/storage.

### Proxy

- **POST /api/openai/images**  
  Proxies image-generation requests to OpenAI (needs `OPENAI_API_KEY`). Used by tooling that generates game assets.

---

## 6. How the RGS Uses the Platform

The **platform** is your existing backend (e.g. Next.js) that:

1. Authenticates users and issues **JWTs** (e.g. via `/api/auth/login` — **not** implemented in this repo).
2. Exposes wallet APIs the RGS expects:

| RGS calls | Platform must implement |
|-----------|--------------------------|
| Get balance | **GET** `/api/balance` — return `{ "balances": { ... } }`. |
| Debit (bet) | **POST** `/api/balance/bet` — body: currency, amount, gameName, gameProvider; return `{ "betId", "balances" }`. |
| Credit (win) | **POST** `/api/balance/win` — body: currency, amount, gameName, gameProvider. |
| Refund | **POST** `/api/balance/rollback` — body: `{ "betId" }`. |

All with **Authorization: Bearer &lt;JWT&gt;** (the token the frontend got at login and passed to the game/RGS).

So: **login and user management live on the platform**. The RGS only forwards the JWT to the platform for balance and bet/win/rollback.

---

## 7. Two Ways to Handle Money: JWT vs Operator

- **JWT (platform)**  
  Frontend has a JWT from your platform. It calls RGS with that JWT (e.g. `/rgs/balance`, `/rgs/round/start`, `/rgs/round/end`).  
  RGS uses **platform/client** to call `PLATFORM_URL` for balance/bet/win/rollback.

- **Operator (session)**  
  A partner calls **GET /game/launch** with `partner_id`, `player_id`, `game_id`, etc. RGS creates a **session** and returns a **game_url** that includes `session_id`.  
  The game UI then uses `session_id` for balance and bets. When **OPERATOR_ENDPOINT** is set, RGS uses **operator/client** to call your Operator API (balance, debit, credit) with signature.  
  So money is debited/credited via the operator, not via the JWT platform APIs.

---

## 8. Database (Optional but Common)

If **DATABASE_URL** is set, the RGS uses Postgres for:

- **games** — catalog of games (game_id, name, provider, enabled, status, photo, etc.).
- **operators** — operator_id, allowed_embed_domains, etc.
- **users** — created/resolved when using `/game/launch` (e.g. by player_id).
- **player_accounts** — links external player_id to user and operator.
- **game_sessions** — session_id, game_id, user_id, launch_url, home_url, exit_url, etc.

The **games registry** (`games/registry.go`) loads provider → game list from the **games** table. Game pages and launch only serve games that exist in DB and are active/enabled.

---

## 9. Game Types in This Codebase

- **Hi/Lo** — Classic “higher or lower” number game. Round state in `round.Store`; next number from `round.NextNumber()` (secure random 1–10).
- **Scratch** — Scratch-card style; uses **gamemath** (prize tables) and optional stored math per game.
- **Crash** — Multiplier crash game; has its own round/cashout flow and status.
- **Bundle games** — e.g. **lucky_star** or any game under `RGS_GAMES_DIR` with the expected structure (e.g. `project_scratch.json`, `math.json`, assets). Served from **GET /rgs/game/<gameId>**.

---

## 10. Game Importer CLI

**cmd/game_importer** is a separate binary. It:

1. Takes a **ZIP** path, a **storage root** directory, and a **base URL** (e.g. CDN).
2. Expects a **manifest.json** in the ZIP (game_id, name, provider, version, bundle_path, assets_path, math_path, thumbnail_path).
3. Extracts the ZIP under `storageRoot/games/<game_id>/v<version>/`.
4. Upserts a row into the **games** table (by game_id) with name, photo, provider, etc.

So: **this project runs the games and talks to wallet**; the **importer** is for adding new games (e.g. AI-generated) into the catalog and storage.

---

## 11. Run and Deploy

- **Local run**  
  From repo root:  
  `go run ./cmd/server`  
  Use `.env` (see `env.example`) to set `PLATFORM_URL`, `RGS_BASE_URL`, `PORT`/`RGS_PORT`, and optionally `DATABASE_URL`, `OPERATOR_*`.

- **Build**  
  `go build -o rgs ./cmd/server` then run `./rgs` (or `rgs.exe` on Windows).

- **Docker**  
  The repo’s `Dockerfile` builds the server from `./cmd/server` and runs it; **PORT** defaults to 8080 in the image.

- **Deploy**  
  e.g. Render (see `render.yaml`), Fly.io, Railway. Set **PLATFORM_URL** to your real platform URL and **RGS_BASE_URL** to the deployed RGS URL so iframe and redirect URLs are correct.

---

## 12. Why You Got 404 on `/api/auth/login`

This repo is **only the RGS**. It does **not** define `/api/auth/login`. That route must be implemented by your **platform** (e.g. your Next.js or other backend).  
Your frontend should call login on the **platform** URL (e.g. `http://localhost:3000/api/auth/login`), get a JWT, and then use that JWT when calling the **RGS** (e.g. `http://localhost:8094/rgs/balance`). So:

- **Login** → platform (e.g. port 3000).
- **Game balance / rounds** → RGS (e.g. port 8094).

---

## Quick Reference: Important Files to Read

| Goal | File(s) |
|------|--------|
| How the server starts and what it configures | `cmd/server/main.go`, `config/config.go` |
| What routes exist | `server/server.go` |
| How RGS talks to your platform | `platform/client.go` |
| Hi/Lo round logic and storage | `round/store.go`, and in `server/server.go` `roundStart` / `roundEnd` |
| How games are listed and launched | `games/registry.go`, `server/server.go` (handleGameLaunch, handleGamePage), `server/handlers_game.go` |
| Scratch/Crash and provider routes | `server/handlers_provider.go`, `server/handlers_game.go` |
| Operator (session) balance | `operator/client.go`, in `server/server.go` `handleTxBalance` |
| DB connection | `db.go` |

If you want to add a new route or game type, start from `server/server.go` (mux) and the corresponding `server/handlers_*.go` file.
