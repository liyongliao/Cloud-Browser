package main

import (
	"cloudbrowser/internal/auth"
	"cloudbrowser/migrations"
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"os"
	"strings"
)

func main() {
	ctx := context.Background()
	db, e := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		log.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(ctx, migrations.SQL); e != nil {
		log.Fatal(e)
	}
	if len(os.Args) != 3 || os.Args[1] != "create" {
		log.Fatal("usage: ADMIN_PASSWORD=... admin create email@example.com")
	}
	hash, e := auth.Password(os.Getenv("ADMIN_PASSWORD"))
	if e != nil {
		log.Fatal(e)
	}
	id := auth.ID()
	tx, e := db.Begin(ctx)
	if e != nil {
		log.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "INSERT INTO users(id,email,password_hash,admin) VALUES($1,$2,$3,true)", id, strings.ToLower(strings.TrimSpace(os.Args[2])), hash); e != nil {
		log.Fatal(e)
	}
	if _, e = tx.Exec(ctx, "INSERT INTO profiles(id,user_id) VALUES($1,$1)", id); e != nil {
		log.Fatal(e)
	}
	if _, e = tx.Exec(ctx, "INSERT INTO sessions(id,profile_id) VALUES($1,$1)", id); e != nil {
		log.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		log.Fatal(e)
	}
	fmt.Println("Administrator created.")
}
