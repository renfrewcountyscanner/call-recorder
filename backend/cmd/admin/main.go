// Command call-recorder-admin administers recorder senders without exposing
// their API keys through the web application or application logs.
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "sender" {
		usage()
	}
	databaseURL := os.Getenv("CALL_RECORDER_DATABASE_URL")
	if databaseURL == "" {
		fatal(errors.New("CALL_RECORDER_DATABASE_URL is required"))
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		fatal(err)
	}
	switch os.Args[2] {
	case "create":
		createOrReplace(pool, false, os.Args[3:])
	case "replace":
		createOrReplace(pool, true, os.Args[3:])
	case "disable":
		disable(pool, os.Args[3:])
	default:
		usage()
	}
}

func createOrReplace(pool *pgxpool.Pool, replace bool, args []string) {
	name := senderName(args)
	key, err := generateKey()
	if err != nil {
		fatal(err)
	}
	hash, err := hashAPIKey(key)
	if err != nil {
		fatal(err)
	}
	ctx := context.Background()
	if replace {
		_, err = pool.Exec(ctx, `INSERT INTO remote_senders (sender_id,key_hash,enabled) VALUES ($1,$2,true) ON CONFLICT (sender_id) DO UPDATE SET key_hash=EXCLUDED.key_hash, enabled=true`, name, []byte(hash))
	} else {
		_, err = pool.Exec(ctx, `INSERT INTO remote_senders (sender_id,key_hash,enabled) VALUES ($1,$2,true)`, name, []byte(hash))
	}
	if err != nil {
		fatal(err)
	}
	// This is deliberately the only output containing the newly generated key.
	fmt.Printf("sender=%s\napi_key=%s\n", name, key)
}

func disable(pool *pgxpool.Pool, args []string) {
	name := senderName(args)
	result, err := pool.Exec(context.Background(), `UPDATE remote_senders SET enabled=false WHERE sender_id=$1`, name)
	if err != nil {
		fatal(err)
	}
	if result.RowsAffected() == 0 {
		fatal(fmt.Errorf("sender %q does not exist", name))
	}
	fmt.Printf("sender=%s disabled\n", name)
}

func senderName(args []string) string {
	flags := flag.NewFlagSet("sender", flag.ExitOnError)
	name := flags.String("name", "", "unique sender name")
	_ = flags.Parse(args)
	if strings.TrimSpace(*name) == "" || len(*name) > 128 {
		fatal(errors.New("--name is required and must be at most 128 characters"))
	}
	return strings.TrimSpace(*name)
}

func generateKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
func hashAPIKey(value string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	digest := argon2.IDKey([]byte(value), salt, 3, 64*1024, 2, 32)
	return "argon2id$v=19$m=65536,t=3,p=2$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(digest), nil
}
func verifyAPIKey(encoded, value string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return false
	}
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil || memory == 0 || iterations == 0 || parallelism == 0 {
		return false
	}
	salt, err := hex.DecodeString(parts[3])
	if err != nil || len(salt) == 0 {
		return false
	}
	expected, err := hex.DecodeString(parts[4])
	if err != nil || len(expected) == 0 {
		return false
	}
	actual := argon2.IDKey([]byte(value), salt, iterations, memory, parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
func usage() {
	fmt.Fprintln(os.Stderr, "usage: call-recorder-admin sender <create|replace|disable> --name NAME")
	os.Exit(2)
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "error:", err); os.Exit(1) }
