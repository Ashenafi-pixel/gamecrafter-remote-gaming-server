package main

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"latam-crypto/rgs"
)

// manifest represents the AI-generated manifest.json format.
// Example:
//
//	{
//	  "game_id": "my_slot_001",
//	  "name": "My Slot 001",
//	  "provider": "AI Studio",
//	  "version": "1.0.0",
//	  "bundle_path": "frontend/index.html",
//	  "assets_path": "frontend/assets/",
//	  "math_path": "math/index.json",
//	  "thumbnail_path": "frontend/assets/thumbnail.png"
//	}
type manifest struct {
	GameID        string `json:"game_id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Version       string `json:"version"`
	BundlePath    string `json:"bundle_path"`
	AssetsPath    string `json:"assets_path"`
	MathPath      string `json:"math_path"`
	ThumbnailPath string `json:"thumbnail_path"`
}

func main() {
	zipPath := flag.String("zip", "", "Path to AI-generated game ZIP file")
	storageRoot := flag.String("storage-root", "games_storage", "Local root directory representing object storage (e.g. mounted bucket)")
	baseURL := flag.String("base-url", "", "Base URL that fronts storageRoot (e.g. https://cdn.example.com)")
	flag.Parse()

	if *zipPath == "" {
		fmt.Fprintln(os.Stderr, "missing required -zip argument")
		os.Exit(1)
	}
	if *baseURL == "" {
		fmt.Fprintln(os.Stderr, "missing required -base-url argument")
		os.Exit(1)
	}

	if err := run(*zipPath, *storageRoot, *baseURL); err != nil {
		fmt.Fprintf(os.Stderr, "import failed: %v\n", err)
		os.Exit(1)
	}
}

func run(zipPath, storageRoot, baseURL string) error {
	db, err := rgs.GetDB()
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	if db == nil {
		return fmt.Errorf("DATABASE_URL is not set; cannot connect to DB")
	}

	m, targetPrefix, err := extractZip(zipPath, storageRoot)
	if err != nil {
		return err
	}

	// Build public URLs from baseURL + relative paths under storageRoot.
	relRoot := filepath.ToSlash(targetPrefix)
	if !strings.HasPrefix(relRoot, "/") {
		relRoot = "/" + relRoot
	}

	trimBase := strings.TrimRight(*&baseURL, "/")

	bundleURL := ""
	if m.BundlePath != "" {
		bundleURL = fmt.Sprintf("%s%s/%s", trimBase, relRoot, strings.TrimLeft(m.BundlePath, "/"))
	}

	assetsBaseURL := ""
	if m.AssetsPath != "" {
		assetsBaseURL = fmt.Sprintf("%s%s/%s", trimBase, relRoot, strings.TrimLeft(m.AssetsPath, "/"))
	}

	mathConfigURL := ""
	if m.MathPath != "" {
		mathConfigURL = fmt.Sprintf("%s%s/%s", trimBase, relRoot, strings.TrimLeft(m.MathPath, "/"))
	}

	manifestURL := fmt.Sprintf("%s%s/%s", trimBase, relRoot, "manifest.json")

	photoURL := ""
	if m.ThumbnailPath != "" {
		photoURL = fmt.Sprintf("%s%s/%s", trimBase, relRoot, strings.TrimLeft(m.ThumbnailPath, "/"))
	}

	if err := upsertGame(context.Background(), db, m, bundleURL, assetsBaseURL, mathConfigURL, manifestURL, photoURL); err != nil {
		return fmt.Errorf("upsert game: %w", err)
	}

	fmt.Printf("Imported game %q (game_id=%s, version=%s)\n", m.Name, m.GameID, m.Version)
	return nil
}

// extractZip extracts the AI ZIP into storageRoot/games/{game_id}/v{version}/
// and returns the parsed manifest plus the relative target prefix under storageRoot.
func extractZip(zipPath, storageRoot string) (*manifest, string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, "", fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	var manifestFile *zip.File

	// First, find manifest.json and parse it.
	for _, f := range r.File {
		// We allow manifest at root or one-level subdir: */manifest.json
		if strings.HasSuffix(f.Name, "manifest.json") {
			manifestFile = f
			break
		}
	}

	if manifestFile == nil {
		return nil, "", fmt.Errorf("manifest.json not found in zip")
	}

	rc, err := manifestFile.Open()
	if err != nil {
		return nil, "", fmt.Errorf("open manifest: %w", err)
	}
	defer rc.Close()

	var m manifest
	if err := json.NewDecoder(rc).Decode(&m); err != nil {
		return nil, "", fmt.Errorf("parse manifest: %w", err)
	}

	if m.GameID == "" || m.Version == "" {
		return nil, "", fmt.Errorf("manifest must include game_id and version")
	}

	targetPrefix := filepath.Join("games", m.GameID, "v"+m.Version)
	targetRoot := filepath.Join(storageRoot, targetPrefix)

	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return nil, "", fmt.Errorf("create storage dir: %w", err)
	}

	// Extract all files, preserving relative paths inside the ZIP under targetRoot.
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}

		relName := sanitizeZipPath(f.Name)
		if relName == "" {
			continue
		}

		destPath := filepath.Join(targetRoot, relName)

		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return nil, "", fmt.Errorf("mkdir for %s: %w", destPath, err)
		}

		src, err := f.Open()
		if err != nil {
			return nil, "", fmt.Errorf("open entry %s: %w", f.Name, err)
		}

		dst, err := os.Create(destPath)
		if err != nil {
			src.Close()
			return nil, "", fmt.Errorf("create dest %s: %w", destPath, err)
		}

		if _, err := io.Copy(dst, src); err != nil {
			src.Close()
			dst.Close()
			return nil, "", fmt.Errorf("copy %s: %w", f.Name, err)
		}

		src.Close()
		dst.Close()
	}

	return &m, targetPrefix, nil
}

// sanitizeZipPath normalizes a zip entry path to a safe, relative path.
func sanitizeZipPath(name string) string {
	// Remove any leading "./" or "/" and clean path.
	clean := filepath.Clean(strings.TrimLeft(name, "./\\"))
	// Prevent directory traversal.
	if strings.HasPrefix(clean, "..") {
		return ""
	}
	return clean
}

// upsertGame inserts or updates a games row in the game_crafter games table.
// Uses only columns that exist in game_crafter: name, status, photo, enabled, game_id, internal_name, provider.
func upsertGame(ctx context.Context, db *sql.DB, m *manifest, bundleURL, assetsBaseURL, mathConfigURL, manifestURL, photoURL string) error {
	var existingID string
	err := db.QueryRowContext(ctx, `SELECT id::text FROM games WHERE game_id = $1`, m.GameID).Scan(&existingID)
	switch {
	case err == sql.ErrNoRows:
		_, err = db.ExecContext(ctx, `
      INSERT INTO games (name, status, photo, enabled, game_id, internal_name, provider)
      VALUES ($1, 'ACTIVE', $2, true, $3, $4, $5)
    `,
			m.Name,
			nullableString(photoURL),
			m.GameID,
			m.GameID,
			m.Provider,
		)
		if err != nil {
			return fmt.Errorf("insert game: %w", err)
		}
	case err != nil:
		return fmt.Errorf("select game: %w", err)
	default:
		_, err = db.ExecContext(ctx, `
      UPDATE games SET name = $1, photo = COALESCE($2, photo), provider = $3, updated_at = CURRENT_TIMESTAMP WHERE game_id = $4
    `,
			m.Name,
			nullableString(photoURL),
			m.Provider,
			m.GameID,
		)
		if err != nil {
			return fmt.Errorf("update game: %w", err)
		}
	}
	return nil
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
