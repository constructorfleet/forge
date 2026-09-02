package linear

import (
	"context"
	"errors"
	"testing"
)

func TestResolveInternalIDReturnsUUIDForIdentifier(t *testing.T) {
	srv := fakeLinearServer(t, map[string]string{
		"issues(filter": `{"data":{"issues":{"nodes":[{"id":"uuid-42","identifier":"FOR-42"}]}}}`,
	})
	defer srv.Close()
	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	got, err := c.resolveInternalID(context.Background(), "FOR-42")
	if err != nil {
		t.Fatalf("resolveInternalID: %v", err)
	}
	if got != "uuid-42" {
		t.Fatalf("got %q, want uuid-42", got)
	}
}

func TestResolveInternalIDNotFound(t *testing.T) {
	srv := fakeLinearServer(t, map[string]string{
		"issues(filter": `{"data":{"issues":{"nodes":[]}}}`,
	})
	defer srv.Close()
	t.Setenv(tokenEnvVar, "k")
	c := NewClient(nil, srv.URL, "FOR")

	_, err := c.resolveInternalID(context.Background(), "FOR-999")
	var notFound *NotFoundError
	if err == nil || !errors.As(err, &notFound) {
		t.Fatalf("err = %v (%T), want *NotFoundError", err, err)
	}
}
