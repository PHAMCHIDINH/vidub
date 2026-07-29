package translator

import (
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
- Dịch chính xác nội dung hội thoại, giữ nguyên ngữ cảnh và sắc thái.
- Giữ nguyên cấu trúc segments: MỖI segment đầu vào phải tương ứng với MỘT segment đầu ra.
- KHÔNG thay đổi thứ tự segments.
- KHÔNG thay đổi timestamp (start, duration) của segments.
- KHÔNG gộp hoặc tách segments.
- Xuất kết quả dưới dạng JSON object với key "segments" chứa mảng các segment đã dịch.

ĐỊNH DẠNG ĐẦU RA:
{
  "segments": [
    {
      "text": "bản dịch tiếng Việt",
      "original_text": "văn bản gốc tiếng Anh"
    }
  ]
}

YÊU CẦU CHẤT LƯỢNG:
- Dịch tự nhiên, không dịch word-by-word.
- Giữ nguyên tên riêng, thuật ngữ chuyên ngành (trừ khi có glossary hướng dẫn khác).
- Câu văn trôi chảy, xuôi tai trong tiếng Việt.
- KHÔNG thêm giải thích, chú thích, hay nội dung ngoài bản dịch.
- CHỈ trả về JSON object, không kèm markdown hay text khác.`, targetLang)
}

// BuildUserPrompt builds the user prompt for a single translation chunk.
// It includes:
//   - Glossary terms to use (if any)
//   - Context from previous chunks (if any)
//   - The segments to translate
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
		b.WriteString("Ngữ cảnh trước đó:\n")
		b.WriteString(chunk.Context)
		b.WriteString("\n\n")
	}

	// Text to translate
	b.WriteString("Văn bản cần dịch (các segments, cách nhau bởi dòng mới):\n")
	for _, seg := range chunk.Segments {
		line := seg.Text
		if seg.Speaker != "" {
			line = fmt.Sprintf("[%s] %s", seg.Speaker, seg.Text)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString("Trả về JSON object với key 'segments' là mảng các object chứa 'text' (bản dịch), 'original_text' (văn bản gốc). Mỗi segment đầu vào tương ứng một phần tử trong mảng segments, giữ nguyên thứ tự.")

	return b.String()
}
