package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/smtp"
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
	_, _ = pool.Exec(ctx, `INSERT INTO notification_worker_heartbeat(id,worker_id,heartbeat_at,updated_at) VALUES(true,'notification-worker',now(),now()) ON CONFLICT(id) DO UPDATE SET worker_id=EXCLUDED.worker_id,heartbeat_at=now(),updated_at=now()`)
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
	rows, err := conn.Query(ctx, `SELECT d.id,d.destination_id,r.template,c.id,c.sender_id,c.system_id,c.site_id,c.talkgroup_id,coalesce(c.talkgroup_name,''),coalesce(c.radio_id,''),coalesce(c.radio_name,''),c.start_time,c.duration_ms,coalesce(c.call_type,''),coalesce(c.notes,''),coalesce(NULLIF((SELECT coalesce(NULLIF(t.edited_text,''),t.text) FROM transcripts t WHERE t.call_id=c.id ORDER BY t.updated_at DESC LIMIT 1),''),c.transcript,'') FROM notification_deliveries d JOIN notification_rules r ON r.id=d.rule_id JOIN calls c ON c.id=d.call_id WHERE (d.status='pending' OR d.status='failed' AND d.attempt_count<5) AND d.next_attempt_at<=now() ORDER BY r.priority DESC,d.id LIMIT 100`)
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
		result, _ := conn.Exec(ctx, `UPDATE notification_deliveries SET status='sending',attempt_count=attempt_count+1,last_attempt_at=now(),updated_at=now() WHERE id=$1 AND (status='pending' OR status='failed' AND attempt_count<5)`, j.id)
		if result.RowsAffected() != 1 {
			continue
		}
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
	if typ == "smtp" {
		return sendSMTPNotification(config, secret, body)
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
	var payload map[string]any
	switch typ {
	case "discord":
		payload = map[string]any{"content": body}
	case "telegram":
		payload = map[string]any{"text": body, "chat_id": config["chat_id"]}
	default:
		payload = map[string]any{"call_id": j.call, "text": body}
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := safeHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("destination returned HTTP %d", resp.StatusCode)
	}
	_ = typ
	_ = secret
	return nil
}

func sendSMTPNotification(config map[string]any, secret *string, body string) error {
	host, _ := config["host"].(string)
	port, _ := config["port"].(string)
	from, _ := config["from"].(string)
	to, _ := config["to"].(string)
	user, _ := config["username"].(string)
	useTLS, _ := config["tls"].(bool)
	if host == "" || port == "" || from == "" || to == "" {
		return errors.New("SMTP host, port, from, and to are required")
	}
	password := ""
	if secret != nil && *secret != "" {
		password = os.Getenv(*secret)
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.Dial("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	defer conn.Close()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if useTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if user != "" {
		if err := client.Auth(smtp.PlainAuth("", user, password, host)); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	headers := "From: " + from + "\r\nTo: " + to + "\r\nSubject: Call Recorder notification\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n"
	if _, err = io.WriteString(writer, headers+"<p>"+html.EscapeString(body)+"</p>"); err != nil {
		_ = writer.Close()
		return err
	}
	return writer.Close()
}

func validateNotificationURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return errors.New("invalid destination URL")
	}
	if strings.EqualFold(os.Getenv("CALL_RECORDER_ALLOW_PRIVATE_DESTINATIONS"), "true") {
		return nil
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return errors.New("private notification destinations are disabled")
	}
	if ip := net.ParseIP(h); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsLinkLocalMulticast()) {
		return errors.New("private notification destinations are disabled")
	}
	return nil
}

func safeHTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("redirects are disabled") }, Transport: &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.LookupIP(host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
				continue
			}
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		return nil, errors.New("destination resolves to a private address")
	}}}
}
func sendTestNotification(pool *pgxpool.Pool, id int64) {
	ctx := context.Background()
	var typ string
	var enabled bool
	var cfg []byte
	var secret *string
	if err := pool.QueryRow(ctx, `SELECT destination_type,enabled,config,secret_ref FROM notification_destinations WHERE id=$1`, id).Scan(&typ, &enabled, &cfg, &secret); err != nil {
		fatal(err)
	}
	if !enabled {
		fatal(errors.New("destination disabled"))
	}
	var config map[string]any
	_ = json.Unmarshal(cfg, &config)
	body := fmt.Sprintf("Test notification from Call Recorder — %s", time.Now().UTC().Format(time.RFC3339))
	if typ == "smtp" {
		if err := sendSMTPNotification(config, secret, body); err != nil {
			fatal(fmt.Errorf("test send failed: %w", err))
		}
		fmt.Printf("destination=%d type=%s test=sent\n", id, typ)
		return
	}
	endpoint, _ := config["url"].(string)
	if endpoint == "" {
		fatal(errors.New("destination URL missing"))
	}
	if !strings.HasPrefix(endpoint, "https://") && !strings.HasPrefix(endpoint, "http://") {
		fatal(errors.New("destination URL must use HTTP(S)"))
	}
	if err := validateNotificationURL(endpoint); err != nil {
		fatal(err)
	}
	var payload map[string]any
	switch typ {
	case "discord":
		payload = map[string]any{"content": body}
	case "telegram":
		payload = map[string]any{"text": body, "chat_id": config["chat_id"]}
	default:
		payload = map[string]any{"text": body}
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := safeHTTPClient().Do(req)
	if err != nil {
		fatal(fmt.Errorf("test send failed: %w", err))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fatal(fmt.Errorf("destination returned HTTP %d", resp.StatusCode))
	}
	fmt.Printf("destination=%d type=%s test=sent\n", id, typ)
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
