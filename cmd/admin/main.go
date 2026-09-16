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
		log.Fatal("usage: ADMIN_PASSWORD_FILE=... admin create email@example.com")
	}
	password := os.Getenv("ADMIN_PASSWORD")
	if path := os.Getenv("ADMIN_PASSWORD_FILE"); path != "" {
		value, err := os.ReadFile(path)
		if err != nil {
			log.Fatal(err)
		}
		password = string(value)
	}
	hash, e := auth.Password(password)
	if e != nil {
		log.Fatal(e)
	}
	email := strings.ToLower(strings.TrimSpace(os.Args[2]))
	var existingHash string
	var administrator bool
	e = db.QueryRow(ctx, "SELECT password_hash,admin FROM users WHERE email=$1", email).Scan(&existingHash, &administrator)
	if e == nil {
		if administrator && auth.Verify(existingHash, password) {
			fmt.Println("Administrator already exists.")
			return
		}
		log.Fatal("database already contains this account with different credentials")
	}
	var count int
	if e = db.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&count); e != nil {
		log.Fatal(e)
	}
	if count != 0 {
		log.Fatal("database already contains accounts; refusing first-install initialization")
	}
	id := auth.ID()
	tx, e := db.Begin(ctx)
	if e != nil {
		log.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "INSERT INTO users(id,email,password_hash,admin) VALUES($1,$2,$3,true)", id, email, hash); e != nil {
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
