package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"vidub/internal/config"
	"vidub/internal/pipeline"
	"vidub/internal/sse"
	"vidub/internal/storage"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/template/html/v2"
)

type voiceOption struct {
	ID    string
	Label string
}

var voices = []voiceOption{
	{"vi-VN-HoaiMyNeural", "Hoai My — Female, Southern"},
	{"vi-VN-NamMinhNeural", "Nam Minh — Male, Southern"},
	{"vi-VN-HongAnNeural", "Hong An — Female, Northern"},
	{"vi-VN-AnhDungNeural", "Anh Dung — Male, Northern"},
	{"vi-VN-LinhSanNeural", "Linh San — Female, Central"},
}

func main() {
	cfg := config.Load()
	broker := sse.NewBroker()

	var gdrive *storage.DriveClient
	if cfg.GoogleDriveEnabled {
		var err error
		gdrive, err = storage.NewDriveClient(cfg.GoogleDriveCredentials, cfg.GoogleDriveFolder)
		if err != nil {
			log.Printf("[gdrive] init failed (upload disabled): %v", err)
			gdrive = nil
		}
	}

	pl := pipeline.New(cfg, broker, gdrive)

	engine := html.New("./templates", ".html")
	app := fiber.New(fiber.Config{
		Views: engine,
	})
	app.Use(recover.New())

	app.Get("/", func(c *fiber.Ctx) error {
		return c.Render("index", fiber.Map{
			"Title":        "Start New Job",
			"APIKey":       cfg.DeepSeekAPIKey,
			"Voices":       voices,
			"DefaultVoice": cfg.TTSVoice,
		})
	})

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

		workDir := filepath.Join(cfg.StorageDir, videoID)
		job := pipeline.VideoJob{
			VideoID:  videoID,
			URL:      rawURL,
			Mode:     mode,
			Voice:    voice,
			APIKey:   apikey,
			WorkDir:  workDir,
		}

		go func() {
			if _, err := pl.Run(context.Background(), job); err != nil {
				log.Printf("[job %s] pipeline error: %v", videoID, err)
			}
		}()

		return c.Render("job_progress", fiber.Map{
			"VideoID": videoID,
			"URL":     rawURL,
		})
	})

	app.Get("/sse/jobs/:id", broker.SSEHandler)

	app.Get("/jobs/:id/files/:filename", func(c *fiber.Ctx) error {
		jobID := c.Params("id")
		filename := c.Params("filename")

		filePath := filepath.Join(cfg.StorageDir, jobID, filepath.Clean(filename))

		absStorage, _ := filepath.Abs(cfg.StorageDir)
		absPath, _ := filepath.Abs(filePath)
		if !strings.HasPrefix(absPath, absStorage) {
			return c.Status(403).SendString("Forbidden")
		}
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return c.Status(404).SendString("File not found")
		}

		c.Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
		return c.SendFile(filePath)
	})

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		app.Shutdown()
	}()

	addr := ":" + cfg.Port
	log.Printf("vidub server starting on %s", addr)
	if err := app.Listen(addr); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func extractVideoID(rawURL string) string {
	patterns := []string{
		`youtube\.com/watch\?.*v=([A-Za-z0-9_-]{11})`,
		`youtu\.be/([A-Za-z0-9_-]{11})`,
		`youtube\.com/embed/([A-Za-z0-9_-]{11})`,
		`youtube\.com/shorts/([A-Za-z0-9_-]{11})`,
		`youtube\.com/v/([A-Za-z0-9_-]{11})`,
		`youtube\.com/live/([A-Za-z0-9_-]{11})`,
	}
	for _, p := range patterns {
		re := regexp.MustCompile(p)
		if m := re.FindStringSubmatch(rawURL); m != nil {
			return m[1]
		}
	}
	if regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`).MatchString(rawURL) {
		return rawURL
	}
	return ""
}
