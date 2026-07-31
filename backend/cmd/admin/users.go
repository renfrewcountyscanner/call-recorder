package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

func usersCommand(pool *pgxpool.Pool, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: call-recorder-admin users create|list|password|enable|disable ...")
		os.Exit(2)
	}
	switch args[0] {
	case "create":
		userCreate(pool, args[1:])
	case "list":
		userList(pool)
	case "password":
		userPassword(pool, args[1:])
	case "enable":
		userEnable(pool, args[1:])
	case "disable":
		userDisable(pool, args[1:])
	default:
		fmt.Fprintln(os.Stderr, "usage: call-recorder-admin users create|list|password|enable|disable ...")
		os.Exit(2)
	}
}

func userCreate(pool *pgxpool.Pool, args []string) {
	f := flag.NewFlagSet("users create", flag.ExitOnError)
	username := f.String("username", "", "username")
	password := f.String("password", "", "password")
	role := f.String("role", "viewer", "role (admin or viewer)")
	f.Parse(args)
	if *username == "" || *password == "" {
		fatal(errors.New("--username and --password are required"))
	}
	if *role != "admin" && *role != "viewer" {
		fatal(errors.New("--role must be admin or viewer"))
	}
	hash, err := hashAPIKey(*password)
	if err != nil {
		fatal(err)
	}
	ctx := context.Background()
	_, err = pool.Exec(ctx, `INSERT INTO users (username, password_hash, role, enabled) VALUES ($1, $2, $3, true)`, *username, hash, *role)
	if err != nil {
		if strings.Contains(err.Error(), "unique") {
			fatal(errors.New("username already exists"))
		}
		fatal(err)
	}
	fmt.Printf("user=%s role=%s created\n", *username, *role)
}

func userList(pool *pgxpool.Pool) {
	ctx := context.Background()
	rows, err := pool.Query(ctx, `SELECT username, role, enabled, created_at FROM users ORDER BY username`)
	if err != nil {
		fatal(err)
	}
	defer rows.Close()
	fmt.Printf("%-30s %-10s %-10s %s\n", "USERNAME", "ROLE", "ENABLED", "CREATED")
	for rows.Next() {
		var username, role string
		var enabled bool
		var created string
		if err := rows.Scan(&username, &role, &enabled, &created); err != nil {
			continue
		}
		enabledStr := "yes"
		if !enabled {
			enabledStr = "no"
		}
		fmt.Printf("%-30s %-10s %-10s %s\n", username, role, enabledStr, created)
	}
}

func userPassword(pool *pgxpool.Pool, args []string) {
	f := flag.NewFlagSet("users password", flag.ExitOnError)
	username := f.String("username", "", "username")
	password := f.String("password", "", "new password")
	f.Parse(args)
	if *username == "" || *password == "" {
		fatal(errors.New("--username and --password are required"))
	}
	hash, err := hashAPIKey(*password)
	if err != nil {
		fatal(err)
	}
	ctx := context.Background()
	tag, err := pool.Exec(ctx, `UPDATE users SET password_hash=$1, updated_at=now() WHERE username=$2`, hash, *username)
	if err != nil {
		fatal(err)
	}
	if tag.RowsAffected() == 0 {
		fatal(errors.New("user not found"))
	}
	fmt.Printf("user=%s password updated\n", *username)
}

func userEnable(pool *pgxpool.Pool, args []string) {
	f := flag.NewFlagSet("users enable", flag.ExitOnError)
	username := f.String("username", "", "username")
	f.Parse(args)
	if *username == "" {
		fatal(errors.New("--username is required"))
	}
	ctx := context.Background()
	tag, err := pool.Exec(ctx, `UPDATE users SET enabled=true, updated_at=now() WHERE username=$1`, *username)
	if err != nil {
		fatal(err)
	}
	if tag.RowsAffected() == 0 {
		fatal(errors.New("user not found"))
	}
	fmt.Printf("user=%s enabled\n", *username)
}

func userDisable(pool *pgxpool.Pool, args []string) {
	f := flag.NewFlagSet("users disable", flag.ExitOnError)
	username := f.String("username", "", "username")
	f.Parse(args)
	if *username == "" {
		fatal(errors.New("--username is required"))
	}
	ctx := context.Background()
	tag, err := pool.Exec(ctx, `UPDATE users SET enabled=false, updated_at=now() WHERE username=$1`, *username)
	if err != nil {
		fatal(err)
	}
	if tag.RowsAffected() == 0 {
		fatal(errors.New("user not found"))
	}
	fmt.Printf("user=%s disabled\n", *username)
}
