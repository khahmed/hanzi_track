package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"hanzitrack/backend/llm"
)

const (
	chatHistoryDefaultLimit = 50
	chatHistoryMaxLimit     = 200
	// chatRecentContext is how many previous messages get fed back into the
	// LLM as conversation memory. SPEC §4 Agent B specifies "the last 10
	// records from chat_messages".
	chatRecentContext = 10
)

// Chat handles the Conversational Partner agent's HTTP surface.
type Chat struct {
	DB       *sql.DB
	Registry *llm.Registry
}

// ChatTurn is the wire shape returned by POST /api/agents/chat — the LLM
// produces three parallel strings (Chinese dialogue, pinyin, bracketed
// coach notes) so the frontend can render them as three stacked sections.
type ChatTurn struct {
	Chinese    string `json:"chinese"`
	Pinyin     string `json:"pinyin"`
	CoachNotes string `json:"coach_notes"`
}

// ChatMessage is one persisted row. AssistantID/UserID help the frontend
// dedupe an optimistic local echo against the server-canonical row.
type ChatMessage struct {
	ID         int64  `json:"id"`
	Role       string `json:"role"`
	Content    string `json:"content"`
	Pinyin     string `json:"pinyin,omitempty"`
	CoachNotes string `json:"coach_notes,omitempty"`
	CreatedAt  string `json:"created_at"`
}

type chatSendRequest struct {
	Message string `json:"message"`
}

type chatSendResponse struct {
	Turn      ChatTurn `json:"turn"`
	UserID    int64    `json:"user_id"`
	MessageID int64    `json:"message_id"`
}

// Send handles POST /api/agents/chat — runs one conversational turn end
// to end: load context, call the LLM, parse the JSON response, persist
// both the user prompt and the assistant reply.
func (c *Chat) Send(w http.ResponseWriter, r *http.Request) {
	var req chatSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	agent, err := c.loadAgent(r.Context(), "conversationalist")
	if err != nil {
		log.Printf("chat: load agent: %v", err)
		http.Error(w, "agent not configured", http.StatusInternalServerError)
		return
	}

	vocab, err := c.loadVocab(r.Context())
	if err != nil {
		log.Printf("chat: load vocab: %v", err)
		http.Error(w, "vocab lookup failed", http.StatusInternalServerError)
		return
	}
	history, err := c.loadHistory(r.Context(), chatRecentContext)
	if err != nil {
		log.Printf("chat: load history: %v", err)
		http.Error(w, "history lookup failed", http.StatusInternalServerError)
		return
	}

	client, err := c.Registry.Get(agent.Provider)
	if err != nil {
		log.Printf("chat: provider %q: %v", agent.Provider, err)
		http.Error(w, "provider not registered", http.StatusInternalServerError)
		return
	}

	messages := buildChatMessages(history, vocab, req.Message)
	llmReq := llm.Request{
		Model:       agent.Model,
		System:      agent.SystemPrompt + "\n\n" + chatJSONContract,
		Messages:    messages,
		Temperature: agent.Temperature,
		MaxTokens:   768,
	}

	turn, _, err := chatWithRetry(r.Context(), client, llmReq, 2)
	if err != nil {
		log.Printf("chat: llm: %v", err)
		http.Error(w, "agent failed to produce a valid response", http.StatusBadGateway)
		return
	}

	userID, assistantID, err := c.persistTurn(r.Context(), req.Message, turn)
	if err != nil {
		log.Printf("chat: persist: %v", err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, chatSendResponse{Turn: turn, UserID: userID, MessageID: assistantID})
}

type chatHistoryResponse struct {
	Results []ChatMessage `json:"results"`
}

// History handles GET /api/agents/chat/history?limit=N — returns the most
// recent N messages in chronological order (oldest first), so the frontend
// can paint the list top-to-bottom on mount.
func (c *Chat) History(w http.ResponseWriter, r *http.Request) {
	limit := chatHistoryDefaultLimit
	if s := strings.TrimSpace(r.URL.Query().Get("limit")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			if n > chatHistoryMaxLimit {
				n = chatHistoryMaxLimit
			}
			limit = n
		}
	}

	// Two-step: pull the newest `limit` ids, then re-order ascending for the
	// client. Simpler than a windowed OVER and the row count is tiny.
	rows, err := c.DB.QueryContext(r.Context(),
		`SELECT id, role, content, COALESCE(pinyin, ''), COALESCE(coach_notes, ''), created_at
		   FROM (
		     SELECT id, role, content, pinyin, coach_notes, created_at
		       FROM chat_messages
		   ORDER BY id DESC
		      LIMIT ?
		   )
		ORDER BY id ASC`, limit)
	if err != nil {
		log.Printf("chat: history: %v", err)
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []ChatMessage{}
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.Pinyin, &m.CoachNotes, &m.CreatedAt); err != nil {
			http.Error(w, "scan failed", http.StatusInternalServerError)
			return
		}
		out = append(out, m)
	}
	writeJSON(w, chatHistoryResponse{Results: out})
}

