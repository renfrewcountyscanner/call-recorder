package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/renfrewcountyscanner/call-recorder/backend/internal/transcription"
)

func pluralSuffix(n int64) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (s *server) enqueueNotifications(ctx context.Context, callID string) {
	_, _ = s.db.Exec(ctx, `INSERT INTO notification_deliveries(rule_id,destination_id,call_id)
		SELECT r.id,r.destination_id,c.id
		FROM notification_rules r
		JOIN notification_destinations d ON d.id=r.destination_id
		JOIN calls c ON c.id=$1
		WHERE r.enabled AND d.enabled
		  AND (r.sender_filter IS NULL OR r.sender_filter=c.sender_id)
		  AND (r.system_filter IS NULL OR r.system_filter=c.system_id)
		  AND (r.site_filter IS NULL OR r.site_filter=c.site_id)
		  AND (r.talkgroup_filter IS NULL OR r.talkgroup_filter=c.talkgroup_id)
		  AND (r.radio_filter IS NULL OR r.radio_filter=c.radio_id)
		  AND (r.call_type_filter IS NULL OR r.call_type_filter=c.call_type)
		  AND (r.frequency_min IS NULL OR (c.frequency ~ '^[0-9]+([.][0-9]+)?$' AND c.frequency::numeric >= r.frequency_min))
		  AND (r.frequency_max IS NULL OR (c.frequency ~ '^[0-9]+([.][0-9]+)?$' AND c.frequency::numeric <= r.frequency_max))
		  AND (r.min_duration_ms IS NULL OR c.duration_ms >= r.min_duration_ms)
		  AND (r.max_duration_ms IS NULL OR c.duration_ms <= r.max_duration_ms)
		  AND (NOT r.patched_only OR EXISTS (SELECT 1 FROM call_targets ct WHERE ct.call_id=c.id))
		  -- Keyword rules are transcript-only. A call with no transcript must
		  -- wait for the transcription worker to enqueue it after transcription.
		  AND (r.keyword IS NULL OR EXISTS (SELECT 1 FROM transcripts t WHERE t.call_id=c.id AND coalesce(NULLIF(t.edited_text,''),NULLIF(t.text,''),'') ILIKE '%'||r.keyword||'%'))
		  AND (r.favourite_group_id IS NULL OR EXISTS (SELECT 1 FROM favourite_members fm WHERE fm.group_id=r.favourite_group_id AND fm.system_id=c.system_id AND fm.talkgroup_id=c.talkgroup_id))
		ON CONFLICT(rule_id,call_id) DO NOTHING`, callID)
}

func (s *server) adminProtectCall(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/protect/")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "invalid call ID", 400)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	protected := r.FormValue("protected") == "on" || r.FormValue("protected") == "true"
	reason := strings.TrimSpace(r.FormValue("reason"))
	if len(reason) > 500 {
		http.Error(w, "reason too long", 400)
		return
	}
	identity := strings.TrimSpace(r.Header.Get("Cf-Access-Authenticated-User-Email"))
	if identity == "" {
		identity = "admin-token"
	}
	if protected {
		_, _ = s.db.Exec(r.Context(), `UPDATE calls SET protected=true,protection_reason=$2,protected_at=now(),protected_by=$3,protection_expires_at=NULLIF($4,'')::timestamptz WHERE id=$1`, id, reason, identity, r.FormValue("expires_at"))
	} else {
		_, _ = s.db.Exec(r.Context(), `UPDATE calls SET protected=false,protection_reason=NULL,protected_at=NULL,protected_by=NULL,protection_expires_at=NULL WHERE id=$1`, id)
	}
	_, _ = s.db.Exec(r.Context(), `INSERT INTO protection_events(call_id,protected,reason,identity) VALUES($1,$2,$3,$4)`, id, protected, reason, identity)
	http.Redirect(w, r, "/call/"+id, http.StatusSeeOther)
}

