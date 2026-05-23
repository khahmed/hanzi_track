// Package agents provides the Orchestrator engine for the Pedagogical Learning
// Loop (Loop 1). It analyzes review_logs and rewrites system_agents prompts to
// adjust the challenge/comprehension balance.
package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"hanzitrack/backend/llm"
)

// AgentStats holds accuracy statistics for one (agent_type, quiz_type) pair.
type AgentStats struct {
	AgentType       string  `json:"agent_type"`
	QuizType        string  `json:"quiz_type"`
	Total           int     `json:"total"`
	Correct         int     `json:"correct"`
	Incorrect       int     `json:"incorrect"`
	PctCorrect      float64 `json:"pct_correct"`
	RecentPctCorrect float64 `json:"recent_pct_correct"`
	Trend           string  `json:"trend"` // "improving", "declining", "flat"
}

// PromptChange records a single agent prompt mutation.
type PromptChange struct {
	AgentName string `json:"agent_name"`
	OldPrompt string `json:"old_prompt"`
	NewPrompt string `json:"new_prompt"`
	Changed   bool   `json:"changed"`
}

// Result is returned by Orchestrate after processing all agents.
type Result struct {
	Changes []PromptChange `json:"changes"`
	Stats   []AgentStats   `json:"stats"`
}

// Orchestrator reads review logs and rewrites agent system prompts via LLM.
type Orchestrator struct {
	DB       *sql.DB
	Registry *llm.Registry
}

// Stats returns per-agent accuracy data aggregated from review_logs.
func (o *Orchestrator) Stats(ctx context.Context) ([]AgentStats, error) {
	// Overall stats grouped by agent_type and quiz_type.
	rows, err := o.DB.QueryContext(ctx, `
		SELECT agent_type, quiz_type,
		       COUNT(*) AS total,
		       SUM(is_correct) AS correct
		  FROM review_logs
		 GROUP BY agent_type, quiz_type
		 ORDER BY agent_type, quiz_type`)
	if err != nil {
		return nil, fmt.Errorf("stats query: %w", err)
	}
	defer rows.Close()

	type rawStat struct {
		AgentType string
		QuizType  string
		Total     int
		Correct   int
	}
	var raw []rawStat
	for rows.Next() {
		var rs rawStat
		if err := rows.Scan(&rs.AgentType, &rs.QuizType, &rs.Total, &rs.Correct); err != nil {
			return nil, fmt.Errorf("stats scan: %w", err)
		}
		raw = append(raw, rs)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return []AgentStats{}, nil
	}

	// Recent accuracy per agent_type (last 20 entries).
	recent, err := o.recentAccuracy(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]AgentStats, 0, len(raw))
	for _, rs := range raw {
		s := AgentStats{
			AgentType:  rs.AgentType,
			QuizType:   rs.QuizType,
			Total:      rs.Total,
			Correct:    rs.Correct,
			Incorrect:  rs.Total - rs.Correct,
			PctCorrect: roundPct(rs.Correct, rs.Total),
		}
		if r, ok := recent[rs.AgentType]; ok {
			s.RecentPctCorrect = r.pct
			s.Trend = computeTrend(s.PctCorrect, r.pct, r.count)
		} else {
			s.RecentPctCorrect = 0
			s.Trend = "flat"
		}
		out = append(out, s)
	}
	return out, nil
}

type recentInfo struct {
	pct   float64
	count int
}

