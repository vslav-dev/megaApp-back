//go:build swagger

package httpx

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestSwaggerServesUIAndSpec(t *testing.T) {
	app := newSwaggerTestApp(t)

	page := getFromApp(app, "/api/swagger")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "SwaggerUIBundle") {
		t.Fatalf("GET /api/swagger status = %d, body has no SwaggerUIBundle", page.Code)
	}

	spec := getFromApp(app, "/api/swagger/openapi.yaml")
	if spec.Code != http.StatusOK || !strings.HasPrefix(spec.Body.String(), "openapi: 3.0.3") {
		t.Fatalf("GET /api/swagger/openapi.yaml status = %d, body is not the spec", spec.Code)
	}
}

// The spec is written by hand, so this is what keeps it honest: every public route must be
// documented and every documented operation must still exist.
func TestOpenAPISpecMatchesRoutes(t *testing.T) {
	app := newSwaggerTestApp(t)

	routes := map[string]bool{}
	err := chi.Walk(app.Handler.(chi.Routes), func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !isUndocumentedRoute(route) {
			routes[method+" "+route] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk() error = %v", err)
	}

	documented := documentedOperations()
	for operation := range routes {
		if !documented[operation] {
			t.Errorf("route %q is missing in openapi.yaml", operation)
		}
	}
	for operation := range documented {
		if !routes[operation] {
			t.Errorf("openapi.yaml documents %q, no such route", operation)
		}
	}
}

// isUndocumentedRoute lists what the spec leaves out on purpose, see its description.
func isUndocumentedRoute(route string) bool {
	if route == "/api/debug/commit-info" {
		return false
	}
	for _, prefix := range []string{"/api/debug/", "/api/lab/", "/api/ingest/", "/api/swagger", "/api/ws"} {
		if strings.HasPrefix(route, prefix) {
			return true
		}
	}
	return false
}

var (
	specPathPattern   = regexp.MustCompile(`^  (/\S*):$`)
	specMethodPattern = regexp.MustCompile(`^    (get|post|put|delete):$`)
)

func documentedOperations() map[string]bool {
	operations := map[string]bool{}
	path := ""
	for _, line := range strings.Split(string(openAPISpec), "\n") {
		if match := specPathPattern.FindStringSubmatch(line); match != nil {
			path = match[1]
		} else if match := specMethodPattern.FindStringSubmatch(line); match != nil {
			operations[strings.ToUpper(match[1])+" "+path] = true
		}
	}
	return operations
}
