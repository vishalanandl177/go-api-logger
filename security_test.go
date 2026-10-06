package apilog

import (
	"strings"
	"testing"
	"time"
)

func TestAllSecurityRules(t *testing.T) {
	for n, id := range SecurityRuleIDs() {
		t.Run(id, func(t *testing.T) {
			c := DefaultConfig().Security
			c.Enabled = true
			c.InspectRequest = true
			c.InspectResponse = true
			d, err := newSecurityDetector(c)
			if err != nil {
				t.Fatal(err)
			}
			e := Event{Status: 200, Route: "/items/{id}", Path: "/items/123", URL: "/items", ClientIP: "127.0.0.1", Context: map[string]string{}}
			repetitions := 1
			switch n + 1 {
			case 1:
				e.Status = 401
			case 2:
				e.Status = 401
				for i := 0; i < 3; i++ {
					d.inspect(e)
				}
				e.Status = 200
			case 3:
				e.Status = 401
				e.Route = "/auth/token"
			case 4:
				e.Status = 403
			case 5:
				e.Route = "/admin/debug"
			case 6:
				e.Request.Data = []byte(`<script>alert(1)</script>`)
			case 7:
				e.Status = 429
			case 8:
				e.Status = 404
			case 9:
				e.Status = 404
				repetitions = 3
			case 10:
				e.Request.Data = []byte("hello\nworld")
			case 11:
				e.Response.Data = []byte(`{"password":"x"}`)
			case 12:
				e.URL = "/items?page=1"
				repetitions = 3
			case 13:
				e.Route = "/report/export"
			case 14:
				e.Response.Data = []byte(strings.Repeat("x", 8192))
			case 15:
				e.Context["is_sensitive_route"] = "true"
			case 16:
				e.Context["flow_name"] = "checkout"
				e.Context["business_action"] = "buy"
				repetitions = 3
			}
			var signals []SecuritySignal
			for i := 0; i < repetitions; i++ {
				signals = d.inspect(e)
			}
			found := false
			for _, s := range signals {
				if s.RuleID == id {
					found = true
				}
			}
			if !found {
				t.Fatalf("rule not emitted %+v", signals)
			}
		})
	}
}
func TestSecurityBoundedExpiringState(t *testing.T) {
	c := DefaultConfig().Security
	c.Enabled = true
	c.MaxActors = 2
	d, _ := newSecurityDetector(c)
	now := time.Now()
	for _, key := range []string{"a", "b", "c"} {
		d.count(key, true, now)
	}
	if len(d.state) > 2 {
		t.Fatal("unbounded state")
	}
	if d.count("c", false, now.Add(c.Window+time.Second)) != 0 {
		t.Fatal("expired state used")
	}
}
func TestSecurityDisabledAndInspectionOptIn(t *testing.T) {
	if d, _ := newSecurityDetector(DefaultConfig().Security); d != nil {
		t.Fatal("enabled by default")
	}
	c := DefaultConfig().Security
	c.Enabled = true
	d, _ := newSecurityDetector(c)
	signals := d.inspect(Event{Request: Body{Data: []byte("<script>\n")}, Response: Body{Data: []byte("password")}})
	if len(signals) != 0 {
		t.Fatal("inspected without opt-in")
	}
}
