package main

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/brianvoe/gofakeit/v7"

	"github.com/prismgroup/query-api/internal/embed"
)

type generator struct {
	rng *rand.Rand
	emb embed.Embedder
	now time.Time
}

var (
	levels   = []string{"debug", "info", "warn", "error"}
	levelW   = []float64{0.15, 0.65, 0.15, 0.05}
	methods  = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	methodW  = []float64{0.7, 0.2, 0.05, 0.02, 0.03}
	statuses = []int{200, 201, 204, 301, 302, 400, 401, 403, 404, 429, 500, 502, 503}
	statusW  = []float64{0.6, 0.05, 0.05, 0.02, 0.03, 0.05, 0.04, 0.02, 0.08, 0.01, 0.03, 0.01, 0.01}
	regions  = []string{"ap-southeast-2", "us-east-1", "us-west-2", "eu-west-1", "eu-central-1"}
	paths    = []string{"/", "/login", "/logout", "/api/v1/users", "/api/v1/orders", "/api/v1/search", "/health", "/static/app.js", "/checkout", "/admin", "/api/v1/devices", "/api/v1/alerts"}
	vendors  = []string{"Cisco", "Juniper", "Dell", "HP", "Apple", "Ubiquiti", "Raspberry Pi"}
	oses     = []string{"IOS-XE 17.9", "Junos 22.4", "Ubuntu 24.04", "Windows Server 2022", "macOS 15", "UniFi OS 4", "Raspberry Pi OS", "Debian 12"}
	sevs     = []string{"low", "medium", "high", "critical"}
	sevW     = []float64{0.4, 0.35, 0.2, 0.05}
	tagPool  = []string{"prod", "staging", "canary", "pci", "internal", "vpn", "iot", "legacy", "gpu", "edge"}

	cities = []struct {
		name     string
		lat, lon float64
	}{
		{"Sydney", -33.8688, 151.2093}, {"Melbourne", -37.8136, 144.9631}, {"Singapore", 1.3521, 103.8198},
		{"London", 51.5072, -0.1276}, {"Frankfurt", 50.1109, 8.6821}, {"New York", 40.7128, -74.0060},
	}

	rules = []struct{ id, name, tmpl string }{
		{"R-1001", "Brute force login", "Repeated failed logins from %s against %s (%d attempts)"},
		{"R-1002", "Port scan detected", "Host %s scanned %s on %d ports"},
		{"R-1003", "Malware beacon", "Outbound beacon from %s to %s on port %d matches known C2 pattern"},
		{"R-1004", "Data exfiltration", "Unusual upload volume from %s to %s (%d MB)"},
		{"R-1005", "Privilege escalation", "Process on %s escalated to root via %s (pid %d)"},
		{"R-1006", "Suspicious DNS", "Host %s queried DGA-like domain via %s (%d queries)"},
	}
	logTemplates = map[string][]string{
		"debug": {"cache lookup for %s hit", "rendering template for %s", "query plan for %s built in %dms"},
		"info":  {"request completed for %s", "user session started on %s", "served %s in %dms", "health check ok on %s"},
		"warn":  {"slow response for %s took %dms", "rate limit approaching for %s", "retrying upstream call for %s", "deprecated endpoint %s called"},
		"error": {"upstream timeout for %s after %dms", "database connection refused while serving %s", "login failure for %s: invalid credentials", "unhandled exception in %s handler", "user locked out after failed attempts on %s"},
	}
)

func (g *generator) pick(items []string, weights []float64) string {
	r := g.rng.Float64()
	acc := 0.0
	for i, w := range weights {
		acc += w
		if r <= acc {
			return items[i]
		}
	}
	return items[len(items)-1]
}

func (g *generator) pickInt(items []int, weights []float64) int {
	r := g.rng.Float64()
	acc := 0.0
	for i, w := range weights {
		acc += w
		if r <= acc {
			return items[i]
		}
	}
	return items[len(items)-1]
}

// recentTime returns a timestamp within the last 30 days with a daytime skew.
func (g *generator) recentTime() time.Time {
	days := g.rng.Float64() * 30
	hour := math.Mod(g.rng.NormFloat64()*4+14, 24)
	if hour < 0 {
		hour += 24
	}
	t := g.now.Add(-time.Duration(days*24) * time.Hour)
	return time.Date(t.Year(), t.Month(), t.Day(), int(hour), g.rng.IntN(60), g.rng.IntN(60), 0, time.UTC)
}

func (g *generator) ip(private bool) string {
	if private {
		switch g.rng.IntN(3) {
		case 0:
			return fmt.Sprintf("10.%d.%d.%d", g.rng.IntN(256), g.rng.IntN(256), 1+g.rng.IntN(254))
		case 1:
			return fmt.Sprintf("192.168.%d.%d", g.rng.IntN(256), 1+g.rng.IntN(254))
		default:
			return fmt.Sprintf("172.%d.%d.%d", 16+g.rng.IntN(16), g.rng.IntN(256), 1+g.rng.IntN(254))
		}
	}
	return gofakeit.IPv4Address()
}

func (g *generator) mac() string {
	ouis := []string{"00:1a:2b", "3c:22:fb", "b8:27:eb", "f4:5c:89", "00:0c:29", "dc:a6:32"}
	return fmt.Sprintf("%s:%02x:%02x:%02x", ouis[g.rng.IntN(len(ouis))], g.rng.IntN(256), g.rng.IntN(256), g.rng.IntN(256))
}

