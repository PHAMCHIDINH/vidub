package httpsec

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestValidVideoID(t *testing.T) {
	for id, want := range map[string]bool{
		"dQw4w9WgXcQ":  true,
		"a_b-c_d-e_f":  true,
		"..":           false,
		".":            false,
		"dQw4w9WgXc":   false, // 10 chars
		"dQw4w9WgXcQQ": false, // 12 chars
		"dQw4w9W/XcQ":  false,
		"dQw4w9W.XcQ":  false,
	} {
		if got := ValidVideoID(id); got != want {
			t.Errorf("ValidVideoID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost:8080":     true,
		"LOCALHOST":          true,
		"app.localhost:8080": true,
		"127.0.0.1:8080":     true,
		"127.5.6.7":          true,
		"[::1]:8080":         true,
		"::1":                true,
		"evil.example:8080":  false,
		"localhost.evil.com": false,
		"192.168.1.10:8080":  false,
		"0.0.0.0:8080":       false,
		"":                   false,
	} {
		if got := IsLoopbackHost(host); got != want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	app := fiber.New()
	app.Use(SameOrigin)
	ok := func(c *fiber.Ctx) error { return c.SendString("ok") }
	app.Post("/jobs", ok)
	app.Get("/jobs", ok)

	tests := []struct {
		name, method string
		headers      map[string]string
		want         int
	}{
		{"same origin post", "POST", map[string]string{"Origin": "http://example.com"}, 200},
		{"cross origin post", "POST", map[string]string{"Origin": "http://evil.example"}, 403},
		{"null origin post", "POST", map[string]string{"Origin": "null"}, 403},
		{"script without origin", "POST", nil, 200},
		{"cross-site fetch without origin", "POST", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"cross origin get is allowed", "GET", map[string]string{"Origin": "http://evil.example"}, 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://example.com/jobs", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tt.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}
