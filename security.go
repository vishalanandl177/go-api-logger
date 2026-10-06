package apilog

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type rollingCount struct {
	count            int
	expires, touched time.Time
}
type securityDetector struct {
	mu      sync.Mutex
	config  SecurityConfig
	state   map[string]rollingCount
	enabled map[string]bool
}

var payloadAttack = regexp.MustCompile(`(?i)(<script\b|javascript:|union\s+select|\bor\b\s+1\s*=\s*1|--|/\*|\.\./|%2e%2e)`)
var objectID = regexp.MustCompile(`/[0-9]+(?:/|$)`)

// SecurityRuleIDs lists all supported detect-only rules. IDs match the existing
// DRF registry so alert mappings can be shared between Python and Go services.
func SecurityRuleIDs() []string {
	ids := make([]string, 16)
	for i := range ids {
		ids[i] = fmt.Sprintf("DRFSEC-%03d", i+1)
	}
	return ids
}
func newSecurityDetector(c SecurityConfig) (*securityDetector, error) {
	if !c.Enabled {
		return nil, nil
	}
	if c.MaxActors < 1 || c.MaxActors > 100000 || c.SampleBytes < 1 || c.SampleBytes > 64<<10 || c.Window <= 0 {
		return nil, errors.New("apilog: invalid security bounds")
	}
	if len(c.Secret) == 0 {
		c.Secret = make([]byte, 32)
		if _, err := rand.Read(c.Secret); err != nil {
			return nil, err
		}
	} else {
		c.Secret = append([]byte{}, c.Secret...)
	}
	d := &securityDetector{config: c, state: map[string]rollingCount{}, enabled: map[string]bool{}}
	known := SecurityRuleIDs()
	if c.Rules == nil {
		c.Rules = known
	}
	for _, id := range c.Rules {
		if !contains(known, id) {
			return nil, errors.New("apilog: unknown security rule")
		}
		d.enabled[id] = true
	}
	return d, nil
}
func (d *securityDetector) count(key string, increment bool, now time.Time) int {
	v, ok := d.state[key]
	if ok && !now.Before(v.expires) {
		delete(d.state, key)
		ok = false
	}
	if !increment {
		if !ok {
			return 0
		}
		return v.count
	}
	if !ok {
		if len(d.state) >= d.config.MaxActors {
			oldest := ""
			var at time.Time
			for k, entry := range d.state {
				if !now.Before(entry.expires) {
					delete(d.state, k)
					continue
				}
				if oldest == "" || entry.touched.Before(at) {
					oldest = k
					at = entry.touched
				}
			}
			if len(d.state) >= d.config.MaxActors {
				delete(d.state, oldest)
			}
		}
		v = rollingCount{expires: now.Add(d.config.Window)}
	}
	v.count++
	v.touched = now
	d.state[key] = v
	return v.count
}
func (d *securityDetector) inspect(e Event) (signals []SecuritySignal) {
	defer func() {
		if recover() != nil {
			signals = nil
		}
	}()
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	h := hmac.New(sha256.New, d.config.Secret)
	_, _ = h.Write([]byte(e.ClientIP + "\x00" + e.Context["actor_id"]))
	actor := hex.EncodeToString(h.Sum(nil))
	route := strings.ToLower(e.Route)
	if len(route) > 512 {
		route = route[:512]
	}
	key := actor + ":" + route
	request, response := []byte(nil), []byte(nil)
	if d.config.InspectRequest {
		request = e.Request.Data[:min(len(e.Request.Data), d.config.SampleBytes)]
	}
	if d.config.InspectResponse {
		response = e.Response.Data[:min(len(e.Response.Data), d.config.SampleBytes)]
	}
	add := func(n int, category, severity string, score int, condition bool) {
		id := fmt.Sprintf("DRFSEC-%03d", n)
		if condition && d.enabled[id] {
			signals = append(signals, SecuritySignal{id, category, severity, score})
		}
	}
	if e.Status == 401 && (d.enabled["DRFSEC-001"] || d.enabled["DRFSEC-002"]) {
		d.count("auth:"+key, true, now)
	}
	add(1, "authentication", "warning", 3, e.Status == 401)
	add(2, "authentication", "warning", 4, e.Status >= 200 && e.Status < 300 && d.count("auth:"+key, false, now) >= 3)
	add(3, "token", "warning", 3, e.Status == 401 && (strings.Contains(route, "token") || strings.Contains(route, "auth")))
	add(4, "authorization", "warning", 3, e.Status == 403)
	add(5, "reconnaissance", "warning", 3, strings.Contains(route, "admin") || strings.Contains(route, "debug"))
	add(6, "payload", "warning", 4, len(request) > 0 && payloadAttack.Match(request))
	add(7, "resource_abuse", "warning", 3, e.Status == 429)
	add(8, "reconnaissance", "notice", 2, e.Status == 404)
	objectProbe := (e.Status == 403 || e.Status == 404) && (objectID.MatchString(e.Path) || strings.Contains(route, "{id}") || strings.Contains(route, ":id"))
	add(9, "authorization", "warning", 4, objectProbe && d.enabled["DRFSEC-009"] && d.count("objects:"+key, true, now) >= 3)
	add(10, "payload", "notice", 2, bytes.ContainsAny(request, "\r\n"))
	lower := bytes.ToLower(response)
	add(11, "data_exposure", "warning", 4, bytes.Contains(lower, []byte("password")) || bytes.Contains(lower, []byte("api_key")) || bytes.Contains(lower, []byte("authorization")))
	u, _ := url.Parse(e.URL)
	pagination := false
	if u != nil {
		q := u.Query()
		pagination = q.Has("page") || q.Has("cursor")
	}
	add(12, "scraping", "notice", 2, pagination && d.enabled["DRFSEC-012"] && d.count("pages:"+key, true, now) >= 3)
	action := strings.ToLower(e.Context["business_action"])
	add(13, "data_exfiltration", "warning", 4, strings.Contains(route, "export") || strings.Contains(route, "download") || strings.Contains(route, "report") || strings.Contains(action, "export"))
	add(14, "data_exfiltration", "notice", 2, len(response) >= 8192)
	add(15, "business_logic", "notice", 2, e.Context["is_sensitive_route"] == "true")
	flow := e.Context["flow_name"]
	add(16, "business_logic", "warning", 4, flow != "" && action != "" && d.enabled["DRFSEC-016"] && d.count("flow:"+actor+":"+cleanText(flow, 128)+":"+cleanText(action, 128), true, now) >= 3)
	return signals
}
