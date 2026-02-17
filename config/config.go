package config

import (
	"os"
	"strconv"
)

type Config struct {
	PlatformURL      string
	RGSBaseURL       string // Base URL for iframe launch (e.g. http://localhost:8081)
	RGSPort          int
	GameName         string
	GameProvider     string
	DataDir          string
	GamesDir         string // Root dir for game bundles (e.g. "games" under rgs/)
	OperatorEndpoint string
	OperatorSecret   string
}

func Load() *Config {
	platformURL := os.Getenv("PLATFORM_URL")
	if platformURL == "" {
		platformURL = "http://localhost:3000"
	}
	port := 8081
	// Prefer PORT (Render, Fly.io, Railway, etc.) then RGS_PORT
	if p := os.Getenv("PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			port = v
		}
	} else if p := os.Getenv("RGS_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			port = v
		}
	}
	gameName := os.Getenv("GAME_NAME")
	if gameName == "" {
		gameName = "Hi/Lo"
	}
	gameProvider := os.Getenv("GAME_PROVIDER")
	if gameProvider == "" {
		gameProvider = "Crypto LATAM"
	}
	dataDir := os.Getenv("RGS_DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	gamesDir := os.Getenv("RGS_GAMES_DIR")
	if gamesDir == "" {
		gamesDir = "games"
	}
	rgsBaseURL := os.Getenv("RGS_BASE_URL")
	if rgsBaseURL == "" {
		rgsBaseURL = "http://localhost:8081"
	}
	operatorEndpoint := os.Getenv("OPERATOR_ENDPOINT")
	operatorSecret := os.Getenv("OPERATOR_SECRET")
	return &Config{
		PlatformURL:      platformURL,
		RGSBaseURL:       rgsBaseURL,
		RGSPort:          port,
		GameName:         gameName,
		GameProvider:     gameProvider,
		DataDir:          dataDir,
		GamesDir:         gamesDir,
		OperatorEndpoint: operatorEndpoint,
		OperatorSecret:   operatorSecret,
	}
}