func (s *server) adminFavourites(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT g.id,g.name,coalesce(g.description,''),g.enabled,g.display_order,
		(SELECT count(*) FROM favourite_members m WHERE m.group_id=g.id),
		(SELECT count(*) FROM calls c JOIN favourite_members m ON m.system_id=c.system_id AND m.talkgroup_id=c.talkgroup_id WHERE m.group_id=g.id)
		FROM favourite_groups g ORDER BY g.display_order,g.id`)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	type group struct {
		ID                int64
		Name, Description string
		Enabled           bool
		Order             int
		Members, Calls    int64
	}
	groups := []group{}
	for rows.Next() {
		var g group
		if rows.Scan(&g.ID, &g.Name, &g.Description, &g.Enabled, &g.Order, &g.Members, &g.Calls) == nil {
			groups = append(groups, g)
		}
	}
	selected, _ := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	var members []map[string]any
	if selected > 0 {
		mr, e := s.db.Query(r.Context(), `SELECT m.system_id,m.talkgroup_id,coalesce(m.display_alias,''),coalesce(a.alias,''),m.group_id
			FROM favourite_members m LEFT JOIN talkgroup_aliases a ON a.system_id=m.system_id AND a.talkgroup_id=m.talkgroup_id AND a.enabled
			WHERE m.group_id=$1 AND ($2='' OR m.system_id ILIKE '%'||$2||'%' OR m.talkgroup_id ILIKE '%'||$2||'%' OR coalesce(m.display_alias,a.alias,'') ILIKE '%'||$2||'%') ORDER BY m.system_id,m.talkgroup_id`, selected, query)
		if e == nil {
			defer mr.Close()
			for mr.Next() {
				var system, tg, display, alias string
				var gid int64
				if mr.Scan(&system, &tg, &display, &alias, &gid) == nil {
					members = append(members, map[string]any{"System": system, "Talkgroup": tg, "Display": display, "Alias": alias, "Group": gid})
				}
			}
		}
	}
	suggestions := []map[string]string{}
	if selected > 0 {
		rows, e := s.db.Query(r.Context(), `SELECT DISTINCT c.system_id,c.talkgroup_id,coalesce(a.alias,c.talkgroup_name,'') FROM calls c LEFT JOIN talkgroup_aliases a ON a.system_id=c.system_id AND a.talkgroup_id=c.talkgroup_id AND a.enabled WHERE c.system_id<>'' AND c.talkgroup_id<>'' ORDER BY c.system_id,c.talkgroup_id LIMIT 250`)
		if e == nil {
			defer rows.Close()
			for rows.Next() {
				var system, tg, alias string
				if rows.Scan(&system, &tg, &alias) == nil {
					suggestions = append(suggestions, map[string]string{"System": system, "Talkgroup": tg, "Alias": alias})
				}
			}
		}
	}
	s.page(w, r, "admin_favourites.html", "Favourite groups", "favourites", map[string]any{"Groups": groups, "Selected": selected, "Members": members, "Search": query, "Suggestions": suggestions})
}

func (s *server) adminDeleteFavourite(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if id < 1 {
		http.Error(w, "invalid group", http.StatusBadRequest)
		return
	}
	var count int
	if err := s.db.QueryRow(r.Context(), `SELECT count(*) FROM favourite_members WHERE group_id=$1`, id).Scan(&count); err != nil {
		s.internal(w, err)
		return
	}
	if count > 0 {
		http.Error(w, "remove all members before deleting a group", http.StatusConflict)
		return
	}
	if _, err := s.db.Exec(r.Context(), `DELETE FROM favourite_groups WHERE id=$1`, id); err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/favourites", http.StatusSeeOther)
}

func (s *server) adminDeleteFavouriteMember(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	gid, _ := strconv.ParseInt(r.FormValue("group_id"), 10, 64)
	if gid < 1 {
		http.Error(w, "invalid group", http.StatusBadRequest)
		return
	}
	_, err := s.db.Exec(r.Context(), `DELETE FROM favourite_members WHERE group_id=$1 AND system_id=$2 AND talkgroup_id=$3`, gid, strings.TrimSpace(r.FormValue("system_id")), strings.TrimSpace(r.FormValue("talkgroup_id")))
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/favourites?group="+strconv.FormatInt(gid, 10), http.StatusSeeOther)
}
func (s *server) adminSaveFavourite(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || len(name) > 120 {
		http.Error(w, "invalid name", 400)
		return
	}
	order, _ := strconv.Atoi(r.FormValue("display_order"))
	_, err := s.db.Exec(r.Context(), `INSERT INTO favourite_groups(name,description,enabled,display_order) VALUES($1,$2,$3,$4) ON CONFLICT(name) DO UPDATE SET description=EXCLUDED.description,enabled=EXCLUDED.enabled,display_order=EXCLUDED.display_order,updated_at=now()`, name, strings.TrimSpace(r.FormValue("description")), r.FormValue("enabled") == "on", order)
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/favourites", 303)
}
func (s *server) adminSaveFavouriteMember(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	gid, _ := strconv.ParseInt(r.FormValue("group_id"), 10, 64)
	system := strings.TrimSpace(r.FormValue("system_id"))
	tg := strings.TrimSpace(r.FormValue("talkgroup_id"))
	if gid < 1 || system == "" || tg == "" {
		http.Error(w, "group, system and talkgroup are required", 400)
		return
	}
	_, err := s.db.Exec(r.Context(), `INSERT INTO favourite_members(group_id,system_id,talkgroup_id,display_alias) VALUES($1,$2,$3,NULLIF($4,'')) ON CONFLICT(group_id,system_id,talkgroup_id) DO UPDATE SET display_alias=EXCLUDED.display_alias,updated_at=now()`, gid, system, tg, strings.TrimSpace(r.FormValue("display_alias")))
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/favourites", 303)
}

func (s *server) adminNotifications(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT id,name,destination_type,enabled,coalesce(secret_ref,'') FROM notification_destinations ORDER BY id`)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	type destination struct {
		ID                 int64
		Name, Type, Secret string
		Enabled            bool
	}
	items := []destination{}
	for rows.Next() {
		var d destination
		if rows.Scan(&d.ID, &d.Name, &d.Type, &d.Enabled, &d.Secret) == nil {
			items = append(items, d)
		}
	}
	rules, _ := s.db.Query(r.Context(), `SELECT id,name,enabled,destination_id,priority,coalesce(sender_filter,''),coalesce(system_filter,''),coalesce(site_filter,''),coalesce(talkgroup_filter,''),coalesce(radio_filter,''),coalesce(call_type_filter,''),coalesce(frequency_min::text,''),coalesce(frequency_max::text,''),coalesce(min_duration_ms::text,''),coalesce(max_duration_ms::text,''),patched_only,coalesce(keyword,''),favourite_group_id FROM notification_rules ORDER BY priority DESC,id`)
	type rule struct {
		ID                                                                                                              int64
		Name                                                                                                            string
		Enabled                                                                                                         bool
		Destination, Priority                                                                                           int64
		Sender, System, Site, Talkgroup, Radio, CallType, FrequencyMin, FrequencyMax, MinDuration, MaxDuration, Keyword string
		Patched                                                                                                         bool
		FavouriteGroup                                                                                                  *int64
	}
	ruleItems := []rule{}
	if rules != nil {
		defer rules.Close()
		for rules.Next() {
			var x rule
			if rules.Scan(&x.ID, &x.Name, &x.Enabled, &x.Destination, &x.Priority, &x.Sender, &x.System, &x.Site, &x.Talkgroup, &x.Radio, &x.CallType, &x.FrequencyMin, &x.FrequencyMax, &x.MinDuration, &x.MaxDuration, &x.Patched, &x.Keyword, &x.FavouriteGroup) == nil {
				ruleItems = append(ruleItems, x)
			}
		}
	}
	deliveries, _ := s.db.Query(r.Context(), `SELECT id,rule_id,destination_id,call_id,status,attempt_count,coalesce(last_attempt_at::text,''),coalesce(next_attempt_at::text,''),coalesce(successful_at::text,''),coalesce(error,'') FROM notification_deliveries ORDER BY id DESC LIMIT 100`)
	type delivery struct {
		ID, Rule, Destination                    int64
		Call, Status, Last, Next, Success, Error string
		Attempts                                 int
	}
	deliveryItems := []delivery{}
	if deliveries != nil {
		defer deliveries.Close()
		for deliveries.Next() {
			var x delivery
			if deliveries.Scan(&x.ID, &x.Rule, &x.Destination, &x.Call, &x.Status, &x.Attempts, &x.Last, &x.Next, &x.Success, &x.Error) == nil {
				deliveryItems = append(deliveryItems, x)
			}
		}
	}
	groups := []map[string]any{}
	if gr, e := s.db.Query(r.Context(), `SELECT id,name FROM favourite_groups WHERE enabled ORDER BY display_order,id`); e == nil {
		defer gr.Close()
		for gr.Next() {
			var id int64
			var name string
			if gr.Scan(&id, &name) == nil {
				groups = append(groups, map[string]any{"ID": id, "Name": name})
			}
		}
	}
	var notificationHeartbeat *time.Time
	_ = s.db.QueryRow(r.Context(), `SELECT heartbeat_at FROM notification_worker_heartbeat WHERE id=true`).Scan(&notificationHeartbeat)
	workerOnline := notificationHeartbeat != nil && time.Since(*notificationHeartbeat) < 2*time.Minute
	var pendingDeliveries, failedDeliveries, expiredDeliveries int64
	_ = s.db.QueryRow(r.Context(), `SELECT count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='failed'),count(*) FILTER(WHERE status='expired') FROM notification_deliveries`).Scan(&pendingDeliveries, &failedDeliveries, &expiredDeliveries)
	s.page(w, r, "admin_notifications.html", "Notifications", "notifications", map[string]any{
		"Destinations": items, "Rules": ruleItems, "Deliveries": deliveryItems, "FavouriteGroups": groups,
		"TestOK": r.URL.Query().Get("test") == "ok", "TestError": r.URL.Query().Get("error"),
		"WorkerOnline": workerOnline, "WorkerHeartbeat": notificationHeartbeat, "PendingDeliveries": pendingDeliveries, "FailedDeliveries": failedDeliveries, "ExpiredDeliveries": expiredDeliveries,
	})
}

