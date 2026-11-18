package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"matchcore/internal/matching"
	"matchcore/internal/model"
)

func New(token string, wait time.Duration, ping func(context.Context) error, services ...*matching.Service) http.Handler {
	var service *matching.Service
	if len(services) > 0 {
		service = services[0]
	}
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
	getSnapshot := func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			notReady(w, r)
			return
		}
		snapshot, err := service.Snapshot()
		if err != nil {
			respond(w, http.StatusServiceUnavailable, "data_unavailable")
			return
		}
		if name := r.PathValue("group"); name != "" {
			found := false
			for _, group := range snapshot.Groups {
				if group.Name == name {
					snapshot.Groups = []model.Group{group}
					found = true
					break
				}
			}
			if !found {
				respond(w, http.StatusNotFound, "group_not_found")
				return
			}
		}
		respondData(w, http.StatusOK, snapshot)
	}
	api.HandleFunc("GET /v1/groups", getSnapshot)
	api.HandleFunc("GET /v1/groups/{group}/servers", getSnapshot)
	api.HandleFunc("POST /v1/matches", func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			notReady(w, r)
			return
		}
		typeName, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || typeName != "application/json" {
			respond(w, http.StatusBadRequest, "bad_request")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
		defer r.Body.Close()
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var request model.MatchRequest
		if err = dec.Decode(&request); err != nil {
			respond(w, http.StatusBadRequest, "bad_request")
			return
		}
		var more any
		if err = dec.Decode(&more); err != io.EOF {
			respond(w, http.StatusBadRequest, "bad_request")
			return
		}
		ticket, err := service.Match(r.Context(), request)
		switch {
		case err == nil:
			respondData(w, http.StatusOK, ticket)
		case errors.Is(err, matching.ErrRequest):
			respond(w, http.StatusBadRequest, "bad_request")
		case errors.Is(err, matching.ErrGroup):
			respond(w, http.StatusNotFound, "group_not_found")
		case errors.Is(err, matching.ErrNoRoom):
			respond(w, http.StatusConflict, "no_room")
		default:
			respond(w, http.StatusServiceUnavailable, "data_unavailable")
		}
	})
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
	respond(w, http.StatusServiceUnavailable, "data_unavailable")
}

func respond(w http.ResponseWriter, status int, code string) {
	respondData(w, status, struct {
		Code string `json:"code"`
	}{Code: code})
}

func respondData(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
