package remote

import (
	"net/http"
	"time"
)

type AuditEvent struct {
	Code       string `json:"code,omitempty"`
	RequestID  string `json:"request_id"`
	Operation  string `json:"operation"`
	Status     int    `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Principal  string `json:"principal"`
	Snapshot   string `json:"snapshot"`
}
type auditContext struct{}
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (s *Server) audit(w *statusWriter, event *AuditEvent, started time.Time) {
	if s.config.Audit == nil {
		return
	}
	event.Status = w.status
	if event.Status == 0 {
		event.Status = 200
	}
	event.DurationMS = time.Since(started).Milliseconds()
	s.config.Audit(*event)
}
