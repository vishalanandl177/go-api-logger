package apilog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/url"
	"strings"
	"unicode"
)

const Filtered = "***FILTERED***"
const maxMetadataBytes = 16 << 10

var defaultMaskKeys = []string{"password", "token", "access", "refresh", "authorization", "proxy_authorization", "cookie", "set_cookie", "x_api_key", "api_key", "secret", "client_secret", "private_key", "sessionid", "csrfmiddlewaretoken"}

func normalized(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "-", "_")
}

func copyHeaders(h map[string][]string) map[string][]string {
	if h == nil {
		return nil
	}
	result := make(map[string][]string, len(h))
	for k, v := range h {
		result[k] = append([]string(nil), v...)
	}
	return result
}
func keySet(extra []string) map[string]bool {
	m := make(map[string]bool, len(defaultMaskKeys)+len(extra))
	for _, k := range defaultMaskKeys {
		m[normalized(k)] = true
	}
	for _, k := range extra {
		m[normalized(k)] = true
	}
	return m
}

// SanitizeJSON rejects malformed, trailing, and excessively nested JSON. It
// preserves numeric precision and never falls back to the original bytes.
func SanitizeJSON(data []byte, keys []string) ([]byte, error) {
	var value any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return nil, errors.New("apilog: invalid JSON")
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return nil, errors.New("apilog: trailing JSON")
	}
	if err := redactValue(value, keySet(keys), 0); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func redactValue(v any, keys map[string]bool, depth int) error {
	if depth > 64 {
		return errors.New("apilog: JSON nesting limit")
	}
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if keys[normalized(k)] {
				x[k] = Filtered
			} else if err := redactValue(child, keys, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range x {
			if err := redactValue(child, keys, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
func cleanText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) > max {
		s = strings.ToValidUTF8(s[:max], "")
	}
	return s
}
func cleanHeaders(headers map[string][]string, keys []string) map[string][]string {
	if headers == nil {
		return nil
	}
	out := make(map[string][]string)
	mask := keySet(keys)
	remaining := maxMetadataBytes
	for key, values := range headers {
		if remaining <= 0 || len(out) >= 128 {
			break
		}
		key = cleanText(key, 256)
		if mask[normalized(key)] {
			out[key] = []string{Filtered}
			remaining -= len(key) + len(Filtered)
			continue
		}
		for _, value := range values {
			if remaining <= len(key) {
				break
			}
			value = cleanText(value, min(4096, remaining-len(key)))
			out[key] = append(out[key], value)
			remaining -= len(key) + len(value)
		}
	}
	return out
}
func cleanURL(raw string, keys []string) string {
	if len(raw) > 8192 {
		return "[URL omitted: size limit]"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "[URL omitted: invalid]"
	}
	u.User = nil
	u.Fragment = ""
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		u.RawQuery = ""
		return cleanText(u.String(), 8192)
	}
	mask := keySet(keys)
	for key := range q {
		if mask[normalized(key)] {
			q[key] = []string{Filtered}
		}
	}
	u.RawQuery = q.Encode()
	return cleanText(u.String(), 8192)
}
func contentType(value string) string {
	v, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(v)
}
func jsonContentType(value string) bool {
	return value == "application/json" || strings.HasSuffix(value, "+json")
}
func (l *Logger) sanitizeBody(b Body, limit int, keys []string) Body {
	b.ContentType = cleanText(b.ContentType, 256)
	if b.State != "complete" {
		b.Data = nil
		return b
	}
	if len(b.Data) > limit {
		b.Data = nil
		b.State = "oversized"
		return b
	}
	if len(b.Data) == 0 {
		b.State = "empty"
		return b
	}
	var out []byte
	var err error
	if b.Encoding == "json" || jsonContentType(contentType(b.ContentType)) {
		out, err = SanitizeJSON(b.Data, keys)
	} else if l.config.Sanitizer != nil {
		out, err = safeSanitizer(l.config.Sanitizer, b.ContentType, b.Data, keys)
		// Custom formats return sanitized JSON so all sinks have one safe format.
		if err == nil {
			out, err = SanitizeJSON(out, keys)
		}
	} else {
		b.Data = nil
		b.State = "unsupported"
		return b
	}
	if err != nil {
		b.Data = nil
		b.State = "invalid"
		return b
	}
	if len(out) > limit {
		b.Data = nil
		b.State = "oversized"
		return b
	}
	b.Data = out
	b.Encoding = "json"
	return b
}
func safeSanitizer(f Sanitizer, ct string, data []byte, keys []string) (out []byte, err error) {
	defer func() {
		if recover() != nil {
			out = nil
			err = errors.New("apilog: sanitizer failed")
		}
	}()
	return f(ct, data, keys)
}
func (l *Logger) sanitize(e Event, decision Decision) Event {
	keys := append(append([]string{}, l.config.MaskKeys...), decision.MaskKeys...)
	e.Version = SchemaVersion
	e.Method = cleanText(e.Method, 32)
	e.Path = cleanText(e.Path, 8192)
	e.Route = cleanText(e.Route, 512)
	if e.Route == "" {
		e.Route = "unresolved"
	}
	e.Name = cleanText(e.Name, 256)
	e.Group = cleanText(e.Group, 256)
	e.Handler = cleanText(e.Handler, 256)
	e.Protocol = cleanText(e.Protocol, 32)
	e.ClientIP = cleanText(e.ClientIP, 128)
	e.URL = cleanURL(e.URL, keys)
	if decision.StripQuery {
		e.URL = cleanURL(e.Path, keys)
	}
	e.RequestID = cleanText(e.RequestID, 128)
	e.TraceID = cleanText(e.TraceID, 128)
	e.RequestHeaders = cleanHeaders(e.RequestHeaders, keys)
	e.ResponseHeaders = cleanHeaders(e.ResponseHeaders, keys)
	if decision.StripHeaders {
		e.RequestHeaders = nil
		e.ResponseHeaders = nil
	}
	if decision.StripRequest {
		e.Request.Data = nil
		e.Request.State = "omitted"
	}
	if decision.StripResponse {
		e.Response.Data = nil
		e.Response.State = "omitted"
	}
	e.Request = l.sanitizeBody(e.Request, l.config.RequestBodyLimit, keys)
	e.Response = l.sanitizeBody(e.Response, l.config.ResponseBodyLimit, keys)
	contextData := make(map[string]string)
	mask := keySet(keys)
	for k, v := range e.Context {
		if len(contextData) >= 32 {
			break
		}
		k = cleanText(k, 64)
		if mask[normalized(k)] {
			v = Filtered
		}
		contextData[k] = cleanText(v, 256)
	}
	e.Context = contextData
	return e
}
