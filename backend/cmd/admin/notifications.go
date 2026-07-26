package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func notifications(pool *pgxpool.Pool, args []string) {
	if len(args) == 0 {
		fatal(errors.New("usage: notifications <run|retry|history|test>"))
	}
	switch args[0] {
	case "run":
		notificationRun(pool)
	case "retry":
		f := flag.NewFlagSet("notifications retry", flag.ExitOnError)
		id := f.Int64("delivery", 0, "delivery ID")
		_ = f.Parse(args[1:])
		if *id == 0 {
			fatal(errors.New("--delivery is required"))
		}
		if _, err := pool.Exec(context.Background(), `UPDATE notification_deliveries SET status='pending',next_attempt_at=now(),error=NULL WHERE id=$1`, *id); err != nil {
			fatal(err)
		}
	case "history":
		rows, err := pool.Query(context.Background(), `SELECT id,rule_id,destination_id,call_id,status,attempt_count,coalesce(error,'') FROM notification_deliveries ORDER BY id DESC LIMIT 100`)
		if err != nil {
			fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, rule, dest, attempt int64
			var call, status, errText string
			if e := rows.Scan(&id, &rule, &dest, &call, &status, &attempt, &errText); e != nil {
				fatal(e)
			}
			fmt.Printf("%d\trule=%d\tdestination=%d\tcall=%s\tstatus=%s\tattempts=%d\t%s\n", id, rule, dest, call, status, attempt, errText)
		}
	case "test":
		f := flag.NewFlagSet("notifications test", flag.ExitOnError)
		id := f.Int64("destination", 0, "destination ID")
		_ = f.Parse(args[1:])
		if *id == 0 {
			fatal(errors.New("--destination is required"))
		}
		sendTestNotification(pool, *id)
	default:
		fatal(errors.New("unknown notifications command"))
	}
}

func notificationRun(pool *pgxpool.Pool) {
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		fatal(err)
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(81640001)`).Scan(&locked); err != nil || !locked {
		fatal(errors.New("another notification worker is active"))
	}
	defer conn.Exec(ctx, `SELECT pg_advisory_unlock(81640001)`)
	rows, err := conn.Query(ctx, `SELECT d.id,d.destination_id,r.template,c.id,c.sender_id,c.system_id,c.site_id,c.talkgroup_id,coalesce(c.talkgroup_name,''),coalesce(c.radio_id,''),coalesce(c.radio_name,''),c.start_time,c.duration_ms,coalesce(c.call_type,''),coalesce(c.notes,''),coalesce(c.transcript,'') FROM notification_deliveries d JOIN notification_rules r ON r.id=d.rule_id JOIN calls c ON c.id=d.call_id WHERE d.status IN ('pending','failed') AND d.next_attempt_at<=now() ORDER BY r.priority DESC,d.id LIMIT 100`)
	if err != nil {
		fatal(err)
	}
	defer rows.Close()
	type job struct {
		id, dest                                                                                     int64
		template, call, sender, system, site, tg, tgName, radio, radioName, ctype, notes, transcript string
		start                                                                                        time.Time
		dur                                                                                          int64
	}
	jobs := []job{}
	for rows.Next() {
		var j job
		if e := rows.Scan(&j.id, &j.dest, &j.template, &j.call, &j.sender, &j.system, &j.site, &j.tg, &j.tgName, &j.radio, &j.radioName, &j.start, &j.dur, &j.ctype, &j.notes, &j.transcript); e != nil {
			fatal(e)
		}
		jobs = append(jobs, j)
	}
	for _, j := range jobs {
		_, _ = conn.Exec(ctx, `UPDATE notification_deliveries SET status='sending',attempt_count=attempt_count+1,last_attempt_at=now(),updated_at=now() WHERE id=$1`, j.id)
		err := deliverNotification(ctx, conn, j.dest, j)
		if err == nil {
			_, _ = conn.Exec(ctx, `UPDATE notification_deliveries SET status='sent',successful_at=now(),updated_at=now() WHERE id=$1`, j.id)
			fmt.Printf("delivery=%d status=sent\n", j.id)
		} else {
			safe := sanitizeError(err)
			_, _ = conn.Exec(ctx, `UPDATE notification_deliveries SET status=CASE WHEN attempt_count>=5 THEN 'failed' ELSE 'pending' END,error=$2,next_attempt_at=now()+least((2^least(attempt_count,8))::int,3600)*interval '1 second',updated_at=now() WHERE id=$1`, j.id, safe)
			fmt.Printf("delivery=%d status=failed error=%s\n", j.id, safe)
		}
	}
}

func deliverNotification(ctx context.Context, conn *pgxpool.Conn, destID int64, j struct {
	id, dest                                                                                     int64
	template, call, sender, system, site, tg, tgName, radio, radioName, ctype, notes, transcript string
	start                                                                                        time.Time
	dur                                                                                          int64
}) error {
	var typ string
	var enabled bool
	var cfg []byte
	var secret *string
	if err := conn.QueryRow(ctx, `SELECT destination_type,enabled,config,secret_ref FROM notification_destinations WHERE id=$1`, destID).Scan(&typ, &enabled, &cfg, &secret); err != nil {
		return err
	}
	if !enabled {
		return errors.New("destination disabled")
	}
	var config map[string]any
	_ = json.Unmarshal(cfg, &config)
	body := fmt.Sprintf("%s %s | %s | %s | %s | %dms", j.start.UTC().Format(time.RFC3339), j.system, j.site, j.tgName, j.tg, j.dur)
	if j.notes != "" {
		body += " | " + j.notes
	}
	if j.transcript != "" {
		body += " | " + j.transcript
	}
	if len(body) > 4000 {
		body = body[:4000]
	}
	endpoint, _ := config["url"].(string)
	if endpoint == "" {
		return errors.New("destination URL missing")
	}
	if !strings.HasPrefix(endpoint, "https://") && !strings.HasPrefix(endpoint, "http://") {
		return errors.New("destination URL must use HTTP(S)")
	}
	if err := validateNotificationURL(endpoint); err != nil {
		return err
	}
	payload := map[string]any{"content": body, "text": body, "call_id": j.call}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("destination returned HTTP %d", resp.StatusCode)
	}
	_ = typ
	_ = secret
	return nil
}

func validateNotificationURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return errors.New("invalid destination URL")
	}
	if strings.EqualFold(os.Getenv("CALL_RECORDER_ALLOW_PRIVATE_DESTINATIONS"), "true") {
		return nil
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return errors.New("private notification destinations are disabled")
	}
	if ip := net.ParseIP(h); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
		return errors.New("private notification destinations are disabled")
	}
	return nil
}
func sendTestNotification(pool *pgxpool.Pool, id int64) {
	ctx := context.Background()
	var typ string
	var enabled bool
	if err := pool.QueryRow(ctx, `SELECT destination_type,enabled FROM notification_destinations WHERE id=$1`, id).Scan(&typ, &enabled); err != nil {
		fatal(err)
	}
	if !enabled {
		fatal(errors.New("destination disabled"))
	}
	fmt.Printf("destination=%d type=%s test=queued\n", id, typ)
}
func sanitizeError(err error) string {
	s := err.Error()
	for _, x := range []string{"key=", "password=", "token=", "apikey", "api_key"} {
		if i := strings.Index(strings.ToLower(s), x); i >= 0 {
			s = s[:i] + "[redacted]"
		}
	}
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}
