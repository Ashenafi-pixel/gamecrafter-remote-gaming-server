# RGS Scratch Bundle Spec for GameCrafter

## 1. Goal

Define a **stable JSON contract** between GameCrafter scratch exports and our Remote Gaming Server (RGS) so that:

- Each game bundle (ZIP) contains **all configuration** needed for the RGS.
- The RGS can automatically:
  - Register the game in its catalog.
  - Load **math** and **mechanics**.
  - Load **symbol configuration** (IDs, categories, images).
- The frontend stays **generic** and only consumes RGS APIs.

Once this is in place, we can support **many scratch game types** (Match‑N, Target Match, Symbol Hunt, Pick One, etc.) without changing backend or frontend code.

---

## 2. Required files inside each bundle

Each game ZIP you send should contain at least:

- `index.html` – Game UI.
- `math.json` – Game math (prize tiers, probabilities, etc.).
- `rgs_config.json` – **New**: RGS‑specific config for mechanics & symbols (see below).
- Optional existing files:
  - `project_scratch.json`, `visuals.json`, etc. (used on your side and/or frontend).

We will rely primarily on `math.json` and `rgs_config.json` for the backend.

---

## 3. `rgs_config.json` schema

This file tells the RGS how to treat the game at a **mechanical** level and which symbols exist.

### 3.1 Top‑level structure

```jsonc
{
  "gameId": "130300089",          // numeric game ID as string; must match RGS DB `games.game_id`
  "mechanic": {
    "type": "match_n",           // "match_n" | "target_match" | "symbol_hunt" | (future)
    "match_count": 3,            // for match_n: 2 / 3 / 4 etc.
    "rows": 3,
    "cols": 3,
    "variant": "MATCH3_BASIC",   // optional, human‑readable label
    "extra": {                   // optional, mechanic‑specific fields
      // e.g. for target_match:
      // "winning_zone_size": 2,
      // "your_numbers_size": 8
    }
  },
  "symbols": [
    {
      "id": "cherry",
      "category": "win",         // "win" | "dud" | "top" | "bonus" (extend if needed)
      "image": "assets/cherry.png",
      "meta": {
        "label": "Cherry",
        "color": "#ff6384"
      }
    },
    {
      "id": "dud_1",
      "category": "dud",
      "image": "assets/dud_1.png",
      "meta": {
        "label": "Dud 1"
      }
    }
    // ...more symbols
  ]
}
```

#### Notes

- `gameId` is the **numeric code** that we will store in our `games` and `scratch_games` tables.  
  Example: if this bundle is meant to be `130300089`, then `"gameId": "130300089"`.
- `mechanic.type`:
  - `"match_n"` – classic Match‑N grid (Match‑2/3/4 on 3×3, 4×4, etc.).
  - `"target_match"` – “Winning Numbers” vs “Your Numbers”.
  - `"symbol_hunt"` – Symbol Hunt / Instant Win / Pick One.
- `rows * cols` defines the grid size (e.g. 3×3 = 9 entries in `revealMap`).

---

## 4. `math.json` expectations

We will treat `math.json` as a JSON representation of our `GameMath` structure:

```jsonc
{
  "schema_version": 1,
  "model_id": "130300089_default",   // may be omitted; we can default to <gameId>_default
  "model_version": "1.0",
  "mechanic": {
    "type": "match_n",
    "match_count": 3
  },
  "math_mode": "UNLIMITED",
  "win_logic": "SINGLE_WIN",
  "prize_table": [
    { "tier": "T0", "multiplier": 0,    "weight": 740000 },
    { "tier": "T1", "multiplier": 2.0,  "weight": 150000 },
    { "tier": "T2", "multiplier": 5.0,  "weight": 70000 },
    { "tier": "T3", "multiplier": 10.0, "weight": 30000 }
    // ... more tiers as needed
  ]
}
```

- We will:
  - Parse `math.json` into our `GameMath` type.
  - Store it as‑is in our `game_math` table (with `game_id = rgs_config.gameId`).
  - Use the `prize_table` weights to pick tiers and determine `tierId` and `finalPrize`.

You can continue to design math using your existing tools; just ensure the JSON matches this structure.

---

## 5. RGS behavior on import

When we receive your ZIP via:

```http
POST /rgs/admin/games/import-zip
Content-Type: application/zip
```

the RGS will:

1. Generate a numeric `game_id` (if not already assigned) and save the bundle under `games/<game_id>/...`.
2. Read `rgs_config.json`:
   - Store `mechanic` + `symbols` into `scratch_games` with `game_id = <gameId>` from the JSON.
3. Read `math.json`:
   - Store it into `game_math` with `game_id = <gameId>` and `model_id` (from `model_id` or `<gameId>_default`).
4. Insert/update the `games` catalog table.

Result: the game becomes automatically available via our APIs using the **numeric `gameId`**.

---

## 6. Runtime APIs the game frontend will use

Your scratch frontends (GameCrafter UIs) will **not** compute outcomes; they will:

### 6.1 Get symbols (dynamic config)

```http
GET /api/scratch/symbols?gameId=<numeric_game_id>
```

Response:

```jsonc
{
  "gameId": "130300089",
  "symbols": [
    { "id": "cherry", "category": "win", "image": "assets/cherry.png" },
    { "id": "dud_1",  "category": "dud", "image": "assets/dud_1.png" }
    // ...
  ]
}
```

### 6.2 Play a ticket

```http
POST /api/scratch/play
Content-Type: application/json
```

Body:

```jsonc
{
  "token": "SESSION_OR_PLATFORM_TOKEN",
  "gameId": "130300089",
  "betAmount": 1.0,
  "currency": "USD"
}
```

RGS will:

- Auth the session/wallet.
- Roll RNG using `game_math` for that game.
- Generate a `revealMap` grid using `mechanic` + `symbols`.
- Debit and (if win) credit the wallet.
- Return:

```jsonc
{
  "roundId": "r_...",
  "isWin": true,
  "tierId": "T2",
  "finalPrize": 10.0,
  "presentationSeed": 1710185234567,
  "revealMap": [
    "dud_1", "dud_2", "bar",
    "cherry", "bar", "dud_3",
    "dud_1", "bar", "dud_2"
  ]
}
```

The frontend:

- Uses `revealMap` with the `symbols` config to render.
- Uses `isWin`, `tierId`, `finalPrize` to display results.
- Never overrides the outcome.

---

## 7. Summary

If every GameCrafter scratch bundle includes:

- `math.json` with the parsheet data.
- `rgs_config.json` in the schema described above,

then the RGS can:

- Automatically ingest **any number of scratch game types**.
- Keep all math and mechanics in the backend (B2B aggregator style).
- Offer a clean, stable API to any frontend or operator.

Once you confirm this format (or propose tweaks), we’ll lock it in and wire our importer to use it.

