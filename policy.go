package apilog

import "strings"

func contains[T comparable](items []T, value T) bool {
	for _, x := range items {
		if x == value {
			return true
		}
	}
	return false
}
func mergeDecision(a, b Decision) Decision {
	a.Skip = a.Skip || b.Skip
	a.StripHeaders = a.StripHeaders || b.StripHeaders
	a.StripRequest = a.StripRequest || b.StripRequest
	a.StripResponse = a.StripResponse || b.StripResponse
	a.DisableStorage = a.DisableStorage || b.DisableStorage
	a.DisableExport = a.DisableExport || b.DisableExport
	a.StripQuery = a.StripQuery || b.StripQuery
	a.MaskKeys = append(append([]string{}, a.MaskKeys...), b.MaskKeys...)
	return a
}
func matches(r Rule, e Event, final bool) bool {
	if !final && (len(r.Statuses) > 0 || len(r.StatusClasses) > 0) {
		return false
	}
	return (r.Route == "" || r.Route == e.Route) && (r.Name == "" || r.Name == e.Name) && (r.Group == "" || r.Group == e.Group) && (r.Handler == "" || r.Handler == e.Handler) && (r.PathPrefix == "" || strings.HasPrefix(e.Path, r.PathPrefix)) && (len(r.Methods) == 0 || contains(r.Methods, e.Method)) && (len(r.Statuses) == 0 || contains(r.Statuses, e.Status)) && (len(r.StatusClasses) == 0 || contains(r.StatusClasses, e.Status/100))
}
func policyFailureDecision() Decision {
	return Decision{StripHeaders: true, StripRequest: true, StripResponse: true, StripQuery: true, DisableExport: true}
}

// policyFailed distinguishes a broken callback from an intentional decision so
// an exchange can retain failure restrictions until its final emission.
func (l *Logger) decision(e Event, final bool) (d Decision, policyFailed bool) {
	d.StripRequest = l.config.MetadataOnly
	d.StripResponse = l.config.MetadataOnly
	if len(l.config.Methods) > 0 && !contains(l.config.Methods, e.Method) {
		d.Skip = true
	}
	if final && len(l.config.Statuses) > 0 && !contains(l.config.Statuses, e.Status) {
		d.Skip = true
	}
	if contains(l.config.SkipRoutes, e.Route) || contains(l.config.SkipNames, e.Name) || contains(l.config.SkipGroups, e.Group) {
		d.Skip = true
	}
	for _, prefix := range l.config.SkipPaths {
		if e.Path == prefix || strings.HasPrefix(e.Path, strings.TrimSuffix(prefix, "/")+"/") {
			d.Skip = true
		}
	}
	for _, r := range l.config.Rules {
		if matches(r, e, final) {
			d = mergeDecision(d, r.Decision)
		}
	}
	if l.config.Policy != nil {
		var p Decision
		// A nil panic can recover as nil when GODEBUG=panicnil=1. Only a
		// normal successful return may authorize retaining protected data.
		failed := true
		func() {
			defer func() { _ = recover() }()
			var err error
			p, err = l.config.Policy(e)
			failed = err != nil
		}()
		if failed {
			p = policyFailureDecision()
			policyFailed = true
		}
		d = mergeDecision(d, p)
	}
	return d, policyFailed
}