func (s *server) adminDestinationAction(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	action := r.FormValue("action")
	if id < 1 {
		http.Error(w, "invalid destination", 400)
		return
	}
	var err error
	switch action {
	case "enable", "disable":
		_, err = s.db.Exec(r.Context(), `UPDATE notification_destinations SET enabled=$2,updated_at=now() WHERE id=$1`, id, action == "enable")
	case "delete":
		_, err = s.db.Exec(r.Context(), `DELETE FROM notification_destinations WHERE id=$1`, id)
	case "test":
		// Send a real test notification.
		var cfg []byte
		var secret *string
		var typ string
		if err := s.db.QueryRow(r.Context(), `SELECT destination_type,config,secret_ref FROM notification_destinations WHERE id=$1`, id).Scan(&typ, &cfg, &secret); err != nil {
			http.Redirect(w, r, "/admin/notifications?test=fail&error=destination+not+found", 303)
			return
		}
		var config map[string]any
		_ = json.Unmarshal(cfg, &config)
		body := fmt.Sprintf("Test notification from Call Recorder — %s", time.Now().UTC().Format(time.RFC3339))
		testErr := s.sendTestNotificationDirect(r.Context(), typ, config, secret, body)
		if testErr != nil {
			http.Redirect(w, r, "/admin/notifications?test=fail&error="+url.QueryEscape(testErr.Error()), 303)
			return
		}
		http.Redirect(w, r, "/admin/notifications?test=ok", 303)
		return
	default:
		http.Error(w, "invalid action", 400)
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/notifications", 303)
}

func (s *server) adminRuleAction(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	action := r.FormValue("action")
	if id < 1 {
		http.Error(w, "invalid rule", 400)
		return
	}
	var err error
	switch action {
	case "enable", "disable":
		_, err = s.db.Exec(r.Context(), `UPDATE notification_rules SET enabled=$2,updated_at=now() WHERE id=$1`, id, action == "enable")
	case "delete":
		_, err = s.db.Exec(r.Context(), `DELETE FROM notification_rules WHERE id=$1`, id)
	default:
		http.Error(w, "invalid action", 400)
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/notifications", 303)
}

func (s *server) adminRetryDelivery(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if id < 1 {
		http.Error(w, "invalid delivery", 400)
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE notification_deliveries SET status='pending',next_attempt_at=now(),error=NULL,updated_at=now() WHERE id=$1`, id); err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, safeAdminReturn(r.FormValue("return"), "/admin/notifications/history"), 303)
}

func safeAdminReturn(value, fallback string) string {
	if strings.HasPrefix(value, "/admin/") && !strings.Contains(value, "//") {
		return value
	}
	return fallback
}

func (s *server) adminNotificationHistory(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	q := r.URL.Query()
	status := strings.TrimSpace(q.Get("status"))
	failedOnly := q.Get("failed") == "1"
	date := strings.TrimSpace(q.Get("date"))
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	const perPage = 100
	where, args := []string{"true"}, []any{}
	if status != "" {
		args = append(args, status)
		where = append(where, fmt.Sprintf("status=$%d", len(args)))
	}
	if failedOnly {
		where = append(where, "status='failed'")
	}
	if date != "" {
		args = append(args, date)
		where = append(where, fmt.Sprintf("created_at::date=$%d::date", len(args)))
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRow(r.Context(), `SELECT count(*) FROM notification_deliveries WHERE `+clause, args...).Scan(&total); err != nil {
		s.internal(w, err)
		return
	}
	args = append(args, perPage, (page-1)*perPage)
	rows, err := s.db.Query(r.Context(), `SELECT id,rule_id,destination_id,call_id,status,attempt_count,coalesce(last_attempt_at::text,''),coalesce(next_attempt_at::text,''),coalesce(successful_at::text,''),coalesce(error,'') FROM notification_deliveries WHERE `+clause+fmt.Sprintf(` ORDER BY id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, rule, dest, attempt int64
		var call, status, last, next, success, e string
		if rows.Scan(&id, &rule, &dest, &call, &status, &attempt, &last, &next, &success, &e) == nil {
			items = append(items, map[string]any{"ID": id, "Rule": rule, "Destination": dest, "Call": call, "Status": status, "Attempts": attempt, "Last": last, "Next": next, "Success": success, "Error": e})
		}
	}
	pages := (total + perPage - 1) / perPage
	s.page(w, r, "admin_notification_history.html", "Notification history", "notifications", map[string]any{"Deliveries": items, "Total": total, "Page": page, "Pages": pages, "Status": status, "Date": date, "Failed": failedOnly, "ReturnURL": r.URL.RequestURI()})
}
func (s *server) adminSaveDestination(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	typ := strings.TrimSpace(r.FormValue("type"))
	if name == "" || len(name) > 120 {
		http.Error(w, "invalid name", 400)
		return
	}
	switch typ {
	case "smtp", "webhook", "discord", "telegram":
	default:
		http.Error(w, "invalid destination type", 400)
		return
	}
	cfg := map[string]string{"url": strings.TrimSpace(r.FormValue("url")), "host": strings.TrimSpace(r.FormValue("host")), "port": strings.TrimSpace(r.FormValue("port")), "from": strings.TrimSpace(r.FormValue("from")), "to": strings.TrimSpace(r.FormValue("to")), "username": strings.TrimSpace(r.FormValue("username")), "chat_id": strings.TrimSpace(r.FormValue("chat_id"))}
	b, _ := json.Marshal(cfg)
	_, err := s.db.Exec(r.Context(), `INSERT INTO notification_destinations(name,destination_type,enabled,config,secret_ref) VALUES($1,$2,false,$3,$4) ON CONFLICT(name) DO UPDATE SET destination_type=EXCLUDED.destination_type,config=EXCLUDED.config,secret_ref=EXCLUDED.secret_ref,updated_at=now()`, name, typ, b, strings.TrimSpace(r.FormValue("secret_ref")))
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/notifications", 303)
}

func (s *server) adminSaveNotificationRule(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	dest, _ := strconv.ParseInt(r.FormValue("destination_id"), 10, 64)
	priority, _ := strconv.Atoi(r.FormValue("priority"))
	if name == "" || dest < 1 {
		http.Error(w, "name and destination are required", 400)
		return
	}
	_, err := s.db.Exec(r.Context(), `INSERT INTO notification_rules(name,destination_id,enabled,priority,sender_filter,system_filter,site_filter,talkgroup_filter,radio_filter,call_type_filter,frequency_min,frequency_max,min_duration_ms,max_duration_ms,patched_only,keyword,favourite_group_id,template) VALUES($1,$2,false,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),NULLIF($10,'')::numeric,NULLIF($11,'')::numeric,NULLIF($12,'')::bigint,NULLIF($13,'')::bigint,$14,NULLIF($15,''),NULLIF($16,'')::bigint,NULLIF($17,'')) ON CONFLICT(name) DO UPDATE SET destination_id=EXCLUDED.destination_id,priority=EXCLUDED.priority,sender_filter=EXCLUDED.sender_filter,system_filter=EXCLUDED.system_filter,site_filter=EXCLUDED.site_filter,talkgroup_filter=EXCLUDED.talkgroup_filter,radio_filter=EXCLUDED.radio_filter,call_type_filter=EXCLUDED.call_type_filter,frequency_min=EXCLUDED.frequency_min,frequency_max=EXCLUDED.frequency_max,min_duration_ms=EXCLUDED.min_duration_ms,max_duration_ms=EXCLUDED.max_duration_ms,patched_only=EXCLUDED.patched_only,keyword=EXCLUDED.keyword,favourite_group_id=EXCLUDED.favourite_group_id,template=EXCLUDED.template,updated_at=now()`, name, dest, priority, r.FormValue("sender"), r.FormValue("system"), r.FormValue("site"), r.FormValue("talkgroup"), r.FormValue("radio"), r.FormValue("call_type"), r.FormValue("frequency_min"), r.FormValue("frequency_max"), r.FormValue("min_duration_ms"), r.FormValue("max_duration_ms"), r.FormValue("patched_only") == "on", r.FormValue("keyword"), r.FormValue("favourite_group_id"), r.FormValue("template"))
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/notifications", 303)
}

type transcriptionStatus struct {
	Enabled, Processing, SecretAvailable, EndpointAllowed, WorkerOnline      bool
	Provider, ProviderType, Endpoint, Model, Profile, Language, AllowedCIDRs string
	PhrasePrompt                                                             string
	MinDurationMS, MaxAudioDurationMS, MaxFileSize                           int64
	Temperature                                                              float64
	VADEnabled, PhrasePromptsEnabled                                         bool
	Timeout, Concurrency, RetryLimit                                         int
	Pending, Failed, Completed                                               int64
	Heartbeat                                                                *time.Time
	LastTestAt                                                               *time.Time
	LastTestOK                                                               *bool
	LastTestError                                                            string
}

func (s *server) loadTranscriptionStatus(ctx context.Context) (transcriptionStatus, error) {
	var st transcriptionStatus
	var heartbeat, lastTestAt *time.Time
	var lastTestOK *bool
	err := s.db.QueryRow(ctx, `SELECT enabled,processing_enabled,provider,provider_type,coalesce(endpoint_url,''),coalesce(model,''),coalesce(profile,''),coalesce(default_language,''),min_duration_ms,max_audio_duration_ms,max_file_size,temperature,vad_enabled,phrase_prompts_enabled,coalesce(phrase_prompt,''),request_timeout_seconds,concurrency,retry_limit,allowed_endpoint_cidrs,heartbeat_at,last_test_at,last_test_ok,coalesce(last_test_error,'') FROM transcription_config LEFT JOIN transcription_worker_heartbeat ON true WHERE transcription_config.id=true`).Scan(
		&st.Enabled, &st.Processing, &st.Provider, &st.ProviderType, &st.Endpoint, &st.Model, &st.Profile, &st.Language,
		&st.MinDurationMS, &st.MaxAudioDurationMS, &st.MaxFileSize, &st.Temperature, &st.VADEnabled, &st.PhrasePromptsEnabled, &st.PhrasePrompt,
		&st.Timeout, &st.Concurrency, &st.RetryLimit, &st.AllowedCIDRs,
		&heartbeat, &lastTestAt, &lastTestOK, &st.LastTestError)
	if err != nil {
		return st, err
	}
	st.Heartbeat = heartbeat
	st.LastTestAt = lastTestAt
	st.LastTestOK = lastTestOK
	if st.Heartbeat != nil && time.Since(*st.Heartbeat) < 2*time.Minute {
		st.WorkerOnline = true
	}
	_ = s.db.QueryRow(ctx, `SELECT count(*)>0 FROM application_secrets WHERE purpose='transcription_api_key'`).Scan(&st.SecretAvailable)
	_ = s.db.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='failed'),count(*) FILTER(WHERE status='completed') FROM transcription_jobs`).Scan(&st.Pending, &st.Failed, &st.Completed)
	if st.Endpoint != "" {
		if _, e := transcription.HTTPClient(st.Endpoint, st.AllowedCIDRs); e == nil {
			st.EndpointAllowed = true
		}
	}
	return st, nil
}

func (s *server) adminTranscription(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	ctx := r.Context()
	st, err := s.loadTranscriptionStatus(ctx)
	if err != nil {
		s.internal(w, err)
		return
	}
	q := r.URL.Query()
	status := strings.TrimSpace(q.Get("status"))
	providerFilter := strings.TrimSpace(q.Get("provider"))
	callFilter := strings.TrimSpace(q.Get("call"))
	date := strings.TrimSpace(q.Get("date"))
	failedOnly := q.Get("failed") == "1"
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	const perPage = 100
	where, args := []string{"true"}, []any{}
	for _, item := range []struct{ value, column string }{{status, "j.status"}, {providerFilter, "j.provider"}, {callFilter, "j.call_id"}} {
		if item.value != "" {
			args = append(args, item.value)
			where = append(where, fmt.Sprintf("%s=$%d", item.column, len(args)))
		}
	}
	if failedOnly {
		where = append(where, "j.status='failed'")
	}
	if date != "" {
		args = append(args, date)
		where = append(where, fmt.Sprintf("j.created_at::date=$%d::date", len(args)))
	}
	clause := strings.Join(where, " AND ")
	var total int
	_ = s.db.QueryRow(ctx, `SELECT count(*) FROM transcription_jobs j WHERE `+clause, args...).Scan(&total)
	args = append(args, perPage, (page-1)*perPage)
	rows, _ := s.db.Query(ctx, `SELECT j.id,j.call_id,j.status,j.provider,j.attempt_count,coalesce(j.error,''),coalesce(j.created_at::text,''),coalesce(j.completed_at::text,''),coalesce(t.id,0),coalesce(t.review_status,''),coalesce(NULLIF(t.edited_text,''),t.text,'') FROM transcription_jobs j LEFT JOIN LATERAL (SELECT * FROM transcripts tx WHERE tx.call_id=j.call_id ORDER BY tx.updated_at DESC LIMIT 1) t ON true WHERE `+clause+fmt.Sprintf(` ORDER BY j.id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	type job struct {
		ID                                                int64
		TranscriptID                                      int64
		Call, Status, Provider, Error, Created, Completed string
		ReviewStatus, Transcript                          string
		Attempts                                          int
	}
	jobs := []job{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var j job
			if rows.Scan(&j.ID, &j.Call, &j.Status, &j.Provider, &j.Attempts, &j.Error, &j.Created, &j.Completed, &j.TranscriptID, &j.ReviewStatus, &j.Transcript) == nil {
				jobs = append(jobs, j)
			}
		}
	}
	pages := (total + perPage - 1) / perPage

	// Talkgroups eligible for transcription
	tgRows, err := s.db.Query(ctx, `SELECT a.system_id,a.talkgroup_id,coalesce(a.alias,''),a.transcription_enabled,coalesce(a.transcription_language,''),count(c.id) FROM talkgroup_aliases a LEFT JOIN calls c ON c.system_id=a.system_id AND c.talkgroup_id=a.talkgroup_id WHERE ($1='' OR a.system_id ILIKE '%'||$1||'%' OR a.talkgroup_id ILIKE '%'||$1||'%' OR coalesce(a.alias,'') ILIKE '%'||$1||'%') GROUP BY a.system_id,a.talkgroup_id,a.alias,a.transcription_enabled,a.transcription_language ORDER BY a.system_id,a.talkgroup_id LIMIT 250`, strings.TrimSpace(q.Get("tgq")))
	type tgRow struct {
		System, ID, Alias, Language string
		Enabled                     bool
		Calls                       int64
	}
	talkgroups := []tgRow{}
	var enabledCount int64
	if err == nil {
		defer tgRows.Close()
		for tgRows.Next() {
			var t tgRow
			if tgRows.Scan(&t.System, &t.ID, &t.Alias, &t.Enabled, &t.Language, &t.Calls) == nil {
				talkgroups = append(talkgroups, t)
				if t.Enabled {
					enabledCount++
				}
			}
		}
	}

	saved := q.Get("saved") == "1"
	removed := q.Get("removed") == "1"
	tested := q.Get("tested") == "1"
	msg := strings.TrimSpace(q.Get("msg"))
	s.page(w, r, "admin_transcription.html", "Transcription", "transcription", map[string]any{
		"Enabled": st.Enabled, "Processing": st.Processing, "Provider": st.Provider, "ProviderType": st.ProviderType,
		"Endpoint": st.Endpoint, "Model": st.Model, "Profile": st.Profile, "Language": st.Language,
		"MinDurationSeconds": float64(st.MinDurationMS) / 1000, "MaxDurationMinutes": float64(st.MaxAudioDurationMS) / 60000,
		"MaxFileSizeMB": float64(st.MaxFileSize) / (1024 * 1024), "Temperature": st.Temperature,
		"VAD": st.VADEnabled, "PhrasePrompts": st.PhrasePromptsEnabled, "PhrasePrompt": st.PhrasePrompt,
		"Timeout": st.Timeout, "Concurrency": st.Concurrency, "RetryLimit": st.RetryLimit,
		"AllowedCIDRs": st.AllowedCIDRs, "SecretAvailable": st.SecretAvailable,
		"EndpointAllowed": st.EndpointAllowed, "WorkerOnline": st.WorkerOnline,
		"Heartbeat": st.Heartbeat, "Pending": st.Pending, "Failed": st.Failed, "Completed": st.Completed,
		"LastTestAt": st.LastTestAt, "LastTestOK": st.LastTestOK, "LastTestError": st.LastTestError,
		"Jobs": jobs, "Total": total, "Page": page, "Pages": pages,
		"StatusFilter": status, "ProviderFilter": providerFilter, "CallFilter": callFilter, "Date": date, "FailedOnly": failedOnly,
		"ReturnURL": r.URL.RequestURI(), "Saved": saved, "Removed": removed, "Tested": tested, "Msg": msg,
		"Talkgroups": talkgroups, "TalkgroupSearch": strings.TrimSpace(q.Get("tgq")), "EnabledTalkgroupCount": enabledCount,
	})
}

