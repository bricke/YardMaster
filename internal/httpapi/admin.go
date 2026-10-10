package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"yardmaster/internal/audit"
	"yardmaster/internal/auth"
	"yardmaster/internal/deploy"
	"yardmaster/internal/jev"
	"yardmaster/internal/secrets"
	"yardmaster/internal/usage"
)

// ---- overview and usage ----

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, u *auth.User) {
	f := usage.Filter{Days: 1}
	summary, err := s.Ledger.Summarize(r.Context(), f)
	if err != nil {
		logErr(w, err)
		return
	}
	byUser, err := s.Ledger.ByUser(r.Context(), f)
	if err != nil {
		logErr(w, err)
		return
	}
	if len(byUser) > 5 {
		byUser = byUser[:5]
	}
	errs, err := s.Ledger.Recent(r.Context(), usage.Filter{Days: 7}, true, 10)
	if err != nil {
		logErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"switchyard": s.Sup.Status(),
		"live":       s.Metrics.Points(),
		"today":      summary,
		"top_users":  byUser,
		"errors":     errs,
		"configured": s.Applier.Parsed() != nil,
	})
}

// handleAdminUsage is total usage and usage per user through a filter.
func (s *Server) handleAdminUsage(w http.ResponseWriter, r *http.Request, u *auth.User) {
	f := usage.Filter{Days: queryInt(r, "days", 30), UserName: r.URL.Query().Get("user")}
	summary, err := s.Ledger.Summarize(r.Context(), f)
	if err != nil {
		logErr(w, err)
		return
	}
	byUser, err := s.Ledger.ByUser(r.Context(), usage.Filter{Days: f.Days})
	if err != nil {
		logErr(w, err)
		return
	}
	recent, err := s.Ledger.Recent(r.Context(), f, r.URL.Query().Get("errors") == "1", 100)
	if err != nil {
		logErr(w, err)
		return
	}
	monthly, err := s.Ledger.Monthly(r.Context(), f.UserName)
	if err != nil {
		logErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": summary, "by_user": byUser, "recent": recent, "monthly": monthly,
	})
}

// ---- users ----

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request, u *auth.User) {
	users, err := s.Auth.Users(r.Context())
	if err != nil {
		logErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request, admin *auth.User) {
	var in struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	nu, temp, err := s.Auth.CreateUser(r.Context(), in.Username, in.DisplayName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r, admin, audit.UserCreated, nu.Username)
	// Shown to the admin once, to hand over.
	writeJSON(w, http.StatusOK, map[string]any{"user": nu, "temporary_password": temp})
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request, admin *auth.User) {
	id, ok := pathID(w, r, "user")
	if !ok {
		return
	}
	target, err := s.Auth.UserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such user")
		return
	}
	if target.Source != auth.SourceBuiltin {
		writeError(w, http.StatusBadRequest, "this user signs in through the proxy")
		return
	}
	if target.ID == admin.ID {
		writeError(w, http.StatusBadRequest, "change your own password from your account menu instead")
		return
	}
	temp, err := s.Auth.ResetPassword(r.Context(), id)
	if err != nil {
		logErr(w, err)
		return
	}
	s.record(r, admin, audit.PasswordReset, target.Username)
	writeJSON(w, http.StatusOK, map[string]any{"temporary_password": temp})
}

