## Render Disk setup for RGS game bundles

This document describes how to configure **Render Disk** so imported game bundles
(`index.html` + `assets/` etc.) are stored on **persistent storage** and survive
redeploys and restarts.

The goal is:

- Game imports via `POST /rgs/admin/games/import-zip` write to a Render Disk.
- Game launch (`/game/launch` + `/rgs/game/<game_id>`) always finds
  `index.html` and assets on that disk for any active game.

The codebase is already wired to read the games root from the env var
`RGS_GAMES_DIR`, defaulting to `games/` in the project root. We will point it
at the Render Disk mount path instead.

---

## 1. Render configuration (UI)

1. Open your **Render** dashboard and navigate to the `latam-rgs-backend` web service.
2. Go to the **Disks** tab and click **Add Disk**:
   - **Name**: `rgs-games-disk` (or any name).
   - **Size**: choose a size that fits your game bundles (e.g. 10–20 GB to start).
   - **Mount path**: `/var/data/rgs-games`
   - **Environment**: attach it to the `latam-rgs-backend` service.
3. Save and deploy. After this, every deploy will mount the disk at
   `/var/data/rgs-games` inside the container.

> Render Disks are **persistent**: files written there survive deploys and restarts.

---

## 2. Environment variable: `RGS_GAMES_DIR`

The Go config (`config/config.go`) reads:

- `RGS_GAMES_DIR` → `cfg.GamesDir`
- Default: `"games"` (relative folder in the repo)

On Render we want game bundles to live on the disk, so `RGS_GAMES_DIR` must be:

```text
/var/data/rgs-games
```

There are two ways to ensure this:

### 2.1 Via `render.yaml` (blueprint)

The `render.yaml` already includes:

```yaml
envVars:
  - key: DATABASE_URL
    sync: false
  - key: OPERATOR_SECRET
    sync: false
  - key: RGS_BASE_URL
    value: https://latam-rgs-backend.onrender.com
  - key: RGS_GAMES_DIR
    value: /var/data/rgs-games
```

If you use blueprint deploys, this will be applied automatically.

### 2.2 Via Render dashboard (Environment tab)

Alternatively (or to override), in the Render UI:

1. Go to the **Environment** tab for `latam-rgs-backend`.
2. Add or edit the variable:

   - **Key**: `RGS_GAMES_DIR`
   - **Value**: `/var/data/rgs-games`

3. Save and redeploy.

---

## 3. How imports and launch work with the disk

### 3.1 Importing a game

The import endpoint:

- `POST /rgs/admin/games/import-zip`
- `Content-Type: application/zip`
- Body: ZIP exported from GameCrafter (or the AI tool) containing:
  - `index.html`
  - `assets/...`
  - `math.json`, `project_scratch.json`, `visuals.json`, etc.

Handler flow (`server/handlers_import.go`):

1. `handleImportZip` reads the ZIP bytes.
2. Generates a numeric `game_id` via `nextNumericGameID`.
3. Calls `extractGameBundleZip(gameID, body)`:
   - Computes `targetRoot := filepath.Join(s.cfg.GamesDir, gameID)`.
   - With `RGS_GAMES_DIR=/var/data/rgs-games`, this becomes:

     ```text
     /var/data/rgs-games/<game_id>/
     ```

   - Extracts all files there:
     - `/var/data/rgs-games/<game_id>/index.html`
     - `/var/data/rgs-games/<game_id>/assets/...`
4. Calls `upsertGameInDB` to register the game in the `games` table.

Because `s.cfg.GamesDir` points to the Render Disk, the extracted bundle is
persisted across deploys.

### 3.2 Launching a game

When the operator/platform calls:

```text
GET /game/launch?...&game_id=<game_id>&...
```

`handleGameLaunch`:

1. Validates parameters and that the game exists in `games` (ACTIVE, enabled).
2. Creates a `game_sessions` row.
3. Builds `game_url` like:

   ```text
   <RGS_BASE_URL>/rgs/game/<game_id>?session_id=...
   ```

The platform or casino then iframes `game_url`.

The iframe hits:

```text
GET /rgs/game/<game_id>?token=...&lang=...&currency=...
```

`handleGamePage`:

1. Validates the session and game.
2. Uses `bundleGameExists` and `serveBundleGame`:
   - `bundleRoot(gameID) = filepath.Join(s.cfg.GamesDir, gameID)`
   - With disk config: `/var/data/rgs-games/<game_id>`
   - Reads `index.html` from that directory and injects `window.RGS_CONFIG`.

Asset requests like:

```text
GET /rgs/game/<game_id>/assets/img_123.png
```

are served by `serveBundleAsset`, which also uses `bundleRoot` and therefore
reads from the Render Disk.

---

## 4. Operational checklist

1. **Create Render Disk**
   - Name: `rgs-games-disk`
   - Mount path: `/var/data/rgs-games`
   - Attach to `latam-rgs-backend` service.

2. **Set `RGS_GAMES_DIR`**
   - Via `render.yaml` or the Render Environment tab:

     ```text
     RGS_GAMES_DIR=/var/data/rgs-games
     ```

3. **Redeploy the service**
   - Trigger a deploy so the disk is mounted and env vars are applied.

4. **Import games (on Render, not just locally)**

   - From your local machine or a script:

     ```bash
     curl -X POST "https://latam-rgs-backend.onrender.com/rgs/admin/games/import-zip" \
       -H "Content-Type: application/zip" \
       --data-binary "@path/to/game_bundle.zip"
     ```

   - The response will include `{ "ok": true, "game_id": "130300000", ... }`.

5. **Launch test**

   - Call:

     ```text
     GET https://latam-rgs-backend.onrender.com/game/launch?...&game_id=<game_id>&...
     ```

   - Open the returned `game_url` in a browser (or embed it in an iframe).
   - Verify the game loads and assets are served from the RGS.

6. **Persistence across deploys**

   - After a new deploy, you **do not** need to re-import the game:
     - The files live on the Render Disk at `/var/data/rgs-games/<game_id>`.
     - The DB rows in `games` and `game_sessions` remain in Supabase.

---

## 5. Notes and best practices

- Keep disk size under control; periodically clean up old game bundles that are
  no longer used.
- For backup / disaster recovery, you may want to periodically sync
  `/var/data/rgs-games` to external object storage (S3/R2/etc.).
- Long term, consider moving large static assets to CDN-backed object storage,
  but this Render Disk setup is sufficient to launch and run the business
  without losing games on each deploy.

