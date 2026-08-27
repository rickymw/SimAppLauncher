package gui

import (
	"encoding/json"
	"net/http"
)

// errorBody is the single shape every failing endpoint returns, so the page has
// one place to look for a message instead of guessing whether a non-2xx
// response carried JSON or a plain string.
type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Every response is computed fresh from the rig's current state; a cached
	// status panel showing apps that stopped five minutes ago would be worse
	// than a slow one.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	// The response header is already written, so a marshalling failure here can
	// only be logged, not reported. In practice it means a value the handler
	// built cannot encode, which is a bug rather than a runtime condition.
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// unsupported is the reply for a panel whose provider is nil — the feature
// exists but not on this OS. 501 rather than 404 so the page can tell "this
// build cannot do that" from "you asked for a route that does not exist".
func unsupported(w http.ResponseWriter, what string) {
	writeErr(w, http.StatusNotImplemented, what+" is only available on Windows")
}
