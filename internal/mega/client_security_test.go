package mega

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

type failingRoundTripper func(*http.Request) (*http.Response, error)

func (f failingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestCommandTransportErrorDoesNotExposeAccountSessionURL(t *testing.T) {
	const session = "account-session-secret"
	client := NewClient(&http.Client{Transport: failingRoundTripper(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New(request.URL.String())
	})}, "https://g.api.mega.test")
	client = client.WithSession(session)

	_, err := client.ResolveLink(context.Background(), "https://mega.nz/file/file0001#AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA", "")
	if err == nil {
		t.Fatal("ResolveLink unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), session) || strings.Contains(err.Error(), "sid=") {
		t.Fatalf("transport error exposed authenticated command URL: %v", err)
	}
}