func (o *Orchestrator) recentAccuracy(ctx context.Context) (map[string]recentInfo, error) {
	rows, err := o.DB.QueryContext(ctx, `
		SELECT agent_type, is_correct
		  FROM (
		    SELECT agent_type, is_correct,
		           ROW_NUMBER() OVER (PARTITION BY agent_type ORDER BY reviewed_at DESC) AS rn
		      FROM review_logs
		  )
		 WHERE rn <= 20
		 ORDER BY agent_type, rn`)
	if err != nil {
		return nil, fmt.Errorf("recent accuracy query: %w", err)
	}
	defer rows.Close()

	type entry struct {
		correct int
		total   int
	}
	acc := map[string]*entry{}
	for rows.Next() {
		var agent string
		var correct int
		if err := rows.Scan(&agent, &correct); err != nil {
			return nil, fmt.Errorf("recent scan: %w", err)
		}
		if acc[agent] == nil {
			acc[agent] = &entry{}
		}
		acc[agent].total++
		acc[agent].correct += correct
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := map[string]recentInfo{}
	for agent, e := range acc {
		out[agent] = recentInfo{
			pct:   roundPct(e.correct, e.total),
			count: e.total,
		}
	}
	return out, nil
}

// agentRow holds a system_agents row during orchestration.
type agentRow struct {
	ID           int64
	Name         string
	SystemPrompt string
	Temperature  float64
	Provider     string
	Model        string
}

// Orchestrate analyzes review_logs and rewrites system_agents prompts via LLM.
func (o *Orchestrator) Orchestrate(ctx context.Context) (*Result, error) {
	stats, err := o.Stats(ctx)
	if err != nil {
		return nil, fmt.Errorf("orchestrate: stats: %w", err)
	}

	// Load all active agents.
	rows, err := o.DB.QueryContext(ctx, `
		SELECT id, name, system_prompt, temperature, provider, model
		  FROM system_agents
		 WHERE is_active = 1
		 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("orchestrate: load agents: %w", err)
	}
	defer rows.Close()

	var agents []agentRow
	for rows.Next() {
		var a agentRow
		if err := rows.Scan(&a.ID, &a.Name, &a.SystemPrompt, &a.Temperature, &a.Provider, &a.Model); err != nil {
			return nil, fmt.Errorf("orchestrate: scan agent: %w", err)
		}
		agents = append(agents, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var changes []PromptChange
	for _, a := range agents {
		// Skip agents with no review data — nothing to tune.
		hasData := false
		for _, s := range stats {
			if s.AgentType == a.Name {
				hasData = true
				break
			}
		}
		if !hasData {
			changes = append(changes, PromptChange{
				AgentName: a.Name,
				OldPrompt: a.SystemPrompt,
				NewPrompt: a.SystemPrompt,
				Changed:   false,
			})
			continue
		}

		change, err := o.tuneAgent(ctx, a, stats)
		if err != nil {
			return nil, fmt.Errorf("orchestrate: tune %q: %w", a.Name, err)
		}
		changes = append(changes, change)
	}

	return &Result{Changes: changes, Stats: stats}, nil
}

func (o *Orchestrator) tuneAgent(ctx context.Context, a agentRow, stats []AgentStats) (PromptChange, error) {
	change := PromptChange{AgentName: a.Name, OldPrompt: a.SystemPrompt}

	// Filter stats for this agent type.
	var relevant []string
	for _, s := range stats {
		if s.AgentType == a.Name {
			relevant = append(relevant, fmt.Sprintf(
				"- %s: %d/%d correct (%.0f%%) — recent: %.0f%% — trend: %s",
				s.QuizType, s.Correct, s.Total, s.PctCorrect*100, s.RecentPctCorrect*100, s.Trend))
		}
	}
	statsBlock := strings.Join(relevant, "\n")

	prompt := fmt.Sprintf(`You are an expert Chinese pedagogy consultant. Your task is to review the user's recent learning performance and adjust the agent's system prompt to maintain an optimal balance between challenge and comprehension.

Current agent name: %s
Current system prompt:
%s

User's performance by quiz type:
%s

- If accuracy is high (>90%%): increase difficulty by asking for harder constructions.
- If accuracy is low (<60%%): add scaffolding instructions to make the task easier.
- If accuracy is moderate (60-90%%): fine-tune the existing approach.
- If the trend is "declining": the current approach may not be working — suggest a different strategy.
- If the trend is "improving": consider slightly increasing difficulty.

Respond ONLY with a JSON object matching this exact shape — no markdown, no commentary:
{
  "revised_prompt": "the full revised system prompt text",
  "rationale": "one-sentence explanation of what changed and why"
}`,
		a.Name, a.SystemPrompt, statsBlock)

	client, err := o.Registry.Get(a.Provider)
	if err != nil {
		// Agent has no LLM provider — leave prompt unchanged.
		change.Changed = false
		change.NewPrompt = a.SystemPrompt
		return change, nil
	}

	llmReq := llm.Request{
		Model:       a.Model,
		System:      "You are a pedagogical consultant that outputs JSON.",
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: prompt}},
		Temperature: a.Temperature,
		MaxTokens:   512,
	}

	resp, err := client.Complete(ctx, llmReq)
	if err != nil {
		return change, fmt.Errorf("llm call: %w", err)
	}

	var result struct {
		RevisedPrompt string `json:"revised_prompt"`
		Rationale     string `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		// Parse failure — leave prompt unchanged.
		change.Changed = false
		change.NewPrompt = a.SystemPrompt
		return change, nil
	}
	if result.RevisedPrompt == "" || result.RevisedPrompt == a.SystemPrompt {
		change.Changed = false
		change.NewPrompt = a.SystemPrompt
		return change, nil
	}

	// Persist the new prompt.
	_, err = o.DB.ExecContext(ctx,
		`UPDATE system_agents
		    SET system_prompt = ?, updated_at = CURRENT_TIMESTAMP
		  WHERE id = ?`,
		result.RevisedPrompt, a.ID)
	if err != nil {
		return change, fmt.Errorf("update prompt: %w", err)
	}

	change.Changed = true
	change.NewPrompt = result.RevisedPrompt
	return change, nil
}

func roundPct(correct, total int) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(correct)/float64(total)*10000) / 10000
}

func computeTrend(overall, recent float64, recentCount int) string {
	if recentCount < 5 {
		return "flat"
	}
	diff := recent - overall
	if diff > 0.05 {
		return "improving"
	}
	if diff < -0.05 {
		return "declining"
	}
	return "flat"
}
