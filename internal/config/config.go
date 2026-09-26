package config

import (
	"os"
)

type Config struct {
	// Host is the listen address. The default only accepts connections from
	// this machine. Set HOST=0.0.0.0 to serve a LAN or a container, together
	// with AUTH_PASSWORD.
	Host           string
	Port           string
	StorageDir     string
	DeepSeekAPIKey string
	DeepSeekModel  string
	TTSVoice       string
	// AuthUser and AuthPassword enable HTTP basic auth when the password is set.
	AuthUser     string
	AuthPassword string
}

func Load() *Config {
	return &Config{
		Host:           getEnv("HOST", "127.0.0.1"),
		Port:           getEnv("PORT", "8080"),
		StorageDir:     getEnv("STORAGE_DIR", "./data"),
		DeepSeekAPIKey: getEnv("DEEPSEEK_API_KEY", ""),
		DeepSeekModel:  getEnv("DEEPSEEK_MODEL", "deepseek-v4-flash"),
		TTSVoice:       getEnv("TTS_VOICE", "vi-VN-HoaiMyNeural"),
		AuthUser:       getEnv("AUTH_USER", "admin"),
		AuthPassword:   getEnv("AUTH_PASSWORD", ""),
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}
