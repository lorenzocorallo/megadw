package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lorenzocorallo/megadw/internal/auth"
)

type deadlineResponseWriter struct {
	header      http.Header
	deadlineSet bool
}

func (w *deadlineResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (*deadlineResponseWriter) WriteHeader(int) {}
func (*deadlineResponseWriter) Flush()          {}
func (*deadlineResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("simulated disconnected SSE peer")
}
func (w *deadlineResponseWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlineSet = !deadline.IsZero()
	return nil
}

func TestSSEWritesInstallAPerWriteDeadline(t *testing.T) {
	server := NewServer(Config{})
	writer := &deadlineResponseWriter{}
	request := httptest.NewRequest(http.MethodGet, "http://example.test/api/v1/events", nil)
	server.handleEvents(writer, request, auth.Principal{})
	if !writer.deadlineSet {
		t.Fatal("SSE handler wrote without installing a deadline")
	}
}
