## Scratch Games – RGS Integration Guide (B2B Aggregator Style)

This document explains how the RGS now handles **all scratch outcomes** in a B2B‑grade way, and what is expected from:

- **RGS (backend)**
- **Frontend / GameCrafter scratch UIs**
- **Game designers / math team**

It also documents the main scratch API and payload contracts you can share with frontend engineers and designers.

---

## 1. Responsibilities Overview

### 1.1 RGS (backend)

- Is the **sole source of truth** for scratch outcomes (Match‑2/3/4, Symbol Hunt, Pick One, etc.).
- Uses:
  - `game_math` table for **math / prize tiers / RTP**.
  - `scratch_games` table for **mechanic + grid + symbols** per game.
- For each play:
  - Authenticates the session/token.
  - Validates game and limits.
  - Debits the ticket price via operator/platform wallet APIs.
  - Picks a tier using weighted math from `game_math`.
  - Generates a **`revealMap`** grid that follows the configured mechanic (Variant A/B/C).
  - Computes `finalPrize` and credits the win (if any).
  - Logs the transaction/round in DB.
  - Returns a **ResolvedOutcome** payload to the frontend.

The frontend never decides a win or runs RNG.

### 1.2 Frontend / GameCrafter scratch UI

- Calls the RGS scratch **play endpoint** (`POST /api/scratch/play`) when the player buys a ticket.
- Receives a **ResolvedOutcome** payload:
  - `roundId`, `isWin`, `tierId`, `finalPrize`, `presentationSeed`, `revealMap`.
- Uses:
  - `revealMap` to render the grid and scratch animation.
  - `isWin`, `tierId`, `finalPrize` for UI and messaging.
- Never changes or re‑rolls the outcome; scratching is purely visual.

### 1.3 Game designers / math team

- Define:
  - Prize tiers, multipliers, and weights (RTP, volatility).
  - Mechanics (Match‑N, Target Match, Symbol Hunt).
  - Symbol sets and categories (win/top/dud).
- Provide:
  - `game_math` rows with full GameMath JSON per game/variant.
  - `scratch_games` rows describing mechanics and symbol config.

---

## 2. Data Model (backend)

### 2.1 `game_math` table (math / RTP)

Defined by `scripts/001_game_math.sql`:

```sql
CREATE TABLE IF NOT EXISTS game_math (
  id         bigserial PRIMARY KEY,
  game_id    text NOT NULL,           -- e.g. 'MATCH3', 'MATCH2', 'PICKONE'
  model_id   text NOT NULL,           -- used as gamemath.GameMath.ModelID
  status     text NOT NULL DEFAULT 'ACTIVE', -- ACTIVE, DRAFT, ARCHIVED, etc.
  math       jsonb NOT NULL,          -- full GameMath JSON (schema_version, prize_table, etc.)
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
```

Key points:

- RGS loads all `status = 'ACTIVE'` rows into memory at startup via `loadGameMathFromDB()`.
- `math` JSON conforms to `gamemath.GameMath`:
  - `schema_version`, `model_id`, `model_version`
  - `mechanic` (optional, can be overridden)
  - `prize_table`: tiers with `id`, `multiplier`, `weight`.
- For scratch, a typical `math` JSON includes:
  - LOSE tier (multiplier = 0)
  - Multiple win tiers (2x, 5x, 10x, etc.) with weights matching the parsheet.

### 2.2 `scratch_games` table (mechanics + symbols)

You should create this table in Supabase:

```sql
CREATE TABLE IF NOT EXISTS scratch_games (
  id            bigserial PRIMARY KEY,
  game_id       text UNIQUE NOT NULL,   -- e.g. 'MATCH3', 'MATCH2', 'MATCH4', 'PICKONE'
  mechanic      jsonb NOT NULL,         -- { "type": "match_n", "match_count": 3, "rows": 3, "cols": 3 }
  symbol_config jsonb NOT NULL,         -- { "symbols": [ { "id": "cherry", "category": "win" }, ... ] }
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
```

Example row for a 3×3 Match‑3 game:

```sql
INSERT INTO scratch_games (game_id, mechanic, symbol_config)
VALUES (
  'MATCH3',
  '{
     "type": "match_n",
     "match_count": 3,
     "rows": 3,
     "cols": 3
   }'::jsonb,
  '{
     "symbols": [
       { "id": "cherry",  "category": "win" },
       { "id": "lemon",   "category": "win" },
       { "id": "bar",     "category": "win" },
       { "id": "bell",    "category": "win" },
       { "id": "777",     "category": "win" },
       { "id": "diamond", "category": "top" },
       { "id": "dud_1",   "category": "dud" },
       { "id": "dud_2",   "category": "dud" },
       { "id": "dud_3",   "category": "dud" }
     ]
   }'::jsonb
);
```

The RGS loads this into memory (`scratchConfigs`) via `loadScratchConfigs()`.

---

## 3. Scratch Play API (backend)

### 3.1 Endpoint

**Method:** `POST`  
**Path:** `/api/scratch/play`

### 3.2 Request body (JSON)

```jsonc
{
  "token": "SESSION_OR_PLATFORM_TOKEN",    // required; used to resolve session
  "session_id": "optional-session-id",     // optional; falls back to token if omitted
  "gameId": "MATCH3",                      // required; maps to scratch_games.game_id
  "betAmount": 1.0,                        // required; ticket price
  "currency": "USD",                       // required (defaults to "USD" if empty)
  "operatorId": 100001,                    // optional; reserved for future per-operator overrides
  "deviceType": "desktop"                  // optional; "desktop" or "mobile"
}
```

