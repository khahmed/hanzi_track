// Command probe runs a few representative pinyin_flat lookups against the
// seeded cedict table and prints timings. Used to validate the SPEC.md
// "<10ms" autocomplete target.
package main

import (
	"fmt"
	"log"
	"time"

	"hanzitrack/backend/db"
)

func main() {
	database, err := db.Open("data/hanzitrack.db")
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	// Confirm the index is used.
	planRows, err := database.Query(
		"EXPLAIN QUERY PLAN SELECT hanzi_simplified, pinyin, english FROM cedict WHERE pinyin_flat GLOB ? LIMIT 10",
		"nihao%",
	)
	if err != nil {
		log.Fatal(err)
	}
	for planRows.Next() {
		var id, parent, notused int
		var detail string
		if err := planRows.Scan(&id, &parent, &notused, &detail); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("PLAN: %s\n", detail)
	}
	planRows.Close()
	fmt.Println()

	queries := []string{"nihao", "pengyou", "zhongguo", "xiexie", "lv", "zh", "ma"}
	// Warm-up pass.
	for _, q := range queries {
		rows, _ := database.Query("SELECT 1 FROM cedict WHERE pinyin_flat GLOB ? LIMIT 10", q+"*")
		for rows.Next() {
			var x int
			rows.Scan(&x)
		}
		rows.Close()
	}
	for _, q := range queries {
		// LIKE prefix match — the autocomplete query shape.
		start := time.Now()
		rows, err := database.Query(
			"SELECT hanzi_simplified, pinyin, english FROM cedict WHERE pinyin_flat GLOB ? LIMIT 10",
			q+"*",
		)
		if err != nil {
			log.Fatalf("query %q: %v", q, err)
		}
		var hits int
		var sampleHanzi, samplePinyin, sampleEng string
		for rows.Next() {
			var h, p, e string
			if err := rows.Scan(&h, &p, &e); err != nil {
				log.Fatal(err)
			}
			if hits == 0 {
				sampleHanzi, samplePinyin, sampleEng = h, p, e
			}
			hits++
		}
		rows.Close()
		elapsed := time.Since(start)
		fmt.Printf("%-10s %6s  %3d hits  e.g. %s (%s) — %s\n",
			q, elapsed.Round(time.Microsecond), hits, sampleHanzi, samplePinyin, truncate(sampleEng, 50))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
