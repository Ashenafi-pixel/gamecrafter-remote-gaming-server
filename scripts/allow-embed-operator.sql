-- Allow operator 100001 to embed the game in https://crypto-latam.vercel.app
-- Run this in Supabase Dashboard → SQL Editor.

-- Option A: Set allowed_embed_domains to only this site (replace existing list)
UPDATE operators
SET allowed_embed_domains = '["crypto-latam.vercel.app"]'::jsonb,
    domain = 'https://crypto-latam.vercel.app'
WHERE operator_id = 100001;

-- Option B: Append to existing allowed_embed_domains (if you already have other domains, use this instead and comment out Option A)
-- UPDATE operators
-- SET allowed_embed_domains = COALESCE(allowed_embed_domains, '[]'::jsonb) || '["crypto-latam.vercel.app"]'::jsonb
-- WHERE operator_id = 100001;

-- Verify
SELECT operator_id, code, allowed_embed_domains, domain FROM operators WHERE operator_id = 100001;