// chatJSONContract is appended to the agent's system prompt so the LLM
// produces a stable wire shape. The prompt in system_agents is mutable
// (Orchestrator can rewrite it); the JSON contract is fixed code so the
// frontend can't break under prompt drift.
const chatJSONContract = `Respond ONLY with a JSON object matching this exact shape — no markdown, no commentary:
{
  "chinese": "<your natural Mandarin reply>",
  "pinyin": "<numeric-tone pinyin transcription of the chinese field>",
  "coach_notes": "<bracketed analysis block: corrections, idiom translations, tone notes — in English>"
}
All three fields must be non-empty strings.`

func chatWithRetry(ctx context.Context, client llm.Client, req llm.Request, maxAttempts int) (ChatTurn, int, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := client.Complete(ctx, req)
		if err != nil {
			lastErr = err
			continue
		}
		var t ChatTurn
		if err := json.Unmarshal([]byte(resp.Content), &t); err != nil {
			lastErr = fmt.Errorf("malformed JSON: %w (content=%q)", err, resp.Content)
			continue
		}
		if strings.TrimSpace(t.Chinese) == "" || strings.TrimSpace(t.Pinyin) == "" || strings.TrimSpace(t.CoachNotes) == "" {
			lastErr = fmt.Errorf("incomplete turn: %+v", t)
			continue
		}
		return t, attempt, nil
	}
	return ChatTurn{}, maxAttempts, lastErr
}

// buildChatMessages translates persisted history + the new user message
// into the llm.Message slice. Vocabulary lives in the first user-turn
// preamble; subsequent turns are the literal exchange. Coach notes from
// past assistant turns are deliberately omitted from the LLM context —
// they were UI scaffolding for the user, not conversation memory.
func buildChatMessages(history []ChatMessage, vocab []chatVocab, userMessage string) []llm.Message {
	out := make([]llm.Message, 0, len(history)+2)

	var preamble strings.Builder
	preamble.WriteString("The user's logged vocabulary (hanzi | pinyin | english). Keep your replies within this dataset where possible:\n")
	if len(vocab) == 0 {
		preamble.WriteString("(no vocabulary recorded yet — fall back to HSK 1 register)\n")
	}
	for _, v := range vocab {
		fmt.Fprintf(&preamble, "- %s | %s | %s\n", v.Hanzi, v.Pinyin, v.English)
	}
	out = append(out, llm.Message{Role: llm.RoleUser, Content: preamble.String()})

	for _, m := range history {
		role := llm.RoleUser
		if m.Role == "assistant" {
			role = llm.RoleAssistant
		}
		out = append(out, llm.Message{Role: role, Content: m.Content})
	}
	out = append(out, llm.Message{Role: llm.RoleUser, Content: userMessage})
	return out
}

func (c *Chat) loadAgent(ctx context.Context, name string) (loadedAgent, error) {
	var a loadedAgent
	err := c.DB.QueryRowContext(ctx,
		`SELECT name, system_prompt, temperature, provider, model
		   FROM system_agents
		  WHERE name = ? AND is_active = 1`, name).
		Scan(&a.Name, &a.SystemPrompt, &a.Temperature, &a.Provider, &a.Model)
	return a, err
}

type chatVocab struct {
	Hanzi   string
	Pinyin  string
	English string
}

func (c *Chat) loadVocab(ctx context.Context) ([]chatVocab, error) {
	rows, err := c.DB.QueryContext(ctx,
		`SELECT hanzi, pinyin, english FROM vocabulary ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []chatVocab{}
	for rows.Next() {
		var v chatVocab
		if err := rows.Scan(&v.Hanzi, &v.Pinyin, &v.English); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (c *Chat) loadHistory(ctx context.Context, limit int) ([]ChatMessage, error) {
	rows, err := c.DB.QueryContext(ctx,
		`SELECT id, role, content, COALESCE(pinyin, ''), COALESCE(coach_notes, ''), created_at
		   FROM (
		     SELECT id, role, content, pinyin, coach_notes, created_at
		       FROM chat_messages
		   ORDER BY id DESC
		      LIMIT ?
		   )
		ORDER BY id ASC`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatMessage{}
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.Pinyin, &m.CoachNotes, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// persistTurn writes the user message and the assistant reply in a single
// transaction so partial state never lands in chat_messages on failure.
func (c *Chat) persistTurn(ctx context.Context, userText string, turn ChatTurn) (userID, assistantID int64, err error) {
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO chat_messages (role, content) VALUES ('user', ?)`, userText)
	if err != nil {
		return
	}
	userID, err = res.LastInsertId()
	if err != nil {
		return
	}

	res, err = tx.ExecContext(ctx,
		`INSERT INTO chat_messages (role, content, pinyin, coach_notes)
		 VALUES ('assistant', ?, ?, ?)`,
		turn.Chinese, turn.Pinyin, turn.CoachNotes)
	if err != nil {
		return
	}
	assistantID, err = res.LastInsertId()
	if err != nil {
		return
	}

	err = tx.Commit()
	return
}
