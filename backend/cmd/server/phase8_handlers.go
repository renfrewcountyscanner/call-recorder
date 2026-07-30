package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

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
		  AND (r.keyword IS NULL OR c.search_document::text ILIKE '%'||lower(r.keyword)||'%')
		  AND (r.favourite_group_id IS NULL OR EXISTS (SELECT 1 FROM favourite_members fm WHERE fm.group_id=r.favourite_group_id AND fm.system_id=c.system_id AND fm.talkgroup_id=c.talkgroup_id))
		ON CONFLICT(rule_id,call_id) DO NOTHING`, callID)
}

func (s *server) adminProtectCall(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
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
	if !s.adminAuthorized(w, r) {
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
	if !s.adminAuthorized(w, r) {
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
	if !s.adminAuthorized(w, r) {
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
	if !s.adminAuthorized(w, r) {
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
	if !s.adminAuthorized(w, r) {
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
	if !s.adminAuthorized(w, r) {
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
	s.page(w, r, "admin_notifications.html", "Notifications", "notifications", map[string]any{"Destinations": items, "Rules": ruleItems, "Deliveries": deliveryItems, "FavouriteGroups": groups})
}

func (s *server) adminDestinationAction(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
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
		http.Redirect(w, r, "/admin/notifications?test=queued", 303)
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
	if !s.adminAuthorized(w, r) {
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
	if !s.adminAuthorized(w, r) {
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
	if !s.adminAuthorized(w, r) {
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
	if !s.adminAuthorized(w, r) {
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
	if !s.adminAuthorized(w, r) {
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

func (s *server) adminTranscription(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	var enabled bool
	var provider, endpoint, model, secret string
	var pending, failed, done int64
	_ = s.db.QueryRow(r.Context(), `SELECT enabled,provider,coalesce(endpoint_url,''),coalesce(model,''),coalesce(secret_ref,'') FROM transcription_config WHERE id=true`).Scan(&enabled, &provider, &endpoint, &model, &secret)
	_ = s.db.QueryRow(r.Context(), `SELECT count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='failed'),count(*) FILTER(WHERE status='completed') FROM transcription_jobs`).Scan(&pending, &failed, &done)
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
	_ = s.db.QueryRow(r.Context(), `SELECT count(*) FROM transcription_jobs j WHERE `+clause, args...).Scan(&total)
	args = append(args, perPage, (page-1)*perPage)
	rows, _ := s.db.Query(r.Context(), `SELECT j.id,j.call_id,j.status,j.provider,j.attempt_count,coalesce(j.error,''),coalesce(j.created_at::text,''),coalesce(j.completed_at::text,'') FROM transcription_jobs j WHERE `+clause+fmt.Sprintf(` ORDER BY j.id DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	type job struct {
		ID                                                int64
		Call, Status, Provider, Error, Created, Completed string
		Attempts                                          int
	}
	jobs := []job{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var j job
			if rows.Scan(&j.ID, &j.Call, &j.Status, &j.Provider, &j.Attempts, &j.Error, &j.Created, &j.Completed) == nil {
				jobs = append(jobs, j)
			}
		}
	}
	pages := (total + perPage - 1) / perPage
	s.page(w, r, "admin_transcription.html", "Transcription", "transcription", map[string]any{"Enabled": enabled, "Provider": provider, "Endpoint": endpoint, "Model": model, "Secret": secret, "Pending": pending, "Failed": failed, "Completed": done, "Jobs": jobs, "Total": total, "Page": page, "Pages": pages, "StatusFilter": status, "ProviderFilter": providerFilter, "CallFilter": callFilter, "Date": date, "FailedOnly": failedOnly, "ReturnURL": r.URL.RequestURI()})
}

func (s *server) adminRetryTranscription(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if id < 1 {
		http.Error(w, "invalid job", 400)
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE transcription_jobs SET status='pending',next_attempt_at=now(),error=NULL,updated_at=now() WHERE id=$1`, id); err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, safeAdminReturn(r.FormValue("return"), "/admin/transcription"), 303)
}

func (s *server) adminSaveTranscriptionConfig(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	maxSize, _ := strconv.ParseInt(r.FormValue("max_file_size"), 10, 64)
	maxDur, _ := strconv.ParseInt(r.FormValue("max_duration_ms"), 10, 64)
	concurrency, _ := strconv.Atoi(r.FormValue("concurrency"))
	retries, _ := strconv.Atoi(r.FormValue("retry_limit"))
	if concurrency < 1 {
		concurrency = 1
	}
	if retries < 0 {
		retries = 0
	}
	_, err := s.db.Exec(r.Context(), `UPDATE transcription_config SET enabled=$1,provider=$2,default_language=NULLIF($3,''),endpoint_url=NULLIF($4,''),model=NULLIF($5,''),secret_ref=NULLIF($6,''),max_file_size=$7,max_audio_duration_ms=$8,concurrency=$9,retry_limit=$10,updated_at=now() WHERE id=true`, r.FormValue("enabled") == "on", strings.TrimSpace(r.FormValue("provider")), strings.TrimSpace(r.FormValue("language")), strings.TrimSpace(r.FormValue("endpoint")), strings.TrimSpace(r.FormValue("model")), strings.TrimSpace(r.FormValue("secret_ref")), maxSize, maxDur, concurrency, retries)
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/transcription", 303)
}

func (s *server) adminEditTranscript(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	text := strings.TrimSpace(r.FormValue("text"))
	if id < 1 || len(text) > 10000 {
		http.Error(w, "invalid transcript", 400)
		return
	}
	identity := r.Header.Get("Cf-Access-Authenticated-User-Email")
	if identity == "" {
		identity = "admin-token"
	}
	_, err := s.db.Exec(r.Context(), `UPDATE transcripts SET edited_text=$2,edited_at=now(),edited_by=$3,updated_at=now() WHERE id=$1`, id, text, identity)
	if err != nil {
		s.internal(w, err)
		return
	}
	http.Redirect(w, r, "/admin/transcription", 303)
}
func (s *server) adminQueueTranscription(w http.ResponseWriter, r *http.Request) {
	if !s.adminAuthorized(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/transcription/queue/")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "invalid call ID", 400)
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

func (s *server) phase8JSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

var _ = fmt.Sprintf