func (s *server) adminRetryTranscription(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if id < 1 {
		http.Error(w, "invalid job", 400)
		return
	}
	identity := strings.TrimSpace(r.Header.Get("Cf-Access-Authenticated-User-Email"))
	if identity == "" {
		identity = "admin-token"
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE transcription_jobs SET status='pending',next_attempt_at=now(),error=NULL,retry_identity=$2,retry_at=now(),updated_at=now() WHERE id=$1`, id, identity); err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, safeAdminReturn(r.FormValue("return"), "/admin/transcription"), 303)
}

func (s *server) adminSaveTranscriptionSecret(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	value := r.FormValue("api_key")
	if value == "" || len(value) > 4096 {
		http.Error(w, "API key is required and must be short enough", 400)
		return
	}
	ciphertext, nonce, err := encryptSecret(s.masterKey, []byte(value))
	if err != nil {
		s.internal(w, err)
		return
	}
	identity := strings.TrimSpace(r.Header.Get("Cf-Access-Authenticated-User-Email"))
	if identity == "" {
		identity = "admin-token"
	}
	_, err = s.db.Exec(r.Context(), `INSERT INTO application_secrets(purpose,display_name,ciphertext,nonce,encryption_version,updated_by) VALUES('transcription_api_key','Transcription provider API key',$1,$2,1,$3) ON CONFLICT(purpose) DO UPDATE SET ciphertext=EXCLUDED.ciphertext,nonce=EXCLUDED.nonce,encryption_version=1,updated_by=EXCLUDED.updated_by,updated_at=now()`, ciphertext, nonce, identity)
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/transcription?saved=1", 303)
}

func (s *server) adminRemoveTranscriptionSecret(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	_, err := s.db.Exec(r.Context(), `DELETE FROM application_secrets WHERE purpose='transcription_api_key'`)
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/transcription?removed=1", 303)
}

func (s *server) adminSaveTranscriptionConfig(w http.ResponseWriter, r *http.Request) {
	if !s.adminOnly(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	parseFloat := func(name string) float64 {
		v, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue(name)), 64)
		return v
	}
	maxSize := int64(parseFloat("max_file_size_mb") * 1024 * 1024)
	maxDur := int64(parseFloat("max_duration_minutes") * 60000)
	minDur := int64(parseFloat("min_duration_seconds") * 1000)
	if maxSize <= 0 {
		maxSize = 50 * 1024 * 1024
	}
	if maxDur <= 0 {
		maxDur = 15 * 60000
	}
	if minDur < 0 {
		minDur = 0
	}
	concurrency, _ := strconv.Atoi(r.FormValue("concurrency"))
	retries, _ := strconv.Atoi(r.FormValue("retry_limit"))
	timeout, _ := strconv.Atoi(r.FormValue("request_timeout_seconds"))
	temperature, _ := strconv.ParseFloat(r.FormValue("temperature"), 64)
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 8 {
		concurrency = 8
	}
	if retries < 0 {
		retries = 0
	}
	if timeout < 1 {
		timeout = 60
	}
	if temperature < 0 {
		temperature = 0
	}
	if temperature > 2 {
		temperature = 2
	}
	providerType := strings.TrimSpace(r.FormValue("provider_type"))
	if providerType != "faster-whisper" {
		providerType = "openai-compatible"
	}
	allowedCIDRs := strings.TrimSpace(r.FormValue("allowed_endpoint_cidrs"))
	if allowedCIDRs != "" {
		if err := transcription.ValidateAllowedCIDRs(allowedCIDRs); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	_, err := s.db.Exec(r.Context(), `UPDATE transcription_config SET enabled=$1,processing_enabled=$2,provider=$3,provider_type=$4,default_language=NULLIF($5,''),endpoint_url=NULLIF($6,''),model=NULLIF($7,''),profile=NULLIF($8,''),max_file_size=$9,max_audio_duration_ms=$10,min_duration_ms=$11,temperature=$12,vad_enabled=$13,phrase_prompts_enabled=$14,phrase_prompt=NULLIF($15,''),request_timeout_seconds=$16,concurrency=$17,retry_limit=$18,allowed_endpoint_cidrs=$19,settings_version=settings_version+1,updated_at=now() WHERE id=true`, r.FormValue("enabled") == "on", r.FormValue("processing_enabled") == "on", strings.TrimSpace(r.FormValue("provider")), providerType, strings.TrimSpace(r.FormValue("language")), strings.TrimSpace(r.FormValue("endpoint")), strings.TrimSpace(r.FormValue("model")), strings.TrimSpace(r.FormValue("profile")), maxSize, maxDur, minDur, temperature, r.FormValue("vad_enabled") == "on", r.FormValue("phrase_prompts_enabled") == "on", r.FormValue("phrase_prompt"), timeout, concurrency, retries, allowedCIDRs)
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/transcription", 303)
}

func (s *server) adminEditTranscript(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	text := strings.TrimSpace(r.FormValue("text"))
	if id < 1 || len(text) > 10000 {
		http.Error(w, "invalid transcript", 400)
		return
	}
	identity := s.requestIdentity(r)
	var callID string
	err := s.db.QueryRow(r.Context(), `UPDATE transcripts SET edited_text=NULLIF($2,''),edited_at=now(),edited_by=$3,review_status='unreviewed',reviewed_at=NULL,reviewed_by=NULL,updated_at=now() WHERE id=$1 RETURNING call_id`, id, text, identity).Scan(&callID)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.recordAudit(r.Context(), r, "transcript.edit", "transcript", strconv.FormatInt(id, 10), map[string]any{"call_id": callID})
	http.Redirect(w, r, "/admin/transcription", 303)
}

type transcriptReviewItem struct {
	ID                                                                   int64
	CallID, Generated, Edited, Received, Effective, Notes                string
	Status, Provider, Model, Profile, Language                           string
	SystemID, SystemName, TalkgroupID, TalkgroupName, RadioID, RadioName string
	StartTime                                                            time.Time
	DurationMS                                                           int64
}

func (s *server) adminTranscriptReview(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = "unreviewed"
	}
	valid := map[string]bool{"unreviewed": true, "reviewed": true, "rejected": true, "inaudible": true, "no_speech": true}
	if !valid[status] {
		http.Error(w, "invalid review status", http.StatusBadRequest)
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	var item transcriptReviewItem
	err := s.db.QueryRow(r.Context(), `SELECT t.id,t.call_id,coalesce(t.text,''),coalesce(t.edited_text,''),coalesce(c.transcript,''),coalesce(NULLIF(t.edited_text,''),NULLIF(t.text,''),NULLIF(c.transcript,''),''),coalesce(t.review_notes,''),t.review_status,coalesce(t.provider,''),coalesce(t.model,''),coalesce(t.profile,''),coalesce(t.language,''),c.system_id,coalesce(c.system_name,''),c.talkgroup_id,coalesce(c.talkgroup_name,''),coalesce(c.radio_id,''),coalesce(c.radio_name,''),c.start_time,c.duration_ms
		FROM transcripts t JOIN calls c ON c.id=t.call_id WHERE t.review_status=$1 AND t.id>$2 ORDER BY t.id LIMIT 1`, status, after).Scan(
		&item.ID, &item.CallID, &item.Generated, &item.Edited, &item.Received, &item.Effective, &item.Notes, &item.Status, &item.Provider, &item.Model, &item.Profile, &item.Language, &item.SystemID, &item.SystemName, &item.TalkgroupID, &item.TalkgroupName, &item.RadioID, &item.RadioName, &item.StartTime, &item.DurationMS)
	if errors.Is(err, pgx.ErrNoRows) && after > 0 {
		http.Redirect(w, r, "/admin/transcription/review?status="+url.QueryEscape(status), http.StatusSeeOther)
		return
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.internal(w, err)
		return
	}
	counts := map[string]int64{}
	rows, countErr := s.db.Query(r.Context(), `SELECT review_status,count(*) FROM transcripts GROUP BY review_status`)
	if countErr == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var count int64
			if rows.Scan(&name, &count) == nil {
				counts[name] = count
			}
		}
	}
	s.page(w, r, "admin_transcription_review.html", "Transcript review", "transcription", map[string]any{"Item": item, "HasItem": err == nil, "Status": status, "Counts": counts})
}

func (s *server) adminReviewTranscript(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	status := strings.TrimSpace(r.FormValue("status"))
	if id < 1 || (status != "reviewed" && status != "rejected" && status != "inaudible" && status != "no_speech" && status != "unreviewed") {
		http.Error(w, "invalid transcript review", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(r.FormValue("text"))
	notes := strings.TrimSpace(r.FormValue("review_notes"))
	if len(text) > 10000 || len(notes) > 2000 {
		http.Error(w, "transcript or review notes are too long", http.StatusBadRequest)
		return
	}
	if status == "reviewed" && r.Form.Has("text") && text == "" {
		http.Error(w, "reviewed transcripts require text", http.StatusBadRequest)
		return
	}
	saveText := r.Form.Has("text") && (status == "reviewed" || status == "rejected")
	var callID string
	err := s.db.QueryRow(r.Context(), `UPDATE transcripts SET
		edited_text=CASE WHEN $4 THEN NULLIF(NULLIF($5,''),coalesce(NULLIF(text,''),(SELECT NULLIF(c.transcript,'') FROM calls c WHERE c.id=transcripts.call_id))) ELSE edited_text END,
		edited_at=CASE WHEN $4 AND NULLIF(NULLIF($5,''),coalesce(NULLIF(text,''),(SELECT NULLIF(c.transcript,'') FROM calls c WHERE c.id=transcripts.call_id))) IS DISTINCT FROM edited_text THEN now() ELSE edited_at END,
		edited_by=CASE WHEN $4 AND NULLIF(NULLIF($5,''),coalesce(NULLIF(text,''),(SELECT NULLIF(c.transcript,'') FROM calls c WHERE c.id=transcripts.call_id))) IS DISTINCT FROM edited_text THEN $3 ELSE edited_by END,
		review_notes=CASE WHEN $4 THEN NULLIF($6,'') ELSE review_notes END,
		review_status=$2,reviewed_at=CASE WHEN $2='unreviewed' THEN NULL ELSE now() END,reviewed_by=CASE WHEN $2='unreviewed' THEN NULL ELSE $3 END,updated_at=now()
		WHERE id=$1 RETURNING call_id`, id, status, s.requestIdentity(r), saveText, text, notes).Scan(&callID)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "transcript not found", http.StatusNotFound)
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.recordAudit(r.Context(), r, "transcript.review", "transcript", strconv.FormatInt(id, 10), map[string]any{"call_id": callID, "status": status})
	if r.FormValue("review_ui") == "1" {
		http.Redirect(w, r, "/admin/transcription/review?status=unreviewed", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, safeAdminReturn(r.FormValue("return"), "/admin/transcription"), http.StatusSeeOther)
}

type transcriptionEligibilityResult struct {
	Eligible bool
	Reason   string
}

func (s *server) transcriptionEligibility(ctx context.Context, callID string) (transcriptionEligibilityResult, error) {
	var cfg struct {
		Enabled, Processing bool
		Provider            string
		MaxFileSize         int64
		MinDurationMS       int64
		MaxAudioDurationMS  int64
	}
	err := s.db.QueryRow(ctx, `SELECT enabled,processing_enabled,provider,max_file_size,min_duration_ms,max_audio_duration_ms FROM transcription_config WHERE id=true`).Scan(&cfg.Enabled, &cfg.Processing, &cfg.Provider, &cfg.MaxFileSize, &cfg.MinDurationMS, &cfg.MaxAudioDurationMS)
	if err != nil {
		return transcriptionEligibilityResult{}, err
	}
	if !cfg.Enabled {
		return transcriptionEligibilityResult{Reason: "Provider is disabled."}, nil
	}
	if !cfg.Processing {
		return transcriptionEligibilityResult{Reason: "Processing is disabled."}, nil
	}
	var secretAvailable bool
	_ = s.db.QueryRow(ctx, `SELECT count(*)>0 FROM application_secrets WHERE purpose='transcription_api_key'`).Scan(&secretAvailable)
	if !secretAvailable {
		return transcriptionEligibilityResult{Reason: "API key is not configured."}, nil
	}
	var c struct {
		System, Talkgroup, AudioPath, AudioFormat string
		DurationMS, AudioSize                     int64
	}
	err = s.db.QueryRow(ctx, `SELECT system_id,talkgroup_id,audio_path,audio_format,duration_ms,audio_size FROM calls WHERE id=$1`, callID).Scan(&c.System, &c.Talkgroup, &c.AudioPath, &c.AudioFormat, &c.DurationMS, &c.AudioSize)
	if err != nil {
		return transcriptionEligibilityResult{}, err
	}
	if c.AudioPath == "" {
		return transcriptionEligibilityResult{Reason: "Call has no audio file."}, nil
	}
	if c.AudioFormat != "mp3" && c.AudioFormat != "wav" {
		return transcriptionEligibilityResult{Reason: fmt.Sprintf("Audio format %q is not supported.", c.AudioFormat)}, nil
	}
	if cfg.MinDurationMS > 0 && c.DurationMS < cfg.MinDurationMS {
		return transcriptionEligibilityResult{Reason: fmt.Sprintf("Call duration %.2fs is below the minimum %.2fs.", float64(c.DurationMS)/1000, float64(cfg.MinDurationMS)/1000)}, nil
	}
	if cfg.MaxAudioDurationMS > 0 && c.DurationMS > cfg.MaxAudioDurationMS {
		return transcriptionEligibilityResult{Reason: fmt.Sprintf("Call duration %.2fs exceeds the maximum %.2fs.", float64(c.DurationMS)/1000, float64(cfg.MaxAudioDurationMS)/1000)}, nil
	}
	if cfg.MaxFileSize > 0 && c.AudioSize > cfg.MaxFileSize {
		return transcriptionEligibilityResult{Reason: fmt.Sprintf("Audio file size exceeds the maximum %d MB.", cfg.MaxFileSize/(1024*1024))}, nil
	}
	var tgEnabled bool
	err = s.db.QueryRow(ctx, `SELECT transcription_mode IN ('inherit','enabled') FROM talkgroup_aliases WHERE system_id=$1 AND talkgroup_id=$2`, c.System, c.Talkgroup).Scan(&tgEnabled)
	if err != nil || !tgEnabled {
		return transcriptionEligibilityResult{Reason: "Talkgroup is not enabled for transcription."}, nil
	}
	var active int64
	_ = s.db.QueryRow(ctx, `SELECT count(*) FROM transcription_jobs WHERE call_id=$1 AND status IN ('pending','running')`, callID).Scan(&active)
	if active > 0 {
		return transcriptionEligibilityResult{Reason: "A transcription job is already active for this call."}, nil
	}
	return transcriptionEligibilityResult{Eligible: true}, nil
}

func (s *server) adminQueueTranscription(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/transcription/queue/")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "invalid call ID", 400)
		return
	}
	elig, err := s.transcriptionEligibility(r.Context(), id)
	if err != nil {
		s.internal(w, err)
		return
	}
	if !elig.Eligible {
		http.Error(w, elig.Reason, http.StatusConflict)
		return
	}
	var provider string
	if err := s.db.QueryRow(r.Context(), `SELECT provider FROM transcription_config WHERE id=true`).Scan(&provider); err != nil {
		s.internal(w, err)
		return
	}
	if _, err := s.db.Exec(r.Context(), `INSERT INTO transcription_jobs(call_id,provider) VALUES($1,$2) ON CONFLICT(call_id,provider) DO UPDATE SET status='pending',next_attempt_at=now(),error=NULL`, id, provider); err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/call/"+id, 303)
}

func (s *server) loadTranscriptionAPIKey(ctx context.Context) (string, error) {
	var encryptedKey, keyNonce []byte
	_ = s.db.QueryRow(ctx, `SELECT ciphertext,nonce FROM application_secrets WHERE purpose='transcription_api_key'`).Scan(&encryptedKey, &keyNonce)
	if len(encryptedKey) == 0 {
		return "", nil
	}
	plain, err := decryptSecret(s.masterKey, encryptedKey, keyNonce)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *server) adminTestTranscription(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	var cfg struct {
		Enabled      bool
		Provider     string
		ProviderType string
		Endpoint     string
		Model        string
		Language     *string
		Timeout      int
		VADEnabled   bool
	}
	err := s.db.QueryRow(ctx, `SELECT enabled,provider,provider_type,endpoint_url,model,default_language,request_timeout_seconds,vad_enabled FROM transcription_config WHERE id=true`).Scan(&cfg.Enabled, &cfg.Provider, &cfg.ProviderType, &cfg.Endpoint, &cfg.Model, &cfg.Language, &cfg.Timeout, &cfg.VADEnabled)
	if err != nil {
		s.internal(w, err)
		return
	}
	if cfg.Endpoint == "" || cfg.Model == "" {
		http.Error(w, "Endpoint and model are required before testing.", http.StatusBadRequest)
		return
	}
	identity := strings.TrimSpace(r.Header.Get("Cf-Access-Authenticated-User-Email"))
	if identity == "" {
		identity = "admin-token"
	}
	var lastTest *time.Time
	_ = s.db.QueryRow(ctx, `SELECT last_test_at FROM transcription_config WHERE id=true`).Scan(&lastTest)
	if lastTest != nil && time.Since(*lastTest) < 30*time.Second {
		http.Error(w, "Provider test is rate-limited. Please wait 30 seconds.", http.StatusTooManyRequests)
		return
	}

	key, err := s.loadTranscriptionAPIKey(ctx)
	if err != nil {
		_, _ = s.db.Exec(ctx, `UPDATE transcription_config SET last_test_at=now(),last_test_ok=false,last_test_error=$1 WHERE id=true`, "Failed to load encrypted API key")
		s.internal(w, err)
		return
	}

	st, err := s.loadTranscriptionStatus(ctx)
	if err != nil {
		s.internal(w, err)
		return
	}
	if !st.EndpointAllowed {
		_, _ = s.db.Exec(ctx, `UPDATE transcription_config SET last_test_at=now(),last_test_ok=false,last_test_error=$1 WHERE id=true`, "Endpoint is not allowed by the configured CIDR allowlist")
		http.Error(w, "Endpoint is not allowed by the configured CIDR allowlist", http.StatusBadRequest)
		return
	}

	client, err := transcription.HTTPClient(cfg.Endpoint, st.AllowedCIDRs)
	if err != nil {
		_, _ = s.db.Exec(ctx, `UPDATE transcription_config SET last_test_at=now(),last_test_ok=false,last_test_error=$1 WHERE id=true`, err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if cfg.Timeout < 1 {
		cfg.Timeout = 60
	}
	client.Timeout = time.Duration(cfg.Timeout) * time.Second

	wav, err := transcription.SyntheticWAV()
	if err != nil {
		s.internal(w, err)
		return
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "synthetic.wav")
	part.Write(wav)
	_ = mw.WriteField("model", cfg.Model)
	if cfg.Language != nil && *cfg.Language != "" {
		_ = mw.WriteField("language", *cfg.Language)
	}
	if cfg.ProviderType == "faster-whisper" && cfg.VADEnabled {
		_ = mw.WriteField("vad_filter", "true")
	}
	_ = mw.Close()

	req, err := http.NewRequest(http.MethodPost, cfg.Endpoint, &body)
	if err != nil {
		s.internal(w, err)
		return
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	result := "ok"
	ok := false
	var errText string
	if err != nil {
		errText = err.Error()
		result = errText
	} else {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errText = fmt.Sprintf("provider returned HTTP %d", resp.StatusCode)
			result = errText
		} else {
			var out struct{ Text string }
			if json.Unmarshal(raw, &out) != nil {
				errText = "provider response is not valid JSON with a text field"
				result = errText
			} else {
				ok = true
			}
		}
	}
	_, _ = s.db.Exec(ctx, `UPDATE transcription_config SET last_test_at=now(),last_test_ok=$1,last_test_error=NULLIF($2,'') WHERE id=true`, ok, errText)
	s.phase8JSON(w, map[string]any{"ok": ok, "result": result, "vad_sent": cfg.ProviderType == "faster-whisper" && cfg.VADEnabled})
}

// parseTalkgroupValues extracts system/talkgroup pairs from a form value list.
// Each entry must be "SYSTEM:TALKGROUP_ID". Invalid entries are silently skipped.
func parseTalkgroupValues(values []string) (systems, talkgroups []string) {
	for _, v := range values {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		systems = append(systems, parts[0])
		talkgroups = append(talkgroups, parts[1])
	}
	return
}

// applyTranscriptionToggle updates transcription_enabled and optionally the
// language override for the given talkgroups inside a single transaction.
// When enable is true and language is non-empty, both fields are set.
// When enable is true and language is empty, the existing language is preserved.
// When enable is false, only transcription_enabled is cleared; the language is
// always preserved.
// Returns the number of talkgroups whose transcription_enabled changed.
func (s *server) applyTranscriptionToggle(ctx context.Context, systems, talkgroups []string, enable bool, language string) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var changed int64
	for i := range systems {
		var tag pgconn.CommandTag
		var err error
		if enable && language != "" {
			tag, err = tx.Exec(ctx, `UPDATE talkgroup_aliases SET transcription_enabled=$1,transcription_mode=$2,transcription_language=NULLIF($3,''),updated_at=now() WHERE system_id=$4 AND talkgroup_id=$5 AND (transcription_enabled IS DISTINCT FROM $1 OR transcription_mode IS DISTINCT FROM $2)`, enable, map[bool]string{true: "enabled", false: "disabled"}[enable], language, systems[i], talkgroups[i])
		} else if enable {
			tag, err = tx.Exec(ctx, `UPDATE talkgroup_aliases SET transcription_enabled=true,transcription_mode='enabled',updated_at=now() WHERE system_id=$1 AND talkgroup_id=$2 AND (NOT transcription_enabled OR transcription_mode<>'enabled')`, systems[i], talkgroups[i])
		} else {
			tag, err = tx.Exec(ctx, `UPDATE talkgroup_aliases SET transcription_enabled=false,transcription_mode='disabled',updated_at=now() WHERE system_id=$1 AND talkgroup_id=$2 AND (transcription_enabled OR transcription_mode<>'disabled')`, systems[i], talkgroups[i])
		}
		if err != nil {
			return 0, err
		}
		changed += tag.RowsAffected()
	}
	return changed, tx.Commit(ctx)
}

// adminUpdateTalkgroupTranscription handles bulk enable/disable from the unified
// talkgroup form on the transcription administration page.
func (s *server) adminUpdateTalkgroupTranscription(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	action := strings.TrimSpace(r.FormValue("action"))
	if action != "enable" && action != "disable" {
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}
	systems, talkgroups := parseTalkgroupValues(r.Form["talkgroup"])
	if len(systems) == 0 {
		http.Error(w, "no talkgroups selected", http.StatusBadRequest)
		return
	}
	language := strings.TrimSpace(r.FormValue("language"))
	enable := action == "enable"
	changed, err := s.applyTranscriptionToggle(r.Context(), systems, talkgroups, enable, language)
	if err != nil {
		s.internal(w, err)
		return
	}
	msg := fmt.Sprintf("%d talkgroup%s %s for transcription.", changed, pluralSuffix(changed), action+"d")
	tgq := strings.TrimSpace(r.FormValue("tgq"))
	redirect := "/admin/transcription?saved=1&msg=" + url.QueryEscape(msg)
	if tgq != "" {
		redirect += "&tgq=" + url.QueryEscape(tgq)
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// adminToggleSingleTalkgroupTranscription handles per-row enable/disable
// actions from the inline button on each talkgroup row.
func (s *server) adminToggleSingleTalkgroupTranscription(w http.ResponseWriter, r *http.Request) {
	if !s.editorAuthorized(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	action := strings.TrimSpace(r.FormValue("action"))
	if action != "enable" && action != "disable" {
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}
	v := strings.TrimSpace(r.FormValue("talkgroup"))
	systems, talkgroups := parseTalkgroupValues([]string{v})
	if len(systems) == 0 {
		http.Error(w, "invalid talkgroup", http.StatusBadRequest)
		return
	}
	enable := action == "enable"
	changed, err := s.applyTranscriptionToggle(r.Context(), systems, talkgroups, enable, "")
	if err != nil {
		s.internal(w, err)
		return
	}
	msg := fmt.Sprintf("%d talkgroup %s for transcription.", changed, action+"d")
	tgq := strings.TrimSpace(r.FormValue("tgq"))
	redirect := "/admin/transcription?saved=1&msg=" + url.QueryEscape(msg)
	if tgq != "" {
		redirect += "&tgq=" + url.QueryEscape(tgq)
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// apiTranscripts returns transcript status and text for a set of call IDs.
// Used by the call list to poll for live transcript updates without refreshing
// the entire page. Accepts ?id=ID1&id=ID2 (up to 100).
func (s *server) apiTranscripts(w http.ResponseWriter, r *http.Request) {
	ids := r.URL.Query()["id"]
	if len(ids) == 0 {
		s.phase8JSON(w, map[string]any{})
		return
	}
	if len(ids) > 100 {
		ids = ids[:100]
	}
	type transcriptInfo struct {
		ID            string `json:"id"`
		Status        string `json:"status"`
		GeneratedText string `json:"generated"`
		Error         string `json:"error,omitempty"`
		GeneratedFlag bool   `json:"generated_flag"`
	}
	result := map[string]transcriptInfo{}
	// Query transcription job status and generated transcript for each call.
	rows, err := s.db.Query(r.Context(), `
		SELECT c.id,
			coalesce((SELECT tj.status FROM transcription_jobs tj WHERE tj.call_id=c.id ORDER BY tj.updated_at DESC LIMIT 1),''),
			coalesce((SELECT coalesce(NULLIF(t.edited_text,''),t.text) FROM transcripts t WHERE t.call_id=c.id ORDER BY t.updated_at DESC LIMIT 1),''),
			coalesce((SELECT tj.error FROM transcription_jobs tj WHERE tj.call_id=c.id AND tj.status='failed' ORDER BY tj.updated_at DESC LIMIT 1),'')
		FROM calls c WHERE c.id = ANY($1)`,
		ids)
	if err != nil {
		s.phase8JSON(w, map[string]any{})
		return
	}
	defer rows.Close()
	for rows.Next() {
		var info transcriptInfo
		if err := rows.Scan(&info.ID, &info.Status, &info.GeneratedText, &info.Error); err != nil {
			continue
		}
		info.GeneratedFlag = info.GeneratedText != ""
		result[info.ID] = info
	}
	s.phase8JSON(w, result)
}

// sendTestNotificationDirect sends a test notification directly from the web
// server without going through the delivery queue. Reuses the same sending
// logic as the admin CLI.
func (s *server) sendTestNotificationDirect(ctx context.Context, typ string, config map[string]any, secret *string, body string) error {
	if typ == "smtp" {
		host, _ := config["host"].(string)
		port, _ := config["port"].(string)
		from, _ := config["from"].(string)
		to, _ := config["to"].(string)
		user, _ := config["username"].(string)
		useTLS, _ := config["tls"].(bool)
		if host == "" || port == "" || from == "" || to == "" {
			return fmt.Errorf("SMTP host, port, from, and to are required")
		}
		password := ""
		if secret != nil && *secret != "" {
			password = os.Getenv(*secret)
		}
		dialer := &net.Dialer{Timeout: 15 * time.Second}
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
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
				return fmt.Errorf("SMTP server does not support STARTTLS")
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
		headers := "From: " + from + "\r\nTo: " + to + "\r\nSubject: Call Recorder test notification\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n"
		if _, err = io.WriteString(writer, headers+"<p>"+html.EscapeString(body)+"</p>"); err != nil {
			_ = writer.Close()
			return err
		}
		return writer.Close()
	}
	endpoint, _ := config["url"].(string)
	if endpoint == "" {
		return fmt.Errorf("destination URL missing")
	}
	if !strings.HasPrefix(endpoint, "https://") && !strings.HasPrefix(endpoint, "http://") {
		return fmt.Errorf("destination URL must use HTTP(S)")
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
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("destination returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (s *server) phase8JSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

var _ = fmt.Sprintf
var _ = os.Getenv
var _ = time.Now
