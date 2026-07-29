package translator

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// parseResponse is an internal type for unmarshalling LLM responses.
type parseResponse struct {
	Segments []struct {
		Text         string `json:"text"`
		OriginalText string `json:"original_text"`
	} `json:"segments"`
}

// ParseTranslationResponse parses an LLM response string into TranslatedSegments.
// It uses a 3-strategy fallback:
//  1. Direct JSON unmarshal into a struct with Segments array.
//  2. Regex extract the top-level JSON object ({...}) from the response.
//  3. Regex extract a JSON array ([{...}]) from the response.
//
// expectedCount is the number of segments expected. If non-zero, the parsed
// segments must have at least expectedCount entries, otherwise the next
// strategy is tried.
func ParseTranslationResponse(response string, expectedCount int) ([]TranslatedSegment, error) {
	if expectedCount <= 0 {
		expectedCount = 1
	}

	// Strategy 1: Direct JSON unmarshal.
	segs, err := parseDirectJSON(response)
	if err == nil && len(segs) >= expectedCount {
		return segs, nil
	}

	// Strategy 2: Regex extract JSON object.
	segs, err = parseRegexObject(response)
	if err == nil && len(segs) >= expectedCount {
		return segs, nil
	}

	// Strategy 3: Direct JSON array unmarshal (e.g. [["translated", "original"], ...])
	segs, err = parseDirectArray(response)
	if err == nil && len(segs) >= expectedCount {
		return segs, nil
	}

	// Strategy 4: Regex extract JSON array.
	segs, err = parseRegexArray(response)
	if err == nil && len(segs) >= expectedCount {
		return segs, nil
	}

	// Attempt one more time with a relaxed approach: try strategies again
	// without count validation.
	segs, err = parseDirectJSON(response)
	if err == nil && len(segs) > 0 {
		return segs, nil
	}

	segs, err = parseDirectArray(response)
	if err == nil && len(segs) > 0 {
		return segs, nil
	}

	segs, err = parseRegexObject(response)
	if err == nil && len(segs) > 0 {
		return segs, nil
	}

	segs, err = parseRegexArray(response)
	if err == nil && len(segs) > 0 {
		return segs, nil
	}

	return nil, fmt.Errorf("unable to parse translation response: all 4 strategies failed; expected %d segments", expectedCount)
}

// parseDirectJSON attempts to unmarshal the response directly as a JSON object
// containing a "segments" array.
func parseDirectJSON(response string) ([]TranslatedSegment, error) {
	var pr parseResponse
	if err := json.Unmarshal([]byte(response), &pr); err != nil {
		return nil, fmt.Errorf("direct JSON parse failed: %w", err)
	}
	if len(pr.Segments) == 0 {
		return nil, fmt.Errorf("direct JSON parse: empty segments array")
	}

	result := make([]TranslatedSegment, len(pr.Segments))
	for i, s := range pr.Segments {
		result[i] = TranslatedSegment{
			Text:         s.Text,
			OriginalText: s.OriginalText,
		}
	}
	return result, nil
}

// jsonObjectPattern matches a top-level JSON object (non-greedy, balanced braces).
var jsonObjectPattern = regexp.MustCompile(`(?s)\{(?:[^{}]|(?:[^{}]*\{[^{}]*\}[^{}]*))*\}`)

// parseRegexObject attempts to extract a JSON object from the response using regex,
// then unmarshals it.
func parseRegexObject(response string) ([]TranslatedSegment, error) {
	matches := jsonObjectPattern.FindString(response)
	if matches == "" {
		return nil, fmt.Errorf("regex object extract: no JSON object found")
	}

	var pr parseResponse
	if err := json.Unmarshal([]byte(matches), &pr); err != nil {
		return nil, fmt.Errorf("regex object parse failed: %w", err)
	}
	if len(pr.Segments) == 0 {
		return nil, fmt.Errorf("regex object parse: empty segments array")
	}

	result := make([]TranslatedSegment, len(pr.Segments))
	for i, s := range pr.Segments {
		result[i] = TranslatedSegment{
			Text:         s.Text,
			OriginalText: s.OriginalText,
		}
	}
	return result, nil
}

// parseDirectArray attempts to unmarshal the entire response as a JSON array.
// It tries two shapes:
//  1. Array of {"text": "...", "original_text": "..."}
//  2. Array of [["translated", "original"], ...]
func parseDirectArray(response string) ([]TranslatedSegment, error) {
	// Try shape 1: array of objects with text/original_text fields.
	var objArray []struct {
		Text         string `json:"text"`
		OriginalText string `json:"original_text"`
	}
	if err := json.Unmarshal([]byte(response), &objArray); err == nil && len(objArray) > 0 {
		result := make([]TranslatedSegment, len(objArray))
		for i, s := range objArray {
			result[i] = TranslatedSegment{
				Text:         s.Text,
				OriginalText: s.OriginalText,
			}
		}
		return result, nil
	}

	// Try shape 2: array of [translated, original] pairs.
	var strPairs [][]string
	if err := json.Unmarshal([]byte(response), &strPairs); err == nil && len(strPairs) > 0 {
		result := make([]TranslatedSegment, len(strPairs))
		for i, pair := range strPairs {
			translated := ""
			original := ""
			if len(pair) > 0 {
				translated = pair[0]
			}
			if len(pair) > 1 {
				original = pair[1]
			}
			result[i] = TranslatedSegment{
				Text:         translated,
				OriginalText: original,
			}
		}
		return result, nil
	}

	return nil, fmt.Errorf("direct array parse: unable to parse as array")
}

// jsonArrayPattern matches a JSON array containing objects or arrays.
var jsonArrayPattern = regexp.MustCompile(`(?s)\[\s*[\[{].*?[\]}]\s*\]`)

// parseRegexArray attempts to extract a JSON array from the response using regex,
// then unmarshals it into the segments format. It tries two shapes:
//  1. Array of {"text": "...", "original_text": "..."}
//  2. Array of [["translated", "original"], ...]
func parseRegexArray(response string) ([]TranslatedSegment, error) {
	matches := jsonArrayPattern.FindString(response)
	if matches == "" {
		return nil, fmt.Errorf("regex array extract: no JSON array found")
	}

	// Try shape 1: array of objects with text/original_text fields.
	var objArray []struct {
		Text         string `json:"text"`
		OriginalText string `json:"original_text"`
	}
	if err := json.Unmarshal([]byte(matches), &objArray); err == nil && len(objArray) > 0 {
		result := make([]TranslatedSegment, len(objArray))
		for i, s := range objArray {
			result[i] = TranslatedSegment{
				Text:         s.Text,
				OriginalText: s.OriginalText,
			}
		}
		return result, nil
	}

	// Try shape 2: array of [translated, original] pairs.
	var strPairs [][]string
	if err := json.Unmarshal([]byte(matches), &strPairs); err == nil && len(strPairs) > 0 {
		result := make([]TranslatedSegment, len(strPairs))
		for i, pair := range strPairs {
			original := ""
			translated := ""
			if len(pair) > 0 {
				translated = pair[0]
			}
			if len(pair) > 1 {
				original = pair[1]
			}
			result[i] = TranslatedSegment{
				Text:         translated,
				OriginalText: original,
			}
		}
		return result, nil
	}

	return nil, fmt.Errorf("regex array parse: unable to parse array content")
}
