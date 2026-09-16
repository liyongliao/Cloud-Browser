package main

import (
	"cloudbrowser/internal/auth"
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx := context.Background()
	database, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal("invalid database configuration")
	}
	defer database.Close()
	if err = database.Ping(ctx); err != nil {
		log.Fatal("database connection failed: ", err)
	}
	tx, err := database.Begin(ctx)
	if err != nil {
		log.Fatal("database transaction failed: ", err)
	}
	defer tx.Rollback(ctx)
	suffix := auth.ID()
	if _, err = tx.Exec(ctx, `CREATE TABLE cloud_browser_permission_check_`+suffix+` (id integer)`); err != nil {
		log.Fatal("database account cannot create application tables: ", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		log.Fatal("database permission check rollback failed: ", err)
	}
	fmt.Println("Database connection and create-table permission verified.")
}
