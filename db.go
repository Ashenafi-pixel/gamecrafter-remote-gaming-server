package rgs

import (
	"database/sql"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
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
		config, err := pgx.ParseConfig(dsn)
		if err != nil {
			dbErr = err
			return
		}
		// Avoid "prepared statement already exists" with PgBouncer/Supabase: use simple protocol (no server-side prepared statements).
		config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		dbConn = stdlib.OpenDB(*config)
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