func (s *Server) handleSetActive(w http.ResponseWriter, r *http.Request, admin *auth.User) {
	id, ok := pathID(w, r, "user")
	if !ok {
		return
	}
	var in struct {
		Active bool `json:"active"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	target, err := s.Auth.UserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such user")
		return
	}
	if err := s.Auth.SetActive(r.Context(), id, in.Active); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	action := audit.UserDeactivated
	if in.Active {
		action = audit.UserReactivated
	}
	s.record(r, admin, action, target.Username)
	writeOK(w)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request, admin *auth.User) {
	id, ok := pathID(w, r, "user")
	if !ok {
		return
	}
	u, err := s.Auth.DeleteUser(r.Context(), id)
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such user")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The name can be given to someone else, who mustn't see this person's usage.
	now := time.Now()
	s.Ledger.RelabelUser(u.ID, u.Username, deletedLabel(u.Username, now), now)
	s.record(r, admin, audit.UserDeleted, u.Username)
	writeOK(w)
}

// deletedLabel is what a deleted user's usage is filed under. It can't be anyone's
// username, which never holds spaces or brackets.
func deletedLabel(name string, at time.Time) string {
	return name + " (deleted " + at.UTC().Format(time.DateTime) + ")"
}

// ---- deployment ----

func (s *Server) handleDeployment(w http.ResponseWriter, r *http.Request, u *auth.User) {
	model, source, err := s.Applier.Model(r.Context())
	if err != nil {
		logErr(w, err)
		return
	}
	current, err := s.Applier.Current()
	if err != nil {
		logErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"model": model, "source": source, "toml": current, "formats": deploy.Formats,
		"openai_efforts": deploy.OpenAIEfforts, "anthropic_efforts": deploy.AnthropicEfforts,
		"typesafe_base_url": jev.BaseURL(s.Settings.JudgePort),
	})
}

func (s *Server) handleSaveModel(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var d deploy.Deployment
	if !readJSON(w, r, &d) {
		return
	}
	if err := s.Applier.SaveModel(r.Context(), &d); err != nil {
		logErr(w, err)
		return
	}
	writeOK(w)
}

// candidate is either a wizard model or raw TOML.
type candidate struct {
	Model *deploy.Deployment `json:"model"`
	TOML  string             `json:"toml"`
}

func (c candidate) render() (text, source string, err error) {
	if c.Model != nil {
		if err := c.Model.Validate(); err != nil {
			return "", "", err
		}
		text, err := c.Model.TOML()
		return text, deploy.SourceWizard, err
	}
	if strings.TrimSpace(c.TOML) == "" {
		return "", "", errors.New("nothing to apply")
	}
	return c.TOML, deploy.SourceTOML, nil
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var c candidate
	if !readJSON(w, r, &c) {
		return
	}
	text, _, err := c.render()
	if err != nil {
		writeJSON(w, http.StatusOK, &deploy.Preview{Errors: err.Error()})
		return
	}
	p, err := s.Applier.Preview(r.Context(), text)
	if err != nil {
		logErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var c candidate
	if !readJSON(w, r, &c) {
		return
	}
	text, source, err := c.render()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	previous, _ := s.Applier.Current()
	// Applying drains and restarts the router; it shouldn't be cut short because the
	// browser navigated away.
	ctx := context.WithoutCancel(r.Context())
	err = s.Applier.Apply(ctx, text, source, c.Model)
	diff := diffText(previous, text)
	var ae *deploy.ApplyError
	if errors.As(err, &ae) && ae.RolledBack {
		s.record(r, u, audit.ConfigRolledBack, ae.Reason+"\n"+diff)
	}
	switch {
	case writeApplyError(w, err):
	case err != nil:
		logErr(w, err)
	default:
		s.record(r, u, audit.ConfigApplied, diff)
		writeOK(w)
	}
}

// diffText is a compact diff for the audit log: changed lines only.
func diffText(old, new string) string {
	var b strings.Builder
	for _, l := range deploy.Diff(old, new) {
		if l.Op != " " {
			b.WriteString(l.Op + " " + l.Text + "\n")
		}
	}
	out := b.String()
	if len(out) > 8000 {
		out = out[:8000] + "\n…"
	}
	return out
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, u *auth.User) {
	h, err := s.Applier.History()
	if err != nil {
		logErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": h})
}

func (s *Server) handleHistoryConfig(w http.ResponseWriter, r *http.Request, u *auth.User) {
	text, err := s.Applier.HistoryConfig(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"toml": text})
}

// ---- provider keys ----

type keyStatus struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	UsedBy []string `json:"used_by"`
}

// handleKeys lists every key variable the saved model or the running config names, with
// where its value comes from. Values are never returned.
func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request, u *auth.User) {
	used := map[string][]string{}
	if model, _, err := s.Applier.Model(r.Context()); err == nil {
		for _, c := range model.Clients {
			if c.Auth == deploy.AuthKey && c.KeyEnv != "" {
				used[c.KeyEnv] = appendNew(used[c.KeyEnv], c.Name)
			}
		}
	}
	if p := s.Applier.Parsed(); p != nil {
		for name, c := range p.Clients {
			if c.APIKeyEnv != "" {
				used[c.APIKeyEnv] = appendNew(used[c.APIKeyEnv], name)
			}
		}
	}
	// The wizard also asks about names it hasn't saved yet.
	for _, name := range strings.Split(r.URL.Query().Get("names"), ",") {
		if name = strings.TrimSpace(name); secrets.ValidName(name) {
			if _, ok := used[name]; !ok {
				used[name] = []string{}
			}
		}
	}
	out := []keyStatus{}
	for name, clients := range used {
		st, err := s.Keys.Status(name)
		if err != nil {
			logErr(w, err)
			return
		}
		sort.Strings(clients)
		out = append(out, keyStatus{Name: name, Status: st, UsedBy: clients})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"keys": out, "encrypted": s.Keys.Encrypted()})
}

func appendNew(list []string, v string) []string {
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

// runningConfigUses reports whether the applied config reads a key from the variable name.
func (s *Server) runningConfigUses(name string) bool {
	p := s.Applier.Parsed()
	return p != nil && slices.Contains(p.KeyEnvNames(), name)
}

// handleSetKey stores a key. If the running config uses it, Switchyard is restarted
// through the safe apply path, since keys only reach it at start.
func (s *Server) handleSetKey(w http.ResponseWriter, r *http.Request, u *auth.User) {
	name := r.PathValue("name")
	var in struct {
		Value string `json:"value"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	before, _ := s.Keys.Status(name)
	if err := s.Keys.Set(name, strings.TrimSpace(in.Value)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	detail := name + " set"
	if before == secrets.FromUI {
		detail = name + " replaced"
	}
	s.record(r, u, audit.KeySet, detail)
	if !s.runningConfigUses(name) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restarted": false})
		return
	}
	err := s.Applier.Restart(context.WithoutCancel(r.Context()))
	switch {
	case writeApplyError(w, err):
	case err != nil:
		logErr(w, err)
	default:
		s.record(r, u, audit.SwitchyardRestart, "to load "+name)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restarted": true})
	}
}

func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request, u *auth.User) {
	name := r.PathValue("name")
	if s.runningConfigUses(name) {
		writeError(w, http.StatusBadRequest, "the running config uses "+name+"; remove it from the config first")
		return
	}
	if err := s.Keys.Delete(name); err != nil {
		logErr(w, err)
		return
	}
	s.record(r, u, audit.KeyDeleted, name)
	writeOK(w)
}

