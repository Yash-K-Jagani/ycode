package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/headless"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/providers/ollama"
	"github.com/Yash-K-Jagani/ycode/internal/providers/registry"
	"github.com/Yash-K-Jagani/ycode/internal/router"
)

type Server struct {
	mux *http.ServeMux
}

func New() *Server {
	s := &Server{mux: http.NewServeMux()}
	s.mux.HandleFunc("/v1/models", s.models)
	s.mux.HandleFunc("/v1/status", s.status)
	s.mux.HandleFunc("/v1/chat", s.chat)
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func Run(addr string) error {
	srv := &http.Server{Addr: addr, Handler: New().Handler(), ReadTimeout: 30 * time.Second, WriteTimeout: 20 * time.Minute}
	fmt.Println("ycode api on", addr)
	return srv.ListenAndServe()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	cfg, _ := config.Load()
	installed, _ := ollama.New(cfg.OllamaHost).ListModels(r.Context())
	writeJSON(w, map[string]any{"installed": installed, "catalog": registry.Catalog()})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	cfg, _ := config.Load()
	p, c, usd := cost.New().Today()
	writeJSON(w, map[string]any{
		"provider": cfg.ActiveProvider, "model": cfg.ActiveModel,
		"today_prompt": p, "today_completion": c, "today_usd": usd,
		"latency": router.New(cfg).Stats().Summary(),
	})
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Prompt  string `json:"prompt"`
		Mode    string `json:"mode"`
		Agent   string `json:"agent"`
		Workdir string `json:"workdir"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Prompt == "" {
		http.Error(w, "prompt required", http.StatusBadRequest)
		return
	}
	cfg, _ := config.Load()
	mode := modes.Chat
	if req.Mode != "" {
		m, err := modes.Parse(req.Mode)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mode = m
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	answer, err := headless.Run(ctx, cfg, req.Prompt, headless.Options{Mode: mode, Agent: req.Agent, Workdir: req.Workdir})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"answer": answer})
}
