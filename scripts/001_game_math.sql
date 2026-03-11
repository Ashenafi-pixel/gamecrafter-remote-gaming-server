-- Game math schema for RGS.
-- Phase 1: move math definitions into the database.

CREATE TABLE IF NOT EXISTS game_math (
  id               bigserial PRIMARY KEY,
  game_id          text NOT NULL,
  model_id         text NOT NULL,         -- links to gamemath.GameMath.ModelID
  status           text NOT NULL DEFAULT 'ACTIVE', -- e.g. ACTIVE, DRAFT, ARCHIVED
  math             jsonb NOT NULL,        -- full GameMath JSON (schema_version, prize_table, etc.)
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_game_math_game_id ON game_math(game_id);
CREATE INDEX IF NOT EXISTS idx_game_math_model_id ON game_math(model_id);
CREATE INDEX IF NOT EXISTS idx_game_math_status ON game_math(status);