Notes:

- `token` / `session_id` must match an existing RGS `game_sessions.session_id` for wallet integration.
- `gameId` must correspond to:
  - A `scratch_games.game_id` row (mechanics + symbols).
  - A `game_math` row (math tiers) with matching `game_id`/`model_id`.

### 3.3 Response body (`ScratchResolvedOutcome`)

The RGS returns a **ResolvedOutcome** compatible with GameCrafter scratch UIs:

```jsonc
{
  "roundId": "r_9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
  "isWin": true,
  "tierId": "T2",                   // tier ID from math (e.g. Tier 2)
  "finalPrize": 10.0,               // absolute currency amount won
  "presentationSeed": 1710185234567,
  "revealMap": [
    "dud_1", "dud_2", "bar",
    "cherry", "bar", "dud_3",
    "dud_1", "bar", "dud_2"
  ]
}
```

Semantics:

- `roundId`: unique ID for the ticket, used for audit and replays.
- `isWin`: `true` if `finalPrize > 0`, else `false`.
- `tierId`: ID of the selected prize tier (maps back to paytable / UI config).
- `finalPrize`: net win amount in **currency units** (e.g. 10.00 for a $10 win).
- `presentationSeed`: optional random seed for frontend particles/animations.
- `revealMap`:
  - Flat 1D array of string IDs.
  - Length = `rows * cols` from mechanic config (e.g. 3×3 → 9).
  - Index 0 = top‑left; last index = bottom‑right.
  - Each string must exactly match a `SymbolConfig.id` in the GameCrafter project for that game.

### 3.4 Behavior per variant

The server uses `scratch_games.mechanic.type` to decide how to generate `revealMap`:

- **Variant A – Match‑N (`type = "match_n"`)**
  - On **win**:
    - Place exactly `match_count` copies of a winning symbol in distinct random cells.
    - Fill remaining cells with **dud** symbols.
    - Do not let any **other** symbol reach `match_count` occurrences (avoid accidental double wins).
  - On **loss**:
    - Fill grid with dud symbols (current implementation is safe baseline).
    - Later, you can add near‑miss logic (e.g. 2x top symbol) while still ensuring no symbol hits `match_count`.

- **Variant C – Symbol Hunt / Pick One (`type = "symbol_hunt"`)**
  - On **win**:
    - Ensure at least one special symbol (`category = "top"` or `"win"`) appears.
    - Fill remaining cells with duds.
  - On **loss**:
    - Fill grid entirely with duds (no top/special symbol on losing tickets).

- **Variant B – Target Match (`type = "target_match"`)**
  - Currently implemented as a **safe loss grid** (all duds).
  - You can extend this later with:
    - W = Winning Numbers zone
    - Y = Your Numbers zone
    - `intersection(W,Y) > 0` on wins, `0` on losses

The **math tier selection** (`tierId`, `finalPrize`) is handled by `game_math` and the `gamemath.PickTier()` function before revealMap generation.

---

## 4. Expected Actions by Role

### 4.1 Backend / RGS engineer (you)

- Ensure DB schema is applied:
  - `game_math` (already added via `scripts/001_game_math.sql`).
  - `scratch_games` (SQL above).
- Insert:
  - One or more `game_math` rows per scratch game (MATCH3, MATCH2, MATCH4, PICKONE, etc.).
  - One `scratch_games` row per scratch game with correct mechanics and symbol config.
- Deploy RGS (Render) with:
  - `DATABASE_URL` pointing to Supabase.
  - `RGS_BASE_URL=https://latam-rgs-backend.onrender.com`.
  - `RGS_GAMES_DIR=/var/data/rgs-games` and a Render Disk mounted there.
- Provide frontend/partners with:
  - The **`/api/scratch/play`** endpoint URL.
  - The request and response schemas from this document.

### 4.2 Frontend engineer (GameCrafter scratch UI)

- For each scratch game (MATCH2/3/4, Symbol Hunt, Pick One, etc.):
  - On “Play” button, call:

    ```http
    POST https://latam-rgs-backend.onrender.com/api/scratch/play
    Content-Type: application/json
    ```

    with body:

    ```json
    {
      "token": "<session-or-platform-token>",
      "gameId": "<GAME_ID_MATCHING_RGS_DB>",
      "betAmount": 1.0,
      "currency": "USD"
    }
    ```

  - Receive `ScratchResolvedOutcome`:
    - Apply `revealMap` to the client grid (length = rows*cols).
    - Use `isWin`, `tierId`, `finalPrize` to show results.
  - Do **not** compute outcomes locally or re‑roll RNG.

### 4.3 Game designers / math team

- Provide parsheets and JSON payloads for:
  - `game_math.math` (tiers, weights, multipliers).
  - `scratch_games.mechanic` (type, match_count, rows, cols).
  - `scratch_games.symbol_config.symbols` (IDs + categories).
- Validate:
  - RTP by simulating many rounds (backend tests).
  - No accidental wins on loss tickets (tests).

---

## 5. Summary

Your RGS now:

- Uses **Supabase Postgres** as system of record for scratch math and config.
- Generates scratch outcomes and `revealMap` **server‑side only**, in a B2B aggregator‑style architecture.
- Exposes a clean, stable **`POST /api/scratch/play`** interface that GameCrafter scratch games can integrate with easily.

This document should be enough for your frontend engineer and game designer to understand:

- Who does what,
- Which DB tables/config they need to populate,
- How to call the RGS and how to consume the responses.

