package translator

import (
	"strings"
)

// DefaultTargetWords is the target number of words per chunk.
const DefaultTargetWords = 200

// DefaultMaxWords is the hard maximum number of words per chunk.
const DefaultMaxWords = 400

// DefaultMaxDurationSecs is the max accumulated duration per chunk in seconds.
// Chunks longer than this are force-split to keep them small for parallel translation.
const DefaultMaxDurationSecs = 12.0

// DefaultContextLines is the default number of preceding lines to include as context.
const DefaultContextLines = 30

// sentenceBoundaries contains characters that mark sentence boundaries.
var sentenceBoundaries = []rune{'.', '?', '!', ';'}

// ChunkTranscript splits transcript segments into chunks at sentence boundaries.
//
// Strategy:
//  1. Walk segments in order, accumulating words.
//  2. When accumulated words >= targetWords, look for a sentence boundary
//     within the current chunk. If found, split there.
//  3. If accumulated words >= maxWords, force-split at the last sentence boundary
//     or at the current segment boundary.
//  4. Each chunk includes context from preceding lines (set via BuildContextWindow).
//
// The function is deterministic: same input always produces the same chunks.
func ChunkTranscript(segments []Segment, targetWords int, maxWords int, maxDurationSecs float64) []Chunk {
	if len(segments) == 0 {
		return nil
	}

	if targetWords <= 0 {
		targetWords = DefaultTargetWords
	}
	if maxWords <= 0 {
		maxWords = DefaultMaxWords
	}
	if maxDurationSecs <= 0 {
		maxDurationSecs = DefaultMaxDurationSecs
	}
	if targetWords > maxWords {
		targetWords = maxWords
	}

	var chunks []Chunk
	var currentSegments []Segment
	currentWordCount := 0
	currentDuration := 0.0
	lastSplitIndex := 0 // index in segments where the last chunk ended

	for i, seg := range segments {
		wordCount := countWords(seg.Text)
		currentSegments = append(currentSegments, seg)
		currentWordCount += wordCount
		currentDuration += seg.Duration

		shouldSplit := false
		splitIndex := -1

		if currentWordCount >= targetWords && currentDuration >= maxDurationSecs*0.5 {
			splitIndex = findSentenceBoundarySegment(currentSegments, 0)
			if splitIndex >= 0 {
				shouldSplit = true
			}
		}

		if currentWordCount >= maxWords || currentDuration >= maxDurationSecs {
			splitIndex = findSentenceBoundarySegment(currentSegments, 0)
			if splitIndex < 0 {
				splitIndex = len(currentSegments) - 1
				if splitIndex < 1 {
					splitIndex = len(currentSegments)
				}
			}
			shouldSplit = true
		}

		// Also split if this is the last segment and we have accumulated content.
		if !shouldSplit && i == len(segments)-1 && len(currentSegments) > 0 {
			splitIndex = len(currentSegments)
			shouldSplit = true
		}

		if shouldSplit && splitIndex >= 0 {
			// Build the chunk from currentSegments[0:splitIndex].
			chunkSegs := currentSegments[:splitIndex]
			if len(chunkSegs) > 0 {
				chunks = append(chunks, Chunk{Segments: chunkSegs})
				lastSplitIndex += len(chunkSegs)
			}

			// Keep the remaining segments (after the split) for the next chunk.
			if splitIndex < len(currentSegments) {
				currentSegments = currentSegments[splitIndex:]
				currentWordCount = sumWords(currentSegments)
				currentDuration = sumDurations(currentSegments)
			} else {
				currentSegments = nil
				currentWordCount = 0
				currentDuration = 0
			}
		}
	}

	// Flush any remaining segments not yet assigned to a chunk.
	if len(currentSegments) > 0 {
		chunks = append(chunks, Chunk{Segments: currentSegments})
	}

	// Empty chunk guard: if no chunks were created (possible with empty segments),
	// create one chunk containing all segments.
	if len(chunks) == 0 && len(segments) > 0 {
		chunks = append(chunks, Chunk{Segments: segments})
	}

	return chunks
}

// BuildContextWindow builds the context string for the chunk at chunkIndex.
// It extracts the last contextLines segments from all chunks before chunkIndex.
// The segments are returned as a single string, one segment per line.
func BuildContextWindow(chunks []Chunk, chunkIndex int, contextLines int) string {
	if chunkIndex <= 0 || len(chunks) == 0 {
		return ""
	}

	if contextLines <= 0 {
		contextLines = DefaultContextLines
	}

	// Collect all segments from previous chunks in reverse order.
	var previousSegments []Segment
	for i := chunkIndex - 1; i >= 0; i-- {
		chunk := chunks[i]
		for j := len(chunk.Segments) - 1; j >= 0; j-- {
			previousSegments = append(previousSegments, chunk.Segments[j])
			if len(previousSegments) >= contextLines {
				break
			}
		}
		if len(previousSegments) >= contextLines {
			break
		}
	}

	// We collected in reverse; reverse back to chronological order.
	for i, j := 0, len(previousSegments)-1; i < j; i, j = i+1, j-1 {
		previousSegments[i], previousSegments[j] = previousSegments[j], previousSegments[i]
	}

	var b strings.Builder
	for i, seg := range previousSegments {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(seg.Text)
	}
	return b.String()
}

// countWords returns the number of words in a text string.
// Words are defined as sequences of non-whitespace characters.
func countWords(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	fields := strings.Fields(text)
	return len(fields)
}

// sumWords sums the word counts across all given segments.
func sumWords(segs []Segment) int {
	total := 0
	for _, s := range segs {
		total += countWords(s.Text)
	}
	return total
}

func sumDurations(segs []Segment) float64 {
	total := 0.0
	for _, s := range segs {
		total += s.Duration
	}
	return total
}

// hasSentenceBoundary returns true if text ends with a sentence boundary character.
func hasSentenceBoundary(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lastRune := []rune(trimmed)[len([]rune(trimmed))-1]
	for _, b := range sentenceBoundaries {
		if lastRune == b {
			return true
		}
	}
	return false
}

// findSentenceBoundarySegment finds the index of the last segment in segs
// (from startIdx onwards) that ends with a sentence boundary.
// Returns -1 if no boundary is found.
// The split point is startIdx+1 (after the boundary segment) so the output
// forms a complete sentence.
func findSentenceBoundarySegment(segs []Segment, startIdx int) int {
	// Search from end to find the last sentence boundary.
	for i := len(segs) - 1; i >= startIdx; i-- {
		if hasSentenceBoundary(segs[i].Text) {
			return i + 1 // split AFTER this segment
		}
	}
	return -1
}
