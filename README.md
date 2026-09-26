# vidub — Lồng tiếng video YouTube, không cần GPU

Dự án lồng tiếng video YouTube tự động sang tiếng Việt. Zero GPU, zero database, zero queue — chỉ cần 1 Go binary + Python scripts.

**Pipeline:** Extract ∥ Download → Translate → TTS → Dub (theo chế độ Voiceover hoặc Replace)

![demo](images/screenshot.png)

## Tính năng

- **Dịch tự động** — Google Translate (free, mặc định) hoặc DeepSeek API (chất lượng cao)
- **2 chế độ lồng tiếng** — Voiceover (giữ audio gốc 15%) hoặc Replace (chỉ TTS)
- **Phụ đề hardcode** — Burn subtitle SRT tiếng Việt trực tiếp vào video
- **Real-time dashboard** — Timeline log từng bước với timestamp + duration
- **Concurrency** — Download ∥ Extract, dịch song song theo chunk, TTS song song theo segment
- **File-based checkpoint** — Job bị ngắt có thể resume từ step chưa xong

## Yêu cầu hệ thống

| Thành phần | Bắt buộc | Ghi chú |
|---|---|---|
| Go 1.25+ | ✅ | Build server (theo `go.mod`) |
| Python 3.12+ | ✅ | Chạy scripts extract + TTS |
| FFmpeg (libx264 hoặc libopenh264) | ✅ | Xử lý audio/video. Cần librubberband cho bước căn chỉnh narrative |
| yt-dlp | ✅ | Tải video YouTube |
| gcc-c++ | ✅ | Build numpy từ source (Python 3.14) |

**Lưu ý Fedora:** FFmpeg mặc định không có `libx264`. Vidub tự phát hiện và dùng `libopenh264` với bitrate cố định 2.5 Mb/s thay thế.

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

### Quản lý dữ liệu

- Dữ liệu job được lưu trong `STORAGE_DIR` (mặc định `./data/`), mỗi job một thư mục đặt tên theo video ID. Thông tin job nằm trong `job.json`, nên lịch sử ở sidebar được giữ lại sau khi restart server. Job đang chạy lúc tắt server sẽ hiện là failed.
- **Auto-cleanup**: các job không chạy lại trong 24h sẽ tự động bị xoá. Cấu hình qua biến `CLEANUP_MAX_AGE` (vd: `48h`, `90m`); giá trị sai sẽ khiến server không khởi động. Cleanup chỉ đụng tới các thư mục có tên là video ID và bỏ qua job đang chạy.
- **Chạy trùng**: gửi lại một video đang chạy sẽ mở job đó thay vì chạy song song.
- API: `DELETE /api/jobs/:id` để xoá job từ script. Trả về `409` nếu job đang chạy.

## Cấu hình

| Biến | Default | Mô tả |
|---|---|---|
| `HOST` | `127.0.0.1` | Địa chỉ lắng nghe. Mặc định chỉ máy này truy cập được |
| `PORT` | `8080` | HTTP port |
| `AUTH_PASSWORD` | (trống) | Bật HTTP basic auth khi được đặt |
| `AUTH_USER` | `admin` | Tên đăng nhập basic auth |
| `STORAGE_DIR` | `./data` | Thư mục lưu artifacts |
| `CLEANUP_MAX_AGE` | `24h` | Tự động xoá job quá tuổi |
| `DEEPSEEK_API_KEY` | (trống) | DeepSeek API key (để trống → dùng Google Translate) |
| `DEEPSEEK_MODEL` | `deepseek-v4-flash` | Model dịch |
| `TTS_VOICE` | `vi-VN-HoaiMyNeural` | Giọng đọc mặc định |

### Truy cập từ máy khác

Mặc định server chỉ nhận kết nối từ chính máy đang chạy. Để dùng trong mạng LAN hoặc trong container, hãy đặt cả hai biến dưới đây. Nếu không có mật khẩu, bất kỳ ai vào được port này đều có thể chạy job bằng DeepSeek key của server và xoá dữ liệu.

```bash
HOST=0.0.0.0 AUTH_PASSWORD=mot-mat-khau-dai ./vidub
```

