package sentences

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// TatoebaURL is the public Tatoeba search endpoint. Mandarin sources, English
// targets. Documented at tatoeba.org's API page; the same URL the web UI uses.
const TatoebaURL = "https://tatoeba.org/en/api_v0/search"

// Tatoeba fetches example sentences from tatoeba.org. The zero value is
// usable; supply HTTPClient to override the default 5s-timeout client.
type Tatoeba struct {
	HTTPClient *http.Client
	MaxPairs   int // defaults to 5
}

func (t *Tatoeba) Fetch(ctx context.Context, hanzi string) ([]Pair, error) {
	client := t.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	max := t.MaxPairs
	if max <= 0 {
		max = 5
	}

	q := url.Values{}
	q.Set("from", "cmn")
	q.Set("to", "eng")
	q.Set("query", hanzi)
	q.Set("sort", "relevance")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, TatoebaURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hanzitrack/0.1")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tatoeba GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tatoeba HTTP %d", resp.StatusCode)
	}

	// Tatoeba's translations field is a nested array: outer is direct vs.
	// indirect translations, inner is the list. We grab any English text we find.
	var body struct {
		Results []struct {
			Text         string            `json:"text"`
			Translations [][]tatoebaSentence `json:"translations"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("tatoeba decode: %w", err)
	}

	pairs := make([]Pair, 0, max)
	for _, r := range body.Results {
		if r.Text == "" {
			continue
		}
		eng := firstEnglish(r.Translations)
		if eng == "" {
			continue
		}
		pairs = append(pairs, Pair{Chinese: r.Text, English: eng})
		if len(pairs) >= max {
			break
		}
	}
	return pairs, nil
}

type tatoebaSentence struct {
	Lang string `json:"lang"`
	Text string `json:"text"`
}

func firstEnglish(groups [][]tatoebaSentence) string {
	for _, g := range groups {
		for _, s := range g {
			if s.Lang == "eng" && s.Text != "" {
				return s.Text
			}
		}
	}
	return ""
}