func (g *generator) location() map[string]float64 {
	c := cities[g.rng.IntN(len(cities))]
	return map[string]float64{"lat": c.lat + g.rng.NormFloat64()*0.15, "lon": c.lon + g.rng.NormFloat64()*0.15}
}

func (g *generator) tags() []string {
	n := g.rng.IntN(3)
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, tagPool[g.rng.IntN(len(tagPool))])
	}
	return out
}

func (g *generator) vector(text string) []float32 {
	v, err := g.emb.Embed(context.Background(), []string{text})
	if err != nil {
		panic(err)
	}
	return v[0]
}

func (g *generator) webLog() map[string]any {
	level := g.pick(levels, levelW)
	status := g.pickInt(statuses, statusW)
	if level == "error" && status < 400 {
		status = 500
	}
	path := paths[g.rng.IntN(len(paths))]
	tm := logTemplates[level]
	tmpl := tm[g.rng.IntN(len(tm))]
	latency := math.Abs(g.rng.NormFloat64()*120 + 80)
	if status >= 500 {
		latency += 800 + g.rng.Float64()*3000
	}
	var msg string
	switch {
	case countVerbs(tmpl) == 2:
		msg = fmt.Sprintf(tmpl, path, int(latency))
	default:
		msg = fmt.Sprintf(tmpl, path)
	}
	return map[string]any{
		"timestamp":  g.recentTime().Format(time.RFC3339Nano),
		"level":      level,
		"method":     g.pick(methods, methodW),
		"status":     status,
		"path":       path,
		"host":       fmt.Sprintf("web-%02d", 1+g.rng.IntN(12)),
		"region":     regions[g.rng.IntN(len(regions))],
		"src_ip":     g.ip(g.rng.Float64() < 0.5),
		"dst_ip":     fmt.Sprintf("10.0.%d.%d", g.rng.IntN(4), 10+g.rng.IntN(20)),
		"mac":        g.mac(),
		"bytes":      int(math.Abs(g.rng.NormFloat64()*40000 + 12000)),
		"latency_ms": math.Round(latency*100) / 100,
		"user_agent": gofakeit.UserAgent(),
		"message":    msg,
		"tags":       g.tags(),
		"active":     g.rng.Float64() < 0.7,
		"location":   g.location(),
		"trace_id":   gofakeit.UUID(),
		"embedding":  g.vector(msg),
	}
}

func (g *generator) device() map[string]any {
	vendor := vendors[g.rng.IntN(len(vendors))]
	last := g.recentTime()
	first := last.Add(-time.Duration(g.rng.IntN(700)+1) * 24 * time.Hour)
	ports := []int{}
	for _, p := range []int{22, 80, 443, 161, 3389, 8080, 5432, 3306} {
		if g.rng.Float64() < 0.3 {
			ports = append(ports, p)
		}
	}
	notes := fmt.Sprintf("%s device running %s in %s, %s", vendor, oses[g.rng.IntN(len(oses))], cities[g.rng.IntN(len(cities))].name, gofakeit.HipsterSentence())
	return map[string]any{
		"timestamp":  last.Format(time.RFC3339Nano),
		"first_seen": first.Format(time.RFC3339Nano),
		"host":       fmt.Sprintf("%s-%s-%03d", gofakeit.Word(), vendorSlug(vendor), g.rng.IntN(1000)),
		"ip":         g.ip(true),
		"mac":        g.mac(),
		"vendor":     vendor,
		"os":         oses[g.rng.IntN(len(oses))],
		"open_ports": ports,
		"managed":    g.rng.Float64() < 0.8,
		"location":   g.location(),
		"notes":      notes,
		"tags":       g.tags(),
		"embedding":  g.vector(notes),
	}
}

func (g *generator) alert() map[string]any {
	r := rules[g.rng.IntN(len(rules))]
	src, dst := g.ip(g.rng.Float64() < 0.4), g.ip(true)
	host := fmt.Sprintf("web-%02d", 1+g.rng.IntN(12))
	port := []int{22, 443, 3389, 8443, 53, 445}[g.rng.IntN(6)]
	msg := fmt.Sprintf(r.tmpl, src, host, 1+g.rng.IntN(500))
	return map[string]any{
		"timestamp":    g.recentTime().Format(time.RFC3339Nano),
		"severity":     g.pick(sevs, sevW),
		"rule_id":      r.id,
		"rule_name":    r.name,
		"host":         host,
		"src_ip":       src,
		"dst_ip":       dst,
		"dst_port":     port,
		"mac":          g.mac(),
		"message":      msg,
		"acknowledged": g.rng.Float64() < 0.3,
		"location":     g.location(),
		"tags":         g.tags(),
		"embedding":    g.vector(msg),
	}
}

func countVerbs(s string) int {
	n := 0
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '%' && (s[i+1] == 's' || s[i+1] == 'd') {
			n++
		}
	}
	return n
}

func vendorSlug(v string) string {
	out := make([]byte, 0, len(v))
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		if c == ' ' {
			continue
		}
		out = append(out, c)
	}
	return string(out)
}