Nếu đặt sau reverse proxy, proxy cần giữ nguyên header `Host`, vì các request POST và DELETE bị chặn khi `Origin` không khớp với `Host`.

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
│                      STEP 3: tts                             │
│  Voiceover: narrative.mp3 (đọc liên tục)                     │
│  Replace:   dub.mp3 (từng câu đúng timestamp, 10 workers)    │
└──────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌──────────────────────────────────────────────────────────────┐
│                 STEP 4: dub + subtitle                       │
│  Voiceover: voiceover.mp4    Replace: dub.mp4                │
│  Phụ đề subtitle.srt được burn vào video                     │
└──────────────────────────────────────────────────────────────┘
```

| Step | Công nghệ | Output | Concurrency |
|---|---|---|---|
| **extract** | `youtube_transcript_api` (Python) | `transcript.json` | ∥ download |
| **download** | yt-dlp | `{video_id}.mp4` | ∥ extract |
| **translate** | Google Translate / DeepSeek (5 workers) | `translated.json` | chunk song song |
| **tts** | Edge TTS (Python) | `narrative.mp3` hoặc `dub.mp3` | 10 workers (Replace) |
| **dub** | FFmpeg (libx264 / libopenh264) | `voiceover.mp4` hoặc `dub.mp4`, kèm `subtitle.srt` | — |

### 2 chế độ lồng tiếng

| | Voiceover | Replace |
|---|---|---|
| Audio gốc | Giữ 15% | Bỏ hoàn toàn |
| TTS audio | Narrative (đọc liên tục) | Dubbing (sync timestamp) |
| Phụ đề | Hardcode vào video | Hardcode vào video |
| Output | `voiceover.mp4` | `dub.mp4` |

### Audio alignment

- **Độ dài bản dịch:** mỗi segment gửi cho DeepSeek kèm `max_chars`, là số ký tự đọc kịp trong thời lượng của câu gốc (`BudgetCharsPerSecond` trong `internal/translator/budget.go`). Model được yêu cầu dịch súc tích để vừa giới hạn này. Google Translate không nhận được chỉ dẫn này.
- **Replace:** các mảnh phụ đề (thường khoảng 2 giây) được gộp thành đơn vị cỡ một câu, tách tại dấu câu, khoảng lặng từ 0.6 giây hoặc sau tối đa 15 giây. Mỗi câu phải đọc xong trước khi câu tiếp theo bắt đầu. Script đo độ dài thật của audio TTS rồi lần lượt tăng tốc độ đọc (tối đa +25%), time-stretch (thêm tối đa +15%) và cuối cùng cắt + fade-out. Track `dub.mp3` không bao giờ bị đổi tốc độ cả bài, nên không bị lệch tiếng.
- **Voiceover:** nếu `narrative.mp3` dài hơn video, FFmpeg `atempo` tăng tốc tối đa +25%, rồi `rubberband` thêm tối đa +15%, cuối cùng cắt + fade-out.
- **Độ dài output** luôn bằng video: audio được `apad` và FFmpeg dừng theo `-shortest`.

Sau mỗi lần dub, log của `tts.py` in số câu đọc tự nhiên, số câu phải tăng tốc và số câu bị cắt, cùng tốc độ đọc đo được (`Measured speech rate`). Nếu tốc độ đo được khác xa 15 ký tự mỗi giây, hãy chỉnh `BudgetCharsPerSecond` theo con số đó, nhân thêm khoảng 1.1.

### File-based checkpoint

Pipeline dùng file system làm state, giống `make`: một step được bỏ qua khi file output của nó mới hơn các file input. Chạy lại một step (bằng force refresh, hoặc sau khi nó lỗi) sẽ tự động làm các step sau chạy lại. Nếu job bị ngắt giữa chừng, chạy lại cùng video ID sẽ tiếp tục từ step chưa hoàn thành. Các file output được ghi vào file tạm rồi đổi tên, nên một step bị ngắt không để lại file dở dang.

```
data/{video_id}/
├── job.json              ← thông tin job (mode, voice, status)
├── transcript.json       ← extract
├── translated.json       ← translate
├── {video_id}.mp4        ← download
├── narrative.mp3         ← tts (Voiceover)
├── dub.mp3               ← tts (Replace)
├── subtitle.srt          ← tạo từ translated.json
├── voiceover.mp4         ← dub (Voiceover)
└── dub.mp4               ← dub (Replace)
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
  │                      └── internal/storage/         (job.json, cleanup)
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
│   │   ├── helpers.go             # SSE HTML fragments, cache freshness, result files
│   │   ├── step_extract.go        # Subprocess: extract.py
│   │   ├── step_translate.go      # Google/DeepSeek translation
│   │   ├── step_tts.go            # Subprocess: tts.py (narrative hoặc dub)
│   │   ├── step_download.go       # Subprocess: yt-dlp
│   │   └── step_dub.go            # FFmpeg mix (voiceover hoặc replacement)
│   ├── translator/                # Google + DeepSeek providers, chunker, parser
│   ├── tts/                       # Audio alignment (atempo/rubberband)
│   ├── media/                     # DubMixer (FFmpeg), subtitle SRT generator
│   ├── store/                     # In-memory job store
│   ├── storage/                   # job.json, history restore, cleanup
│   └── httpsec/                   # Job ID check, CSRF and DNS rebinding guards
├── scripts/
│   ├── extract.py                 # youtube_transcript_api → JSON
│   └── tts.py                     # edge-tts → narrative.mp3 + dub.mp3
├── templates/
│   ├── partials/header.html       # Dashboard layout, sidebar, CSS
│   ├── partials/footer.html       # Close layout
│   ├── partials/pipeline_view.html # Running job: SSE progress + timeline (sse-swap)
│   ├── partials/job_results.html  # Completed job: download links
│   ├── partials/job_not_found.html
│   ├── partials/job_history.html  # Sidebar history
│   ├── index.html                 # Job form + pipeline view
│   └── job_detail.html            # /jobs/:id full page
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

## API

| Endpoint | Method | Mô tả |
|---|---|---|
| `/` | GET | Dashboard — form + timeline |
| `/jobs` | POST | Tạo job mới (form submit) |
| `/sse/jobs/:id` | GET | SSE stream — real-time log |
| `/jobs/:id` | GET | Trang job (progress hoặc kết quả) |
| `/jobs/:id/files/:filename` | GET | Download output (`voiceover.mp4`, `dub.mp4`, `narrative.mp3`, `dub.mp3`, `subtitle.srt`) |
| `/api/jobs` | GET | Sidebar history (HTML partial) |
| `/api/jobs/:id` | DELETE | Xoá job, trả `409` nếu đang chạy |

## License

MIT
