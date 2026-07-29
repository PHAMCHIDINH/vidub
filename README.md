# vidub — Lồng tiếng video YouTube, không cần GPU

Dự án lồng tiếng video YouTube tự động sang tiếng Việt. Zero GPU, zero database, zero queue — chỉ cần 1 Go binary + Python scripts.

**Pipeline:** Extract ∥ Download → Translate → TTS (narrative ∥ dub) → Dub (voiceover ∥ replacement) → Upload (optional)

![demo](images/screenshot.png)

## Tính năng

- **Dịch tự động** — Google Translate (free, mặc định) hoặc DeepSeek API (chất lượng cao)
- **2 chế độ lồng tiếng** — Voiceover (giữ audio gốc 15%) hoặc Replace (chỉ TTS)
- **Phụ đề hardcode** — Burn subtitle SRT tiếng Việt trực tiếp vào video
- **Real-time dashboard** — Timeline log từng bước với timestamp + duration
- **Concurrency** — Download∥Extract, narrative∥dub TTS, voiceover∥replacement mix
- **Google Drive upload** — Tự động upload output lên Drive (optional)
- **File-based checkpoint** — Job bị ngắt có thể resume từ step chưa xong

## Yêu cầu hệ thống

| Thành phần | Bắt buộc | Ghi chú |
|---|---|---|
| Go 1.22+ | ✅ | Build server |
| Python 3.12+ | ✅ | Chạy scripts extract + TTS |
| FFmpeg (libopenh264) | ✅ | Xử lý audio/video |
| yt-dlp | ✅ | Tải video YouTube |
| gcc-c++ | ✅ | Build numpy từ source (Python 3.14) |

**Lưu ý Fedora:** FFmpeg mặc định không có `libx264`, vidub dùng `libopenh264` thay thế.

## Cài đặt

```bash
git clone https://github.com/your-username/vidub.git
cd vidub

# Python dependencies
pip install -r requirements.txt

# Go build
go build -o vidub .

# Kiểm tra tool system
which ffmpeg ffprobe yt-dlp python3
```

## Chạy

```bash
# Start server (Google Translate mặc định, không cần API key)
./vidub

# Hoặc với DeepSeek API (chất lượng dịch cao hơn)
export DEEPSEEK_API_KEY=sk-your-key-here
./vidub
```

Truy cập **http://localhost:8080**, dán YouTube URL, chọn chế độ mix, bấm **Start Dubbing**.

### Google Drive upload (optional)

```bash
export GOOGLE_DRIVE_ENABLED=true
export GOOGLE_DRIVE_CREDENTIALS=/path/to/service-account.json
export GOOGLE_DRIVE_FOLDER=vidub
./vidub
```

