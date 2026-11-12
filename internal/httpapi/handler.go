package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"
)

func New(token string, wait time.Duration, ping func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), wait)
		defer cancel()
		if err := ping(ctx); err != nil {
			respond(w, http.StatusServiceUnavailable, "redis_unavailable")
			return
		}
		respond(w, http.StatusOK, "ok")
	})
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/groups", notReady)
	api.HandleFunc("GET /v1/groups/{group}/servers", notReady)
	api.HandleFunc("POST /v1/matches", notReady)
	mux.Handle("/v1/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values("Authorization")
		if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte("Bearer "+token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			respond(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		api.ServeHTTP(w, r)
	}))
	return mux
}

func notReady(w http.ResponseWriter, r *http.Request) {
	respond(w, http.StatusNotImplemented, "not_implemented")
}

func respond(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code string `json:"code"`
	}{Code: code})
}
