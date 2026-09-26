package main

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"

	"vidub/internal/config"
	"vidub/internal/httpsec"
	"vidub/internal/pipeline"
	"vidub/internal/sse"
	"vidub/internal/storage"
	"vidub/internal/store"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/basicauth"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/template/html/v2"
)

type voiceOption struct {
	ID    string
	Label string
}

// voices are the Vietnamese voices Edge TTS offers ("edge-tts --list-voices").
var voices = []voiceOption{
	{"vi-VN-HoaiMyNeural", "Hoai My — Female"},
	{"vi-VN-NamMinhNeural", "Nam Minh — Male"},
}

func main() {
	cfg := config.Load()
	broker := sse.NewBroker()
	jobStore := store.NewJobStore()

	pl := pipeline.New(cfg, broker, jobStore)

	// Cancelled on SIGINT/SIGTERM. Running pipelines then stop, and their
	// child processes (yt-dlp, ffmpeg, python) are killed with them.
	rootCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	var runningJobs sync.WaitGroup

	// The store is in memory; rebuild the history from the job directories.
	restored := storage.LoadJobs(cfg.StorageDir)
	for _, j := range restored {
		jobStore.Restore(j)
	}
	if len(restored) > 0 {
		log.Printf("[jobs] restored %d job(s) from %s", len(restored), cfg.StorageDir)
	}

	engine := html.New("./templates", ".html")
	app := fiber.New(fiber.Config{
		Views: engine,
	})
	app.Use(recover.New())

	loopback := httpsec.IsLoopbackHost(cfg.Host)
	if loopback {
		app.Use(httpsec.LoopbackHostOnly)
	}
	if cfg.AuthPassword != "" {
		app.Use(basicauth.New(basicauth.Config{
			Users: map[string]string{cfg.AuthUser: cfg.AuthPassword},
			Realm: "vidub",
		}))
	} else if !loopback {
		log.Printf("WARNING: listening on %s without AUTH_PASSWORD. Anyone who can reach this port "+
			"can start jobs (using the server's DeepSeek key), download results and delete data.", cfg.Host)
	}
	app.Use(httpsec.SameOrigin)

	// Home page.
	app.Get("/", func(c *fiber.Ctx) error {
		return c.Render("index", fiber.Map{
			"Title": "Start New Job",
			// Only whether a key exists: the key itself never goes to the browser.
			"ServerHasKey": cfg.DeepSeekAPIKey != "",
			"Voices":       voices,
			"DefaultVoice": cfg.TTSVoice,
			"Jobs":         jobStore.List(),
		})
	})

	// Start new job.
	app.Post("/jobs", func(c *fiber.Ctx) error {
		rawURL := c.FormValue("url")
		mode := c.FormValue("mode", "voiceover")
		voice := c.FormValue("voice", cfg.TTSVoice)
		apikey := c.FormValue("apikey")

		videoID := extractVideoID(rawURL)
		if videoID == "" {
			return c.Status(400).SendString("Invalid YouTube URL")
		}
		if mode != "voiceover" && mode != "replace" {
			return c.Status(400).SendString("Mode must be voiceover or replace")
		}
		if !knownVoice(voice) && voice != cfg.TTSVoice {
			return c.Status(400).SendString("Unknown voice")
		}

		forceRefresh := map[string]bool{
			"extract":   c.FormValue("refresh_extract") == "on",
			"translate": c.FormValue("refresh_translate") == "on",
			"tts":       c.FormValue("refresh_tts") == "on",
			"download":  c.FormValue("refresh_download") == "on",
			"dub":       c.FormValue("refresh_dub") == "on",
		}

		// Everything downstream (yt-dlp, the store, templates) gets a URL we
		// built, never the raw input. extractVideoID only searches the input,
		// so "--update-to=... youtube.com/watch?v=..." passes it, and yt-dlp
		// would read the raw string as an option.
		canonicalURL := "https://www.youtube.com/watch?v=" + videoID

		workDir := filepath.Join(cfg.StorageDir, videoID)
		job := pipeline.VideoJob{
			VideoID:      videoID,
			URL:          canonicalURL,
			Mode:         mode,
			Voice:        voice,
			APIKey:       apikey,
			WorkDir:      workDir,
			ForceRefresh: forceRefresh,
		}

		// Push URL so browser navigates to /jobs/:id.
		c.Set("HX-Push", "/jobs/"+videoID)

		// Register the run. If this video is already running, show that run
		// instead of starting a second pipeline in the same directory.
		storedJob, started := jobStore.Start(videoID, canonicalURL, mode, voice)
		if !started {
			log.Printf("[job %s] already running, attaching to it", videoID)
			return c.Render("partials/pipeline_view", fiber.Map{
				"VideoID": videoID,
				"Job":     storedJob,
			})
		}

		// Drop events from any previous run before the client opens the SSE stream.
		broker.Reset(videoID)

		runningJobs.Add(1)
		go func() {
			defer runningJobs.Done()
			if _, err := pl.Run(rootCtx, job); err != nil {
				log.Printf("[job %s] pipeline error: %v", videoID, err)
			}
		}()

		return c.Render("partials/pipeline_view", fiber.Map{
			"VideoID": videoID,
			"Job":     storedJob,
		})
	})

	// Job detail page (direct access) or content (htmx request).
	app.Get("/jobs/:id", httpsec.ValidateJobID, func(c *fiber.Ctx) error {
		jobID := c.Params("id")
		job, ok := jobStore.Get(jobID)

		if c.Get("HX-Request") != "" {
			if !ok {
				return c.Render("partials/job_not_found", fiber.Map{"VideoID": jobID})
			}
			if job.Status == store.JobCompleted {
				return c.Render("partials/job_results", fiber.Map{
					"VideoID": jobID,
					"Job":     job,
					"Files":   pipeline.ResultFiles(filepath.Join(cfg.StorageDir, jobID)),
				})
			}
			return c.Render("partials/pipeline_view", fiber.Map{"VideoID": jobID, "Job": job})
		}

		return c.Render("job_detail", fiber.Map{
			"Title":    "Job " + jobID,
			"VideoID":  jobID,
			"Job":      job,
			"NotFound": !ok,
			"Files":    pipeline.ResultFiles(filepath.Join(cfg.StorageDir, jobID)),
			"Jobs":     jobStore.List(),
		})
	})

	// Sidebar history partial (for refresh).
	app.Get("/api/jobs", func(c *fiber.Ctx) error {
		return c.Render("partials/job_history", fiber.Map{
			"Jobs": jobStore.List(),
		})
	})

	app.Get("/sse/jobs/:id", httpsec.ValidateJobID, broker.SSEHandler)

	app.Get("/jobs/:id/files/:filename", httpsec.ValidateJobID, func(c *fiber.Ctx) error {
		jobID := c.Params("id")
		filename := c.Params("filename")
		if !pipeline.IsResultFile(filename) {
			return c.Status(404).SendString("File not found")
		}

		filePath := filepath.Join(cfg.StorageDir, jobID, filename)
		if _, err := os.Stat(filePath); err != nil {
			return c.Status(404).SendString("File not found")
		}

		c.Attachment(filename)
		return c.SendFile(filePath)
	})

	// DELETE /api/jobs/:id — manual cleanup for a specific job.
	app.Delete("/api/jobs/:id", httpsec.ValidateJobID, func(c *fiber.Ctx) error {
		jobID := c.Params("id")
		dirPath := filepath.Join(cfg.StorageDir, jobID)

		err := jobStore.Remove(jobID, func() error { return os.RemoveAll(dirPath) })
		if errors.Is(err, store.ErrJobRunning) {
			return c.Status(409).JSON(fiber.Map{"error": "Job is running"})
		}
		if err != nil {
			log.Printf("[jobs] delete %s: %v", jobID, err)
			return c.Status(500).JSON(fiber.Map{"error": "Failed to delete job"})
		}
		broker.Reset(jobID)

		log.Printf("[jobs] deleted %s", jobID)
		return c.JSON(fiber.Map{"status": "deleted"})
	})

	// Background TTL cleanup for old jobs (default 24h).
	var cleanupAge time.Duration // 0 means the default
	if v := os.Getenv("CLEANUP_MAX_AGE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Fatalf("invalid CLEANUP_MAX_AGE %q (examples: 48h, 90m): %v", v, err)
		}
		cleanupAge = d
	}
	cleanupDone := make(chan struct{})
	storage.CleanupOldJobs(cfg.StorageDir, cleanupAge, jobStore, cleanupDone)
	defer close(cleanupDone)

	go func() {
		<-rootCtx.Done()
		stopSignals() // a second Ctrl+C now kills the process immediately
		log.Println("Shutting down...")
		app.Shutdown()
	}()

	srvAddr := net.JoinHostPort(cfg.Host, cfg.Port)
	log.Printf("vidub server starting on %s", srvAddr)
	if err := app.Listen(srvAddr); err != nil {
		log.Fatalf("Server error: %v", err)
	}

	// Give cancelled pipelines a moment to record their final status.
	waited := make(chan struct{})
	go func() {
		runningJobs.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(15 * time.Second):
		log.Println("Some jobs did not stop in time")
	}
}

func knownVoice(id string) bool {
	for _, v := range voices {
		if v.ID == id {
			return true
		}
	}
	return false
}

var (
	videoURLPatterns = []*regexp.Regexp{
		regexp.MustCompile(`youtube\.com/watch\?.*v=([A-Za-z0-9_-]{11})`),
		regexp.MustCompile(`youtu\.be/([A-Za-z0-9_-]{11})`),
		regexp.MustCompile(`youtube\.com/embed/([A-Za-z0-9_-]{11})`),
		regexp.MustCompile(`youtube\.com/shorts/([A-Za-z0-9_-]{11})`),
		regexp.MustCompile(`youtube\.com/v/([A-Za-z0-9_-]{11})`),
		regexp.MustCompile(`youtube\.com/live/([A-Za-z0-9_-]{11})`),
	}
	bareVideoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
)

// extractVideoID finds a YouTube video ID in a URL, or accepts a bare ID.
// It only searches the input: callers must not pass the input on.
func extractVideoID(rawURL string) string {
	for _, re := range videoURLPatterns {
		if m := re.FindStringSubmatch(rawURL); m != nil {
			return m[1]
		}
	}
	if bareVideoID.MatchString(rawURL) {
		return rawURL
	}
	return ""
}
