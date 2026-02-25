## OpenAI Image Proxy – Go Backend Integration Plan

### 1. Goal

Move all image generation to a **single Go backend endpoint** so:

- The **frontend never talks directly to `api.openai.com`**.
- The **API key lives only on the backend**.
- The existing React frontend (via `enhancedOpenaiClient`) keeps working with minimal changes by calling a configurable proxy URL.

---

### 2. Frontend → Backend Contract

#### Endpoint (Go backend)

- **Method**: `POST`
- **Path**: `POST /api/openai/images`  
  (You can choose `/api/v1/openai/images` if you prefer versioned routes.)

#### Request body (JSON)

Match what `enhancedOpenaiClient` already sends into the proxy:

```jsonc
{
  "model": "gpt-image-1.5",      // optional, default if empty
  "prompt": "High-end mobile game background...", // required
  "n": 1,                        // optional, default 1
  "size": "1024x1536",           // optional, default "1024x1024"
  "quality": "high"              // optional, default "standard"
  // optionally later: "referenced_image_ids": [...]
}
```

#### Response body (JSON)

Return the **same shape as OpenAI Images API** (`v1/images/generations`), so the existing TS client keeps working:

```jsonc
{
  "created": 1234567890,
  "data": [
    {
      "url": "https://...",
      "b64_json": null,          // or base64 if you choose that format
      "revised_prompt": "..."
    }
  ]
}
```

`enhancedOpenaiClient` already expects `data[0].url` or `data[0].b64_json`, so no frontend parsing changes are required.

---

### 3. Go Backend Implementation Steps

#### 3.1 Configuration / Env

Add environment variables in your Go backend:

- `OPENAI_API_KEY` – **required**, the secret OpenAI key.
- `OPENAI_ORG_ID` – optional, if you use org scoping.

Load them once at startup (or via your preferred config system). If `OPENAI_API_KEY` is missing, log a fatal error on startup or return a 500 from the handler with a clear message.

#### 3.2 Request types

Define a Go struct that mirrors the request JSON:

```go
type ImageRequest struct {
    Model   string `json:"model"`
    Prompt  string `json:"prompt"`
    N       int    `json:"n"`
    Size    string `json:"size"`
    Quality string `json:"quality"`
    // ReferencedImageIDs []string `json:"referenced_image_ids,omitempty"`
}
```

Optionally define a response struct, or just use `map[string]any` / `json.RawMessage` to pass OpenAI’s response through unchanged.

#### 3.3 Handler logic (`POST /api/openai/images`)

1. **Parse JSON** from the request body into `ImageRequest`.
2. **Validate / default:**
   - If `Prompt` is empty → return `400` with `{ "error": "prompt is required" }`.
   - If `Model` is empty → set to `"gpt-image-1.5"` (or your chosen model).
   - If `N == 0` → set to `1`.
   - If `Size` is empty → set to `"1024x1024"`.
   - If `Quality` is empty → set to `"standard"` or `"high"` depending on your desired default.
3. **Build OpenAI request body:**

   ```go
   payload := map[string]any{
       "model":   req.Model,
       "prompt":  req.Prompt,
       "n":       req.N,
       "size":    req.Size,
       "quality": req.Quality,
       // "referenced_image_ids": req.ReferencedImageIDs,
       // "response_format": "url", // optional; default is url
   }
   ```

4. **Call OpenAI Images API:**
   - URL: `https://api.openai.com/v1/images/generations`
   - Use `net/http` with a client timeout ~90–120 seconds.
   - Headers:
     - `Authorization: Bearer <OPENAI_API_KEY>`
     - `Content-Type: application/json`
     - optionally `OpenAI-Organization: <OPENAI_ORG_ID>`
5. **Error handling:**
   - If OpenAI response status is **not 2xx**:
     - Read response body as text.
     - Log status + body (for debugging).
     - Return e.g. `502`/`500` with JSON:

       ```jsonc
       {
         "error": "OpenAI API error: <status>",
         "details": "<raw openai response text>"
       }
       ```

6. **Success case:**
   - Decode the JSON response from OpenAI.
   - Optionally validate that `data` exists and has at least one element.
   - Return the decoded JSON directly to the frontend:
     - Status `200`.
     - Header `Content-Type: application/json`.