// ---- switchyard ----

func (s *Server) handleSwitchyard(w http.ResponseWriter, r *http.Request, u *auth.User) {
	out := map[string]any{
		"status":  s.Sup.Status(),
		"logs":    s.Sup.Logs(),
		"version": s.Settings.SwitchyardVersion,
	}
	if stats, err := s.switchyardCall(r.Context(), http.MethodGet, "/v1/stats"); err == nil {
		out["stats"] = stats
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request, u *auth.User) {
	err := s.Applier.Restart(context.WithoutCancel(r.Context()))
	var ae *deploy.ApplyError
	if errors.As(err, &ae) {
		s.record(r, u, audit.SwitchyardRestart, "failed: "+ae.Reason)
	}
	if writeApplyError(w, err) {
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r, u, audit.SwitchyardRestart, "")
	writeOK(w)
}

func (s *Server) handleStatsReset(w http.ResponseWriter, r *http.Request, u *auth.User) {
	if _, err := s.switchyardCall(r.Context(), http.MethodPost, "/v1/stats/reset"); err != nil {
		writeError(w, http.StatusBadGateway, "the router isn't reachable")
		return
	}
	s.record(r, u, audit.StatsReset, "")
	writeOK(w)
}

// switchyardCall calls one of switchyard-server's own admin endpoints on loopback.
func (s *Server) switchyardCall(ctx context.Context, method, path string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, method, s.Sup.URL()+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return json.RawMessage(b), err
}

// playgroundLabel is the name playground calls are recorded under. The brackets keep it
// from ever being someone's username, so nobody sees them as their own usage.
const playgroundLabel = "(playground)"

// handlePlayground asks Switchyard which target it would pick (POST /v1/decision). The
// classifier and judge calls it makes still cost tokens, so they are recorded under the
// admin's name with origin playgroundLabel.
func (s *Server) handlePlayground(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var in struct {
		Route   string `json:"route"`
		Message string `json:"message"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Route == "" || strings.TrimSpace(in.Message) == "" {
		writeError(w, http.StatusBadRequest, "pick a route and write a message")
		return
	}
	body, _ := json.Marshal(map[string]any{
		"input_format": "openai_chat",
		"request": map[string]any{
			"model":    in.Route,
			"messages": []map[string]string{{"role": "user", "content": in.Message}},
		},
	})
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.Sup.URL()+"/v1/decision", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	requestID := fmt.Sprintf("ym-playground-%d", time.Now().UnixNano())
	req.Header.Set("X-Switchyard-Origin", playgroundLabel)
	req.Header.Set("X-Switchyard-Trial-Id", requestID)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "the router isn't reachable")
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	s.Ledger.AddGateway(usage.GatewayEvent{
		RequestID: requestID, At: start, UserID: u.ID, UserName: playgroundLabel,
		TokenName: "playground (" + u.Username + ")", Route: in.Route, Status: resp.StatusCode,
		LatencyMS: time.Since(start).Milliseconds(),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"status": resp.StatusCode, "elapsed_ms": time.Since(start).Milliseconds(), "result": asJSON(raw),
	})
}

// asJSON passes a JSON body through as is, and wraps anything else as a JSON string.
func asJSON(b []byte) json.RawMessage {
	if json.Valid(b) {
		return b
	}
	q, _ := json.Marshal(string(b))
	return q
}

// ---- prices ----

func (s *Server) handlePrices(w http.ResponseWriter, r *http.Request, u *auth.User) {
	type row struct {
		Target  string       `json:"target"`
		ModelID string       `json:"model_id"`
		Price   *usage.Price `json:"price"`
	}
	prices := map[string]usage.Price{}
	for _, p := range s.Prices.All() {
		prices[p.Target] = p
	}
	out := []row{}
	seen := map[string]bool{}
	if p := s.Applier.Parsed(); p != nil {
		for _, name := range p.TargetNames() {
			rw := row{Target: name, ModelID: p.TargetModel(name)}
			if pr, ok := prices[name]; ok {
				rw.Price = &pr
			}
			out = append(out, rw)
			seen[name] = true
		}
	}
	// Prices for targets no longer in the config stay visible so they can be removed.
	for name, pr := range prices {
		if !seen[name] {
			out = append(out, row{Target: name, Price: &pr})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"prices": out})
}

func (s *Server) handleSetPrice(w http.ResponseWriter, r *http.Request, u *auth.User) {
	var p usage.Price
	if !readJSON(w, r, &p) {
		return
	}
	p.Target = r.PathValue("target")
	if err := s.Prices.Set(r.Context(), p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.record(r, u, audit.PriceSet, fmt.Sprintf("%s: %s %.4g in / %.4g cached / %.4g cache write / %.4g out per 1M",
		p.Target, strings.ToUpper(p.Currency), p.Input, p.Cached, p.CacheWrite, p.Output))
	writeOK(w)
}

func (s *Server) handleDeletePrice(w http.ResponseWriter, r *http.Request, u *auth.User) {
	target := r.PathValue("target")
	if err := s.Prices.Delete(r.Context(), target); err != nil {
		logErr(w, err)
		return
	}
	s.record(r, u, audit.PriceDeleted, target)
	writeOK(w)
}

// ---- audit ----

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request, u *auth.User) {
	q := r.URL.Query()
	entries, err := s.Audit.Query(r.Context(), q.Get("actor"), q.Get("action"), int64(queryInt(r, "before", 0)), queryInt(r, "limit", 100))
	if err != nil {
		logErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
