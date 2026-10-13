package workflows

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

//go:embed web/index.html web/app.css web/app.js
var web embed.FS

// IdleTimeout shuts the editor down when nobody has used it for this long.
const IdleTimeout = 30 * time.Minute

// Server is the workflow editor: a page on 127.0.0.1 only, and an API that
// answers only requests carrying its one-time token. It reads and writes
// nothing but ~/.pie/workflows files for the repos it was started with.
type Server struct {
	token string
	repos []string
	host  string // "127.0.0.1:<port>", set by Serve
	last  atomic.Int64
	Logf  func(string, ...interface{})
}

// New is an editor for repos (absolute paths, in display order).
func New(repos []string) *Server {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	s := &Server{token: hex.EncodeToString(b), repos: repos, Logf: log.Printf}
	s.last.Store(time.Now().Unix())
	return s
}

// Serve listens on a random localhost port, calls ready with the page's URL
// (the token rides in the fragment, which browsers never send), and serves
// until ctx ends or the editor sits idle for IdleTimeout.
func (s *Server) Serve(ctx context.Context, ready func(url string)) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.host = ln.Addr().String()
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = srv.Close()
				return
			case <-t.C:
				if time.Since(time.Unix(s.last.Load(), 0)) > IdleTimeout {
					s.Logf("workflow editor idle for %s - closing", IdleTimeout)
					cancel()
				}
			}
		}
	}()
	ready(fmt.Sprintf("http://%s/#%s", s.host, s.token))
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Handler is the editor's routes behind the Host, Origin and token checks.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /app.css", s.asset("web/app.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /app.js", s.asset("web/app.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /api/state", s.api(s.state))
	mux.HandleFunc("POST /api/preview", s.api(s.preview))
	mux.HandleFunc("PUT /api/workflows", s.api(s.save))
	return s.guard(mux)
}

// guard refuses other hosts (DNS rebinding) and cross-site writes.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.host != "" && !s.localHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && r.Method != http.MethodGet && !s.localHost(strings.TrimPrefix(o, "http://")) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		s.last.Store(time.Now().Unix())
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) localHost(h string) bool {
	_, port, _ := net.SplitHostPort(s.host)
	return h == s.host || h == "localhost:"+port
}

func (s *Server) page(w http.ResponseWriter, _ *http.Request) {
	b, _ := web.ReadFile("web/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'")
	_, _ = w.Write(b)
}

// asset serves one embedded file (no token: it is the page's own code).
func (s *Server) asset(name, ctype string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		b, _ := web.ReadFile(name)
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	}
}

type apiFn func(r *http.Request) (any, int, error)

// api checks the token and writes the handler's JSON (or its error).
func (s *Server) api(fn apiFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Pie-Token")), []byte(s.token)) != 1 {
			http.Error(w, "missing or wrong token - reopen the editor from Apple Pie", http.StatusUnauthorized)
			return
		}
		v, code, err := fn(r)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			s.Logf("workflow editor: %s %s: %v", r.Method, r.URL.Path, err)
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	}
}

// repo is the request's repo, which must be one the editor was opened for.
func (s *Server) repo(name string) (string, error) {
	if name == "" && len(s.repos) > 0 {
		return s.repos[0], nil
	}
	for _, r := range s.repos {
		if r == name {
			return r, nil
		}
	}
	return "", fmt.Errorf("%q is not a configured repo", name)
}

type stageView struct {
	agent.Stage
	Blocks []agent.Block `json:"blocks"`
}

type stateView struct {
	Repos     []string            `json:"repos"`
	Repo      string              `json:"repo"`
	File      string              `json:"file"`
	Slots     []agent.Slot        `json:"slots"`
	Saved     map[string]string   `json:"saved"`
	Stages    []stageView         `json:"stages"`
	Workflows []Workflow          `json:"workflows"`
	Lint      map[string][]string `json:"lint"`
}

// view renders every stage with these slot texts.
func view(slots map[string]string) ([]stageView, map[string][]string) {
	p := agent.NewPrompts(slots).Marked()
	out := make([]stageView, 0, len(agent.Stages))
	for _, st := range agent.Stages {
		out = append(out, stageView{Stage: st, Blocks: agent.SplitSlots(p.Sample(st.ID))})
	}
	lint := map[string][]string{}
	for id, t := range slots {
		if w := Lint(t); len(w) > 0 && agent.KnownSlot(id) {
			lint[id] = w
		}
	}
	return out, lint
}

func (s *Server) state(r *http.Request) (any, int, error) {
	repo, err := s.repo(r.URL.Query().Get("repo"))
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	return s.stateFor(repo)
}

func (s *Server) stateFor(repo string) (any, int, error) {
	saved, err := Load(repo)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	stages, lint := view(saved)
	return stateView{Repos: s.repos, Repo: repo, File: tilde(paths.WorkflowsFor(repo)), Slots: agent.Slots,
		Saved: saved, Stages: stages, Workflows: Workflows, Lint: lint}, http.StatusOK, nil
}

type slotsReq struct {
	Repo  string            `json:"repo"`
	Slots map[string]string `json:"slots"`
}

func decode(r *http.Request) (slotsReq, error) {
	var req slotsReq
	err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(&req)
	return req, err
}

// preview renders unsaved slot text - the page's live preview.
func (s *Server) preview(r *http.Request) (any, int, error) {
	req, err := decode(r)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if _, err := s.repo(req.Repo); err != nil {
		return nil, http.StatusBadRequest, err
	}
	stages, lint := view(req.Slots)
	return map[string]any{"stages": stages, "lint": lint}, http.StatusOK, nil
}

func (s *Server) save(r *http.Request) (any, int, error) {
	req, err := decode(r)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	repo, err := s.repo(req.Repo)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if err := Save(repo, req.Slots); err != nil {
		return nil, http.StatusBadRequest, err
	}
	s.Logf("workflow editor: saved %s", paths.WorkflowsFor(repo))
	return s.stateFor(repo)
}

// tilde shortens a path under $HOME for display.
func tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}
