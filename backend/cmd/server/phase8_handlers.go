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
	_, _ = s.db.Exec(ctx, `INSERT INTO notification_deliveries(rule_id,destination_id,call_id) SELECT r.id,r.destination_id,$1 FROM notification_rules r JOIN notification_destinations d ON d.id=r.destination_id WHERE r.enabled AND d.enabled AND (r.sender_filter IS NULL OR r.sender_filter=(SELECT sender_id FROM calls WHERE id=$1)) AND (r.system_filter IS NULL OR r.system_filter=(SELECT system_id FROM calls WHERE id=$1)) AND (r.site_filter IS NULL OR r.site_filter=(SELECT site_id FROM calls WHERE id=$1)) AND (r.talkgroup_filter IS NULL OR r.talkgroup_filter=(SELECT talkgroup_id FROM calls WHERE id=$1)) AND (r.radio_filter IS NULL OR r.radio_filter=(SELECT radio_id FROM calls WHERE id=$1)) ON CONFLICT(rule_id,call_id) DO NOTHING`, callID)
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
	rows, err := s.db.Query(r.Context(), `SELECT id,name,coalesce(description,''),enabled,display_order FROM favourite_groups ORDER BY display_order,id`)
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
	}
	groups := []group{}
	for rows.Next() {
		var g group
		if rows.Scan(&g.ID, &g.Name, &g.Description, &g.Enabled, &g.Order) == nil {
			groups = append(groups, g)
		}
	}
	s.page(w, r, "admin_favourites.html", "Favourite groups", "favourites", map[string]any{"Groups": groups})
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
	rules, _ := s.db.Query(r.Context(), `SELECT id,name,enabled,destination_id,priority FROM notification_rules ORDER BY priority DESC,id`)
	type rule struct {
		ID                    int64
		Name                  string
		Enabled               bool
		Destination, Priority int64
	}
	ruleItems := []rule{}
	if rules != nil {
		defer rules.Close()
		for rules.Next() {
			var x rule
			if rules.Scan(&x.ID, &x.Name, &x.Enabled, &x.Destination, &x.Priority) == nil {
				ruleItems = append(ruleItems, x)
			}
		}
	}
	deliveries, _ := s.db.Query(r.Context(), `SELECT id,rule_id,call_id,status,attempt_count,coalesce(error,'') FROM notification_deliveries ORDER BY id DESC LIMIT 50`)
	type delivery struct {
		ID, Rule            int64
		Call, Status, Error string
		Attempts            int
	}
	deliveryItems := []delivery{}
	if deliveries != nil {
		defer deliveries.Close()
		for deliveries.Next() {
			var x delivery
			if deliveries.Scan(&x.ID, &x.Rule, &x.Call, &x.Status, &x.Attempts, &x.Error) == nil {
				deliveryItems = append(deliveryItems, x)
			}
		}
	}
	s.page(w, r, "admin_notifications.html", "Notifications", "notifications", map[string]any{"Destinations": items, "Rules": ruleItems, "Deliveries": deliveryItems})
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
	cfg := map[string]string{"url": strings.TrimSpace(r.FormValue("url"))}
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
	_, err := s.db.Exec(r.Context(), `INSERT INTO notification_rules(name,destination_id,enabled,priority,sender_filter,system_filter,site_filter,talkgroup_filter,radio_filter,call_type_filter,keyword,template) VALUES($1,$2,false,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),NULLIF($11,'')) ON CONFLICT(name) DO UPDATE SET destination_id=EXCLUDED.destination_id,priority=EXCLUDED.priority,sender_filter=EXCLUDED.sender_filter,system_filter=EXCLUDED.system_filter,site_filter=EXCLUDED.site_filter,talkgroup_filter=EXCLUDED.talkgroup_filter,radio_filter=EXCLUDED.radio_filter,call_type_filter=EXCLUDED.call_type_filter,keyword=EXCLUDED.keyword,template=EXCLUDED.template,updated_at=now()`, name, dest, priority, r.FormValue("sender"), r.FormValue("system"), r.FormValue("site"), r.FormValue("talkgroup"), r.FormValue("radio"), r.FormValue("call_type"), r.FormValue("keyword"), r.FormValue("template"))
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
	s.page(w, r, "admin_transcription.html", "Transcription", "transcription", map[string]any{"Enabled": enabled, "Provider": provider, "Endpoint": endpoint, "Model": model, "Secret": secret, "Pending": pending, "Failed": failed, "Completed": done})
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