#### 3.4 CORS (if needed)

If your Go API runs on a different origin from the React app (e.g. different domain or port), enable CORS:

- Allow origins: include your Vercel domain(s), e.g. `https://gamecrafter-dev.vercel.app`.
- Allowed methods: `POST`, `OPTIONS`.
- Allowed headers: at least `Content-Type`.

You can use a middleware like `github.com/rs/cors` or your own.

---

### 4. Frontend Wiring (`VITE_OPENAI_IMAGE_PROXY` / `VITE_RGS_URL`)

The React client uses a configurable proxy URL in `enhancedOpenaiClient.ts`:

```ts
const IMAGE_PROXY = (() => {
  if (typeof import.meta === 'undefined') {
    return '/.netlify/functions/openai-images';
  }
  const env = import.meta.env || {};

  if (env.VITE_OPENAI_IMAGE_PROXY) {
    return env.VITE_OPENAI_IMAGE_PROXY as string;
  }

  if (env.VITE_RGS_URL) {
    const base = (env.VITE_RGS_URL as string).replace(/\/$/, '');
    return `${base}/api/openai/images`;
  }

  return '/.netlify/functions/openai-images';
})();
```

That means, in order of priority:

1. If `VITE_OPENAI_IMAGE_PROXY` is set → **frontend calls that URL directly**.
2. Else, if `VITE_RGS_URL` is set → frontend calls `VITE_RGS_URL + "/api/openai/images"`.
3. Else, it falls back to the old Netlify-style path `/.netlify/functions/openai-images` (for legacy/dev setups).

#### 4.1 Production (e.g. Vercel + Go backend)

In your production env (Vercel or similar) you have two equivalent options:

- **Option A (recommended with current wiring)**  
  Set only:
  - `VITE_RGS_URL=https://your-go-backend-domain`  
  and expose your Go endpoint at:
  - `POST https://your-go-backend-domain/api/openai/images`

  The frontend will automatically build the full image proxy URL from `VITE_RGS_URL`.

- **Option B (explicit override)**  
  Set:
  - `VITE_OPENAI_IMAGE_PROXY=https://your-go-backend-domain/api/openai/images`

  This overrides `VITE_RGS_URL` for image generation if you ever need a different host or path.

#### 4.2 Local dev

Two options:

- **Use Go backend locally as well**:
  - Run Go API on e.g. `http://localhost:8081`.
  - In `.env`:
    ```env
    VITE_RGS_URL=http://localhost:8081
    ```
    or explicitly:
    ```env
    VITE_OPENAI_IMAGE_PROXY=http://localhost:8081/api/openai/images
    ```
  - Run `npm run dev` and your Go server; the frontend will call the Go API directly.

- **Keep existing Vite dev proxy** (if desired):
  - Leave both `VITE_OPENAI_IMAGE_PROXY` and `VITE_RGS_URL` unset locally so dev still uses `/.netlify/functions/openai-images` via the Vite plugin.
  - In prod, set `VITE_RGS_URL` (or `VITE_OPENAI_IMAGE_PROXY`) to point at the real Go backend.

---

### 5. Testing Checklist

1. **Backend unit test / curl**

   ```bash
   curl -X POST https://your-go-backend/api/openai/images \
     -H \"Content-Type: application/json\" \
     -d '{\"prompt\":\"test symbol\",\"size\":\"1024x1024\",\"quality\":\"standard\"}'
   ```

   - Expect `200` with `{ "data": [ { "url": ... }] }`.

2. **Frontend integration (local)**
   - Run Go backend + frontend.
   - Trigger symbol/background generation.
   - In DevTools → Network, confirm:
     - Request goes to `/api/openai/images` (or configured proxy).
     - Response has `data[0].url`.

3. **Production (Vercel + Go API)**
   - Confirm `VITE_OPENAI_IMAGE_PROXY` is set on Vercel.
   - Redeploy.
   - Trigger image generation and verify:
     - No calls to `/.netlify/functions/openai-images` on Vercel.
     - Requests go to your Go backend URL and succeed.

This document can be given directly to a Cursor agent in your Go backend project as a spec for implementing the `POST /api/openai/images` endpoint and wiring it to OpenAI.

