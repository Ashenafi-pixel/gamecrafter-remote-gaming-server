# RGS (Remote Gaming Server) – Go

Remote Gaming Server in Go that communicates with the Crypto LATAM platform for wallet operations (bet, win, rollback) and runs Hi/Lo game logic.

## Requirements

- Go 1.22+
- Platform (Next.js app) running and reachable (e.g. `http://localhost:3000`)

## Configuration

Environment variables:

| Variable        | Default              | Description                    |
|----------------|----------------------|--------------------------------|
| `PLATFORM_URL` | `http://localhost:3000` | Base URL of the platform API   |
| `PORT` / `RGS_PORT` | `8081`          | Port the server listens on (cloud platforms usually set `PORT`) |
| `RGS_BASE_URL` | same as server URL   | Public URL of this RGS (for iframe/game launch; set to your deployed URL) |
| `RGS_DATA_DIR` | `data`               | Directory for round/game data (writable; ephemeral on many free tiers) |
| `GAME_NAME`    | `Hi/Lo`              | Game name sent to platform     |
| `GAME_PROVIDER`| `Crypto LATAM`       | Game provider sent to platform |

Copy `env.example` to `.env` and adjust if needed.

## Build and run

```bash
cd rgs
go build -o rgs .
./rgs
```

Or run directly:

```bash
go run .
```

Ensure the platform is running (`npm run dev` in the project root) so the RGS can call its balance APIs.

## API (RGS)

All requests/responses are JSON.

### Health

- **GET /health** – Returns `{"status":"ok","service":"rgs"}`.

### Balance (proxy to platform)

- **GET /rgs/balance** – Returns user balances. Send platform JWT via header `Authorization: Bearer <token>` or query `?token=<token>`.

### Hi/Lo round lifecycle

- **POST /rgs/round/start**  
  - Body: `{ "token": "<platform JWT>", "currency": "USD", "amount": 10, "roundId": "<optional>" }`  
  - RGS calls platform `POST /api/balance/bet`, stores round state, returns `{ "roundId", "currentNumber", "betId" }`.

- **POST /rgs/round/end**  
  - Body: `{ "token": "<platform JWT>", "roundId": "<from start>", "choice": "higher" | "lower" }`  
  - RGS resolves outcome, calls platform win or rollback, returns `{ "outcome": "win"|"lose"|"push", "nextNumber", "balanceDelta" }`.

## Platform integration

The RGS uses the platform’s existing balance APIs with the user’s JWT:

- **POST /api/balance/bet** – Place bet (debit).
- **POST /api/balance/win** – Credit win.
- **POST /api/balance/rollback** – Refund bet.
- **GET /api/balance** – Get balances.

All platform requests include `Authorization: Bearer <token>` and, where applicable, `gameName` and `gameProvider` in the body.

## Free deployment (Go backend)

Good free options for hosting the RGS:

| Platform   | Free tier | Notes |
|-----------|-----------|--------|
| **Render** | Free web service | Spins down after ~15 min idle (cold start on first request). Set root to `rgs`, build `go build -o rgs .`, start `./rgs`. |
| **Fly.io** | 3 shared VMs, 3GB | No spin-down. Use `fly launch` in `rgs/` or add a `Dockerfile`. |
| **Railway** | Free credit/month | Easy GitHub deploy; set root to `rgs`, build command `go build -o rgs .`, start `./rgs`. |

**Required env vars when deployed:**

- `PLATFORM_URL` = your Vercel frontend URL (e.g. `https://your-app.vercel.app`)
- `RGS_BASE_URL` = the public URL of the deployed RGS (e.g. `https://your-rgs.onrender.com` or your Fly/Railway URL)

Then set `RGS_URL` in your Vercel project to that same `RGS_BASE_URL` so the frontend can launch games.
