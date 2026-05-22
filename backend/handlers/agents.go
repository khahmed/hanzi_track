package handlers

import (
	"database/sql"
	"log"
	"net/http"
)

// Agent is the wire shape returned by GET /api/agents. Mirrors the
// system_agents row 1:1 — the frontend uses display_name for the tab label
// and name as the agent identifier when routing to /api/agents/<thing>.
type Agent struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	DisplayName  string  `json:"display_name"`
	SystemPrompt string  `json:"system_prompt"`
	Temperature  float64 `json:"temperature"`
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	IsActive     bool    `json:"is_active"`
}

type listAgentsResponse struct {
	Results []Agent `json:"results"`
}

// ListAgents handles GET /api/agents — returns active agents in insertion
// order so the frontend tab nav is stable across reloads.
func ListAgents(database *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := database.QueryContext(r.Context(),
			`SELECT id, name, display_name, system_prompt, temperature, provider, model, is_active
			   FROM system_agents
			  WHERE is_active = 1
			  ORDER BY id`)
		if err != nil {
			log.Printf("list agents: %v", err)
			http.Error(w, "query failed", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		results := []Agent{}
		for rows.Next() {
			var a Agent
			var active int
			if err := rows.Scan(&a.ID, &a.Name, &a.DisplayName, &a.SystemPrompt,
				&a.Temperature, &a.Provider, &a.Model, &active); err != nil {
				http.Error(w, "scan failed", http.StatusInternalServerError)
				return
			}
			a.IsActive = active != 0
			results = append(results, a)
		}
		if err := rows.Err(); err != nil {
			http.Error(w, "iter failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, listAgentsResponse{Results: results})
	}
}
