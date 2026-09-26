package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// BuildSystemPrompt builds the Vietnamese system prompt for the translator LLM.
// It instructs the model on how to perform the translation task.
func BuildSystemPrompt(targetLang string) string {
	if targetLang == "" {
		targetLang = "vi-VN"
	}

	return fmt.Sprintf(`Bạn là dịch giả chuyên nghiệp chuyên dịch phụ đề video từ ngôn ngữ nguồn sang %s.

NHIỆM VỤ:
- Đầu vào là một mảng JSON các segment, mỗi segment có "id" và "text" (có thể có "speaker" và "max_chars").
- Dịch "text" của TỪNG segment. Trả về ĐÚNG một phần tử cho MỖI id đầu vào và giữ nguyên id đó.
- KHÔNG gộp, tách, bỏ sót hay thêm segment. Nếu một câu bị ngắt qua nhiều segment, hãy dịch từng phần sao cho khi đọc liền nhau vẫn tự nhiên.
- Xuất kết quả dưới dạng JSON object với key "segments" chứa mảng các segment đã dịch.

ĐỊNH DẠNG ĐẦU RA:
{
  "segments": [
    {"id": 1, "text": "bản dịch tiếng Việt của segment 1"},
    {"id": 2, "text": "bản dịch tiếng Việt của segment 2"}
  ]
}

ĐỘ DÀI (bản dịch được đọc lồng tiếng đúng thời lượng của câu gốc):
- "max_chars" là số ký tự tối đa đọc kịp trong thời lượng của segment đó. Bản dịch KHÔNG được dài hơn "max_chars".
- Để vừa độ dài: dùng câu ngắn gọn, bỏ từ đệm, từ nối và từ lặp không cần thiết, chọn từ ngắn hơn. Giữ đủ ý chính, số liệu và tên riêng.
- Tiếng Việt thường dài hơn tiếng Anh, nên hãy dịch súc tích thay vì dịch sát từng chữ.

YÊU CẦU CHẤT LƯỢNG:
- Dịch tự nhiên, không dịch word-by-word.
- Giữ nguyên tên riêng, thuật ngữ chuyên ngành (trừ khi có glossary hướng dẫn khác).
- Câu văn trôi chảy, xuôi tai trong tiếng Việt.
- KHÔNG thêm giải thích, chú thích, hay nội dung ngoài bản dịch.
- CHỈ trả về JSON object, không kèm markdown hay text khác.`, targetLang)
}

// promptSegment is one input line as the model sees it. IDs are 1-based
// positions inside the chunk, and the response is mapped back by them.
type promptSegment struct {
	ID       int    `json:"id"`
	Text     string `json:"text"`
	Speaker  string `json:"speaker,omitempty"`
	MaxChars int    `json:"max_chars,omitempty"`
}

// BuildUserPrompt builds the user prompt for a single translation chunk.
// It includes:
//   - Glossary terms to use (if any)
//   - Context from previous chunks (if any)
//   - The segments to translate, as a JSON array with ids
//
// Segments are sent as JSON rather than one per line: caption text often
// contains its own line breaks, which made the model merge or split lines.
func BuildUserPrompt(chunk Chunk, glossary []GlossaryTerm, contextLines int) string {
	var b strings.Builder

	// Glossary terms section
	if len(glossary) > 0 {
		b.WriteString("Các thuật ngữ cần tuân theo:\n")
		for _, term := range glossary {
			b.WriteString(fmt.Sprintf("- %s → %s\n", term.SourceTerm, term.TargetTerm))
		}
		b.WriteString("\n")
	}

	// Context window section
	if chunk.Context != "" && contextLines > 0 {
		b.WriteString("Ngữ cảnh trước đó (chỉ để tham khảo, KHÔNG dịch):\n")
		b.WriteString(chunk.Context)
		b.WriteString("\n\n")
	}

	items := make([]promptSegment, len(chunk.Segments))
	for i, seg := range chunk.Segments {
		items[i] = promptSegment{ID: i + 1, Text: seg.Text, Speaker: seg.Speaker, MaxChars: seg.MaxChars}
	}
	var js bytes.Buffer
	enc := json.NewEncoder(&js)
	enc.SetEscapeHTML(false) // keep <, > and & readable for the model
	enc.SetIndent("", "  ")
	_ = enc.Encode(items) // cannot fail: plain strings and ints

	b.WriteString("Các segment cần dịch (JSON):\n")
	b.Write(js.Bytes())
	b.WriteString(fmt.Sprintf(
		"\nTrả về JSON object {\"segments\": [...]} gồm đúng %d phần tử, mỗi phần tử có \"id\" (từ 1 đến %d) và \"text\" (bản dịch, không dài hơn \"max_chars\" nếu có).",
		len(items), len(items),
	))

	return b.String()
}
