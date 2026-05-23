package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"hanzitrack/backend/devops"
)

type mutateRequest struct {
	FeatureDescription string `json:"feature_description"`
}

type mutateResponse struct {
	JobID        int64    `json:"job_id"`
	Status       string   `json:"status"`
	FilesChanged []string `json:"files_changed,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Branch       string   `json:"branch,omitempty"`
	PRURL        string   `json:"pr_url,omitempty"`
	Error        string   `json:"error,omitempty"`
}

// Mutate handles POST /api/devops/mutate. If the client Accepts
// text/event-stream it streams real-time status updates via SSE;
// otherwise it returns a single JSON response.
func Mutate(m *devops.Mutator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req mutateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if req.FeatureDescription == "" {
			http.Error(w, "feature_description is required", http.StatusBadRequest)
			return
		}

		accept := r.Header.Get("Accept")
		wantsSSE := strings.Contains(accept, "text/event-stream")

		if wantsSSE {
			mutateSSE(w, r, m, req.FeatureDescription)
		} else {
			mutateJSON(w, r, m, req.FeatureDescription)
		}
	}
}

// mutateJSON runs mutation and returns a single JSON response.
func mutateJSON(w http.ResponseWriter, r *http.Request, m *devops.Mutator, featureDesc string) {
	result, err := m.Mutate(r.Context(), featureDesc)
	if err != nil {
		log.Printf("mutate: %v", err)
		http.Error(w, "mutation failed", http.StatusInternalServerError)
		return
	}

	resp := mutateResponse{
		JobID:        result.JobID,
		Status:       result.Status,
		FilesChanged: result.FilesChanged,
		Summary:      result.Summary,
		Branch:       result.Branch,
		PRURL:        result.PRURL,
		Error:        result.Error,
	}

	status := http.StatusOK
	if resp.Status == "failed" || resp.Status == "failed_validation" {
		status = http.StatusInternalServerError
	}
	w.WriteHeader(status)
	writeJSON(w, resp)
}

// mutateSSE runs mutation and streams events via Server-Sent Events.
func mutateSSE(w http.ResponseWriter, r *http.Request, m *devops.Mutator, featureDesc string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan any, 32)
	m.OnEvent = func(ev devops.MutationEvent) {
		ch <- ev
	}
	go func() {
		defer close(ch)
		res, err := m.Mutate(r.Context(), featureDesc)
		if err != nil {
			ch <- map[string]string{"error": err.Error()}
			return
		}
		ch <- res
	}()

	for v := range ch {
		switch typed := v.(type) {
		case devops.MutationEvent:
			writeSSE(w, typed.Step, typed)
		case *devops.MutationResult:
			writeSSE(w, "result", typed)
		case map[string]string:
			writeSSE(w, "error", typed)
		}
		flusher.Flush()
	}

	m.OnEvent = nil
}

func writeSSE(w http.ResponseWriter, event string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		log.Printf("sse marshal: %v", err)
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
}
