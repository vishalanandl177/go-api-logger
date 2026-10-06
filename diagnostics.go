package apilog

type Diagnostic struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Diagnose checks this live logger instance. It does not query application data,
// change schemas, inspect payloads, or start a metrics endpoint.
func (l *Logger) Diagnose() []Diagnostic {
	h := l.Health()
	findings := []Diagnostic{{"OK", "configuration", "Configuration passed constructor validation"}}
	if h.Closed {
		findings = append(findings, Diagnostic{"WARNING", "closed", "Logger shutdown has started"})
	}
	if len(h.Outputs) == 0 {
		findings = append(findings, Diagnostic{"OK", "no_outputs", "No persistence or subscriber outputs configured"})
	}
	for _, o := range h.Outputs {
		if !h.Closed && !o.WorkerRunning {
			findings = append(findings, Diagnostic{"ERROR", "worker_stopped", "An output worker is not running"})
		}
		if o.Dropped > 0 {
			findings = append(findings, Diagnostic{"WARNING", "events_dropped", "Events were dropped; inspect output health and queue capacity"})
		}
		if o.Failed > 0 {
			findings = append(findings, Diagnostic{"WARNING", "writes_failed", "Output writes failed; inspect storage configuration and availability"})
		}
		if o.Queued+o.InFlight >= l.config.Queue.Capacity || o.QueuedBytes >= l.config.Queue.MaxBytes {
			findings = append(findings, Diagnostic{"WARNING", "queue_full", "An output queue reached its configured capacity"})
		}
	}
	return findings
}
