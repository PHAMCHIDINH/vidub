package translator

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ParseTranslations reads an LLM response and maps each translation to its
// input position (0-based) using the "id" field, which is the 1-based
// position given in the prompt (see BuildUserPrompt).
//
// Items without a usable id are mapped by position, but only when the
// response has exactly n items. Positional mapping of a response with a
// different count would shift translations onto the wrong timestamps.
//
// Positions the model skipped, or answered with empty text, are absent from
// the result; the caller decides whether to ask again.
func ParseTranslations(response string, n int) (map[int]string, error) {
	items, err := extractItems(response)
	if err != nil {
		return nil, err
	}

	out := make(map[int]string, n)
	hasIDs := false
	for _, it := range items {
		if it.ID >= 1 && int(it.ID) <= n {
			hasIDs = true
			break
		}
	}

	if hasIDs {
		for _, it := range items {
			idx := int(it.ID) - 1
			text := strings.TrimSpace(it.Text)
			if idx < 0 || idx >= n || text == "" {
				continue // unknown id or empty answer
			}
			if _, dup := out[idx]; !dup {
				out[idx] = text
			}
		}
		return out, nil
	}

	if len(items) != n {
		return nil, fmt.Errorf("response has %d items without ids, expected %d", len(items), n)
	}
	for i, it := range items {
		if text := strings.TrimSpace(it.Text); text != "" {
			out[i] = text
		}
	}
	return out, nil
}

// responseItem is one translated segment in the model's answer.
type responseItem struct {
	ID   flexInt `json:"id"`
	Text string  `json:"text"`
}

// flexInt accepts 3 or "3". Anything else becomes 0, meaning "no id".
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	n, err := strconv.Atoi(strings.Trim(string(b), `"`))
	if err != nil {
		n = 0
	}
	*f = flexInt(n)
	return nil
}

// extractItems finds the translation list in a response. It accepts:
//   - {"segments": [{"id": 1, "text": "..."}]}   (the requested shape)
//   - [{"id": 1, "text": "..."}]
//   - [["translated", "original"], ...]
//
// and tolerates markdown fences or prose around the JSON.
func extractItems(response string) ([]responseItem, error) {
	s := strings.TrimSpace(response)
	candidates := []string{s}
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		candidates = append(candidates, s[i:j+1])
	}
	if i, j := strings.Index(s, "["), strings.LastIndex(s, "]"); i >= 0 && j > i {
		candidates = append(candidates, s[i:j+1])
	}

	for _, c := range candidates {
		if items := decodeItems(c); len(items) > 0 {
			return items, nil
		}
	}
	return nil, fmt.Errorf("no translation JSON found in response (%d bytes)", len(response))
}

func decodeItems(s string) []responseItem {
	var obj struct {
		Segments []responseItem `json:"segments"`
	}
	if json.Unmarshal([]byte(s), &obj) == nil && len(obj.Segments) > 0 {
		return obj.Segments
	}

	var arr []responseItem
	if json.Unmarshal([]byte(s), &arr) == nil && len(arr) > 0 {
		return arr
	}

	var pairs [][]string
	if json.Unmarshal([]byte(s), &pairs) == nil && len(pairs) > 0 {
		items := make([]responseItem, 0, len(pairs))
		for _, p := range pairs {
			if len(p) > 0 {
				items = append(items, responseItem{Text: p[0]})
			}
		}
		return items
	}
	return nil
}