Cần tạo Service Account trên [Google Cloud Console](https://console.cloud.google.com) và download JSON key.

## Cấu hình

| Biến | Default | Mô tả |
|---|---|---|
| `PORT` | `8080` | HTTP port |
| `STORAGE_DIR` | `./data` | Thư mục lưu artifacts |
| `DEEPSEEK_API_KEY` | (trống) | DeepSeek API key (để trống → dùng Google Translate) |
| `DEEPSEEK_MODEL` | `deepseek-v4-flash` | Model dịch |
| `TTS_VOICE` | `vi-VN-HoaiMyNeural` | Giọng đọc mặc định |
| `GOOGLE_DRIVE_ENABLED` | `false` | Bật upload Google Drive |
| `GOOGLE_DRIVE_CREDENTIALS` | `.commandcode/gdrive-credentials.json` | Path đến Service Account JSON |
| `GOOGLE_DRIVE_FOLDER` | `vidub` | Tên folder gốc trên Drive |

## Pipeline

```
┌──────────────────────────────────────────────────────────────┐
│                    CONCURRENT STEP 1                         │
│  ┌─────────┐              ┌──────────┐                       │
│  │ extract │ ───────────▶ │ download │  (song song, I/O)     │
│  └─────────┘              └──────────┘                       │
└──────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌──────────────────────────────────────────────────────────────┐
│                      STEP 2: translate                       │
│  Google Translate RPC (free) hoặc DeepSeek API               │
│  Chunking ≤12s + 5 workers song song                         │
└──────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌──────────────────────────────────────────────────────────────┐
│                CONCURRENT STEP 3: tts                        │
│  ┌──────────────┐        ┌──────────────┐                    │
│  │ narrative.mp3│        │  dub.mp3     │  (2 process Python)│
│  │ (đọc liên tục)│       │ (sync timestamp)│                  │
│  └──────────────┘        └──────────────┘                    │
└──────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌──────────────────────────────────────────────────────────────┐
│            CONCURRENT STEP 4: dub + subtitle                 │
│  ┌──────────────┐        ┌──────────────┐                    │
│  │ voiceover.mp4│        │  dub.mp4     │  (2 ffmpeg song song)│
│  │ + subtitle   │        │ + subtitle   │                     │
│  └──────────────┘        └──────────────┘                    │
└──────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌──────────────────────────────────────────────────────────────┐
│              STEP 5: upload (optional)                       │
│  Google Drive — voiceover.mp4, dub.mp4, subtitle.srt         │
└──────────────────────────────────────────────────────────────┘
```

| Step | Công nghệ | Output | Concurrency |
|---|---|---|---|
| **extract** | `youtube_transcript_api` (Python) | `transcript.json` | ∥ download |
| **download** | yt-dlp | `{video_id}.mp4` | ∥ extract |
| **translate** | Google Translate / DeepSeek (5 workers) | `translated.json` | chunk song song |
| **tts** | Edge TTS (Python, 10 workers) | `narrative.mp3` + `dub.mp3` | narrative ∥ dub |
| **dub** | FFmpeg libopenh264 | `voiceover.mp4` + `dub.mp4` + `subtitle.srt` | voiceover ∥ replacement |
| **upload** | Google Drive API | Shareable links | 3 files song song |

### 2 chế độ lồng tiếng

| | Voiceover | Replace |
|---|---|---|
| Audio gốc | Giữ 15% | Bỏ hoàn toàn |
| TTS audio | Narrative (đọc liên tục) | Dubbing (sync timestamp) |
| Phụ đề | Hardcode vào video | Hardcode vào video |
| Output | `voiceover.mp4` | `dub.mp4` |

### Audio alignment

TTS audio thường dài hơn video gốc (tiếng Việt đọc chậm hơn). Vidub tự động:
- **Tier 1:** FFmpeg `atempo` — tăng tốc tối đa +25% (giữ chất lượng)
- **Tier 2:** FFmpeg `rubberband` — phase vocoder thêm +15% (giữ pitch)
- **Fallback:** Trim + fade-out nếu vẫn quá dài
- **amix duration=shortest** — ép output đúng video duration

### File-based checkpoint

Pipeline dùng file system làm state — file nào đã tồn tại thì skip step đó. Nếu job bị ngắt giữa chừng, chạy lại cùng video ID sẽ tiếp tục từ step chưa hoàn thành.

```
data/{video_id}/
├── transcript.json       ← extract
├── translated.json       ← translate
├── narrative.mp3         ← TTS mode B
├── narrative_aligned.mp3 ← align (nếu cần)
├── dub.mp3               ← TTS mode A
├── {video_id}.mp4        ← download
├── subtitle.srt          ← generate từ translated
├── voiceover.mp4         ← dub (voiceover) + subtitle
└── dub.mp4               ← dub (replace) + subtitle
```

## Kiến trúc

```
Browser ──POST /jobs──▶ Go Server (Fiber, :8080)
  ▲ SSE stream           │
  │ (htmx hx-ext="sse")  ├── scripts/extract.py      (extract)
  │                      ├── internal/translator/      (translate)
  │                      ├── scripts/tts.py           (TTS)
  │                      ├── yt-dlp subprocess         (download)
  │                      ├── internal/media/           (FFmpeg dub)
  │                      └── internal/storage/         (Google Drive)
```

- **Go Fiber** — HTTP server + HTML templates
- **htmx + SSE** — Dashboard real-time, timeline log từng bước
- **Python subprocess** — Extract transcript + TTS (không có Go library tương đương)
- **File system** — State management, không cần database

### Cấu trúc thư mục

```
vidub/
├── main.go                        # Entry point, routes, server lifecycle
├── internal/
│   ├── config/config.go           # Load config từ env
│   ├── sse/broker.go              # SSE pub/sub broker
│   ├── pipeline/
│   │   ├── pipeline.go            # Orchestrator, concurrency, timing
│   │   ├── helpers.go             # SSE emit, step_log JSON, progress
│   │   ├── step_extract.go        # Subprocess: extract.py
│   │   ├── step_translate.go      # Google/DeepSeek translation
│   │   ├── step_tts.go            # Subprocess: tts.py (narrative ∥ dub)
│   │   ├── step_download.go       # Subprocess: yt-dlp
│   │   └── step_dub.go            # FFmpeg mix (voiceover ∥ replacement)
│   ├── translator/                # Google + DeepSeek providers, chunker, parser
│   ├── tts/                       # Edge TTS client, audio alignment (atempo/rubberband)
│   ├── media/                     # DubMixer (FFmpeg), subtitle SRT generator
│   └── storage/                   # Google Drive client
├── scripts/
│   ├── extract.py                 # youtube_transcript_api → JSON
│   └── tts.py                     # edge-tts → narrative.mp3 + dub.mp3
├── templates/
│   ├── partials/header.html       # Dashboard layout, sidebar, CSS, JS
│   ├── partials/footer.html       # Close layout
│   ├── index.html                 # Job form + pipeline view
│   └── job_progress.html          # SSE timeline fragment
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
| Google Translate | Miễn phí |
| DeepSeek V4 Flash | ~$0.001-0.005/video |
| DeepSeek V4 Pro | ~$0.003-0.01/video |
| Edge TTS | Miễn phí |
| yt-dlp + FFmpeg | Miễn phí |
| Google Drive API | Miễn phí (15GB storage) |

## API

| Endpoint | Method | Mô tả |
|---|---|---|
| `/` | GET | Dashboard — form + timeline |
| `/jobs` | POST | Tạo job mới (form submit) |
| `/sse/jobs/:id` | GET | SSE stream — real-time log |
| `/jobs/:id/files/:filename` | GET | Download artifact |

## License

MIT
