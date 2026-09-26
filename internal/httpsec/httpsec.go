// Package httpsec holds the HTTP-level protections of the web UI.
package httpsec

import (
	"net"
	"net/url"
	"strings"

	"vidub/internal/store"

	"github.com/gofiber/fiber/v2"
)

// ValidVideoID reports whether id is a YouTube video ID (see store.ValidJobID).
func ValidVideoID(id string) bool {
	return store.ValidJobID(id)
}

// ValidateJobID rejects requests whose :id route parameter is not a video ID.
func ValidateJobID(c *fiber.Ctx) error {
	if !ValidVideoID(c.Params("id")) {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid job id")
	}
	return c.Next()
}

// SameOrigin blocks state-changing requests sent by other websites (CSRF).
// Without it, any page the user visits could POST a job to this server,
// even one that only listens on localhost.
//
// Browsers send Origin (or Sec-Fetch-Site) on such requests. Scripts like
// curl send neither and are let through.
// Behind a reverse proxy, the proxy must forward the original Host header.
func SameOrigin(c *fiber.Ctx) error {
	switch c.Method() {
	case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
		return c.Next()
	}

	origin := c.Get(fiber.HeaderOrigin)
	if origin == "" {
		if site := c.Get("Sec-Fetch-Site"); site == "cross-site" || site == "same-site" {
			return c.Status(fiber.StatusForbidden).SendString("Cross-site request blocked")
		}
		return c.Next()
	}

	u, err := url.Parse(origin) // "null" parses with an empty host and is rejected
	if err != nil || !strings.EqualFold(u.Host, string(c.Request().Host())) {
		return c.Status(fiber.StatusForbidden).SendString("Cross-site request blocked")
	}
	return c.Next()
}

// LoopbackHostOnly rejects requests whose Host header is not a loopback name.
// Use it when the server listens on a loopback address: it stops DNS
// rebinding, where a malicious site points its own domain at 127.0.0.1 to
// read this server's pages. Such requests still carry the attacker's domain
// as Host.
func LoopbackHostOnly(c *fiber.Ctx) error {
	if !IsLoopbackHost(string(c.Request().Host())) {
		return c.Status(fiber.StatusForbidden).SendString("Host not allowed")
	}
	return c.Next()
}

// IsLoopbackHost reports whether host (with or without a port) names this
// machine: localhost, *.localhost, 127.0.0.0/8 or ::1.
func IsLoopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
