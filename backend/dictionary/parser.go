// Package dictionary parses CC-CEDICT entries.
//
// CC-CEDICT line format:
//
//	Traditional Simplified [pin1 yin1] /definition 1/definition 2/
//
// Lines starting with '#' are comments; blank lines are skipped.
package dictionary

import (
	"fmt"
	"strings"
)

type Entry struct {
	Simplified  string
	Traditional string
	Pinyin      string // raw numeric form from brackets, e.g. "ni3 hao3"
	PinyinFlat  string // lowercase, no digits/spaces, u: -> v, e.g. "nihao"
	English     string // definitions joined with "/"
}

// ParseLine parses one CEDICT line. Returns (nil, nil) for blank/comment
// lines that should be silently skipped. Returns an error for malformed
// non-comment lines so callers can decide whether to abort or log-and-continue.
func ParseLine(line string) (*Entry, error) {
	line = strings.TrimRight(line, "\r\n")
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return nil, nil
	}

	lb := strings.Index(line, "[")
	rb := strings.Index(line, "]")
	if lb < 0 || rb < 0 || rb < lb {
		return nil, fmt.Errorf("missing pinyin brackets: %q", line)
	}

	hanziPart := strings.TrimSpace(line[:lb])
	pinyin := strings.TrimSpace(line[lb+1 : rb])
	defsPart := strings.TrimSpace(line[rb+1:])

	hanziTokens := strings.Fields(hanziPart)
	if len(hanziTokens) < 2 {
		return nil, fmt.Errorf("expected traditional+simplified before brackets: %q", line)
	}
	trad, simp := hanziTokens[0], hanziTokens[1]

	defsPart = strings.Trim(defsPart, "/")
	if defsPart == "" {
		return nil, fmt.Errorf("no definitions: %q", line)
	}

	return &Entry{
		Simplified:  simp,
		Traditional: trad,
		Pinyin:      pinyin,
		PinyinFlat:  FlattenPinyin(pinyin),
		English:     defsPart,
	}, nil
}

// FlattenPinyin normalizes CEDICT pinyin into a compact lookup key:
// lowercase, tone digits stripped, spaces removed, middle dots removed,
// and "u:" rewritten to "v" (matches the convention Chinese IMEs use, so
// users can type "lv" to find 绿).
func FlattenPinyin(p string) string {
	runes := []rune(p)
	var b strings.Builder
	b.Grow(len(p))
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a')
		case r >= 'a' && r <= 'z':
			if r == 'u' && i+1 < len(runes) && runes[i+1] == ':' {
				b.WriteRune('v')
				i++
				continue
			}
			b.WriteRune(r)
		case r >= '0' && r <= '9':
		case r == ' ', r == '\t':
		case r == '·':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
