# vidub — Lồng tiếng video YouTube, không cần GPU

Dự án lồng tiếng video YouTube tự động sang tiếng Việt. Zero GPU, zero database, zero queue — chỉ cần 1 Go binary + Python scripts.

**Pipeline:** Extract transcript → Translate (DeepSeek) → TTS (Edge TTS) → Download video → Dub (FFmpeg)

![demo](images/screenshot.png)

## Yêu cầu hệ thống

| Thành phần | Bắt buộc | Ghi chú |
|---|---|---|
| Go 1.22+ | ✅ | Build server |
| Python 3.12+ | ✅ | Chạy scripts extract + TTS |
| FFmpeg | ✅ | Xử lý audio/video |
| yt-dlp | ✅ | Tải video YouTube |
| DeepSeek API key | ✅ | Dịch transcript (đăng ký miễn phí tại [platform.deepseek.com](https://platform.deepseek.com)) |

## Cài đặt

```bash
git clone https://github.com/your-username/vidub.git
cd vidub

# Python dependencies
pip install -r requirements.txt

# Go build
go build -o vidub .

# Kiểm tra tool system
which ffmpeg yt-dlp python3
```

## Chạy

```bash
# Optional: set API key và voice default
export DEEPSEEK_API_KEY=sk-your-key-here
export TTS_VOICE=vi-VN-HoaiMyNeural

# Start server
./vidub
```

Truy cập **http://localhost:8080**, dán YouTube URL, chọn giọng đọc, nhập API key (nếu chưa set env), bấm **Start Dubbing**.

### Cấu hình qua biến môi trường

| Biến | Default | Mô tả |
|---|---|---|
| `PORT` | `8080` | HTTP port |
| `STORAGE_DIR` | `./data` | Thư mục lưu artifacts |
| `DEEPSEEK_API_KEY` | (trống) | DeepSeek API key |
| `DEEPSEEK_MODEL` | `deepseek-v4-flash` | Model dịch |
| `TTS_VOICE` | `vi-VN-HoaiMyNeural` | Giọng đọc mặc định |
| `AUDIO_BITRATE` | `128k` | Bitrate audio output |

Tất cả config đều có thể override trực tiếp trên giao diện web khi tạo job.

## Pipeline

```
┌─────────┐    ┌───────────┐    ┌─────┐    ┌──────────┐    ┌─────┐
│ extract │───▶│ translate │───▶│ tts │───▶│ download │───▶│ dub │
└─────────┘    └───────────┘    └─────┘    └──────────┘    └─────┘
 Python          Go (DeepSeek)   Python      yt-dlp        FFmpeg
 (yt API)                       (Edge TTS)
```

| Step | Công nghệ | Output |
|---|---|---|
| **extract** | `youtube_transcript_api` (Python) | `transcript.json` |
| **translate** | DeepSeek V4 Flash API (Go, 5 workers) | `translated.json` |
| **tts** | Edge TTS free (Python, 2 modes) | `narrative.mp3` + `dub.mp3` |
| **download** | yt-dlp (shell) | `{video_id}.mp4` |
| **dub** | FFmpeg (voiceover + replace) | `voiceover.mp4` + `dub.mp4` |

### 2 chế độ lồng tiếng

| | Voiceover | Replace |
|---|---|---|
| Audio gốc | Giữ 15% | Bỏ hoàn toàn |
| TTS audio | Narrative (đọc liên tục) | Dubbing (sync timestamp) |
| Output | `voiceover.mp4` | `dub.mp4` |

### File-based checkpoint

Pipeline dùng file system làm state — file nào đã tồn tại thì skip step đó. Nếu job bị ngắt giữa chừng, chạy lại cùng video ID sẽ tiếp tục từ step chưa hoàn thành.

```
data/{video_id}/
├── transcript.json       ← extract
├── translated.json       ← translate
├── narrative.mp3         ← TTS mode B
├── dub.mp3               ← TTS mode A
├── {video_id}.mp4        ← download
├── voiceover.mp4         ← dub (voiceover)
└── dub.mp4               ← dub (replace)
```

## Kiến trúc

```
Browser ──POST /jobs──▶ Go Server (Fiber, :8080)
  ▲ SSE stream           │
  │ (htmx hx-ext="sse")  ├── scripts/extract.py
  │                      ├── internal/translator/ (DeepSeek)
  │                      ├── scripts/tts.py
  │                      ├── yt-dlp
  │                      └── internal/media/ (FFmpeg)
```

- **Go Fiber** — HTTP server + HTML templates
- **htmx + SSE** — frontend không cần JavaScript, progress real-time
- **Python subprocess** — extract transcript + TTS (không có Go library tương đương)
- **3 package reuse từ go-backend** — `translator/`, `tts/`, `media/` copy nguyên không sửa

### Cấu trúc thư mục

```
vidub/
├── main.go                        # Entry point, routes, server lifecycle
├── internal/
│   ├── config/config.go           # Load config từ env
│   ├── sse/broker.go              # SSE pub/sub broker
│   ├── pipeline/
│   │   ├── pipeline.go            # Orchestrator, step dispatch
│   │   ├── helpers.go             # SSE HTML builders, file helpers
│   │   ├── step_extract.go        # Subprocess: extract.py
│   │   ├── step_translate.go      # Go: DeepSeek translation
│   │   ├── step_tts.go            # Subprocess: tts.py
│   │   ├── step_download.go       # Subprocess: yt-dlp
│   │   └── step_dub.go            # Go: FFmpeg mix via media.DubMixer
│   ├── translator/                # Copy từ go-backend (DeepSeek, chunker, parser)
│   ├── tts/                       # Copy từ go-backend (Edge TTS, audio utils)
│   └── media/                     # Copy từ go-backend (DubMixer, voiceover/replace)
├── scripts/
│   ├── extract.py                 # youtube_transcript_api → JSON
│   └── tts.py                     # edge-tts → narrative.mp3 + dub.mp3
├── templates/
│   ├── base.html                  # Layout shell + CSS + htmx CDN
│   ├── index.html                 # Job creation form
│   └── job_progress.html          # SSE-powered progress page
├── data/                          # Runtime artifacts
├── requirements.txt               # Python dependencies
├── go.mod
└── README.md
```

## Giọng đọc

5 giọng neural tiếng Việt từ Microsoft Edge TTS (miễn phí):

| Voice ID | Giới tính | Vùng miền |
|---|---|---|
| `vi-VN-HoaiMyNeural` | Nữ | Miền Nam |
| `vi-VN-NamMinhNeural` | Nam | Miền Nam |
| `vi-VN-HongAnNeural` | Nữ | Miền Bắc |
| `vi-VN-AnhDungNeural` | Nam | Miền Bắc |
| `vi-VN-LinhSanNeural` | Nữ | Miền Trung |

## Chi phí

| Thành phần | Chi phí |
|---|---|
| DeepSeek V4 Flash | ~$0.001-0.005/video (487 từ) |
| DeepSeek V4 Pro | ~$0.003-0.01/video |
| Edge TTS | Miễn phí |
| yt-dlp + FFmpeg | Miễn phí |

## So sánh với hệ thống Go backend

| | vidub (đơn giản) | Go backend (đầy đủ) |
|---|---|---|
| GPU | Không | Cần (Demucs, WhisperX, CosyVoice) |
| Database | Không (file system) | PostgreSQL |
| Queue | Không | Asynq + Redis |
| Audio separation | Không (voiceover 15%) | Demucs → BGM preservation |
| Transcript | youtube_transcript_api | YouTube API + WhisperX fallback |
| TTS | Edge TTS | Edge TTS + CosyVoice voice cloning |
| Voice cloning | Không | Có |
| Pause/confirm | Không | Có |
| RAG glossary | Không | Có |
| Frontend | htmx (server-rendered) | React + Ant Design |
| Deploy | 1 binary + Python venv | Docker Compose (3 services) |
