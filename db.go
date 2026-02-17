package rgs

import (
	"database/sql"
	"os"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var (
	dbOnce sync.Once
	dbConn *sql.DB
	dbErr  error
)

func GetDB() (*sql.DB, error) {
	dbOnce.Do(func() {
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" {
			dbErr = nil
			return
		}
		dbConn, dbErr = sql.Open("pgx", dsn)
		if dbErr != nil {
			return
		}
		// Pool settings for Supabase/Render: idle timeout 4m, limit open conns for pooler
		dbConn.SetConnMaxIdleTime(4 * time.Minute)
		dbConn.SetMaxOpenConns(10)
		dbConn.SetMaxIdleConns(2)
		dbErr = dbConn.Ping()
	})
	if dbErr != nil {
		return nil, dbErr
	}
	return dbConn, nil
}
