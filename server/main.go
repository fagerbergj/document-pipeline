package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/fagerbergj/document-pipeline/server/store/sqlite"
)

func main() {
	dbPath := flag.String("db", envOr("DB_PATH", "/data/pipeline.db"), "SQLite database path")
	migrationsDir := flag.String("migrations", envOr("MIGRATIONS_DIR", "db/migrations"), "Path to SQL migration files")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	db, err := sqlite.Open(*dbPath, *migrationsDir)
	if err != nil {
		log.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	log.Info("database ready", "path", *dbPath)
	// TODO: wire services, start HTTP server and worker
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
