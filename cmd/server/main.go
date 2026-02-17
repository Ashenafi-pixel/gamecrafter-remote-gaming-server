package main

import (
	"log"

	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/config"
	"github.com/Ashenafi-pixel/gamecrafter-remote-gaming-server/server"

	"github.com/joho/godotenv"
)

func main() {
	// Load .env so DATABASE_URL is set: rgs/.env, cwd .env, or project root .env/.env.local
	_ = godotenv.Load(".env")
	_ = godotenv.Load("rgs/.env")
	_ = godotenv.Load("../.env")
	_ = godotenv.Load("../.env.local")
	cfg := config.Load()
	srv := server.New(cfg)
	if err := srv.Run(); err != nil {
		log.Fatal(err)
	}
}
