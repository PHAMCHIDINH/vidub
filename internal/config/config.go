package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port           string
	StorageDir     string
	DeepSeekAPIKey string
	DeepSeekModel  string
	TTSVoice       string
	MaxHeight      int
	AudioBitrate   string
	// Google Drive integration
	GoogleDriveEnabled     bool
	GoogleDriveCredentials string
	GoogleDriveFolder      string
}

func Load() *Config {
	return &Config{
		Port:                   getEnv("PORT", "8080"),
		StorageDir:             getEnv("STORAGE_DIR", "./data"),
		DeepSeekAPIKey:         getEnv("DEEPSEEK_API_KEY", ""),
		DeepSeekModel:          getEnv("DEEPSEEK_MODEL", "deepseek-v4-flash"),
		TTSVoice:               getEnv("TTS_VOICE", "vi-VN-HoaiMyNeural"),
		MaxHeight:              720,
		AudioBitrate:           getEnv("AUDIO_BITRATE", "128k"),
		GoogleDriveEnabled:     getBoolEnv("GOOGLE_DRIVE_ENABLED", false),
		GoogleDriveCredentials: getEnv("GOOGLE_DRIVE_CREDENTIALS", ".commandcode/gdrive-credentials.json"),
		GoogleDriveFolder:      getEnv("GOOGLE_DRIVE_FOLDER", "vidub"),
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

func getBoolEnv(key string, fallback bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fallback
		}
		return b
	}
	return fallback
}
