//go:build !swagger

package httpx

import (
	"net/http"
	"testing"
)

// The prod build must not know the swagger routes at all: a 404 here is the guarantee the deploy
// of the release branch (built without the tag) exposes no API documentation.
func TestSwaggerIsAbsentWithoutBuildTag(t *testing.T) {
	app := newSwaggerTestApp(t)

	for _, path := range []string{"/api/swagger", "/api/swagger/openapi.yaml"} {
		if status := getFromApp(app, path).Code; status != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, status)
		}
	}
}
