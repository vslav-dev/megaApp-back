//go:build swagger

package httpx

import (
	_ "embed"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Compiled only with `-tags swagger` (the test deploy). A build without the tag has neither the
// spec nor the UI page in the binary, see swagger_off.go.

//go:embed openapi.yaml
var openAPISpec []byte

// The page and the spec share the origin of the API, so the session cookie from /api/auth/login
// is sent by "Try it out" and the same-origin check of unsafe methods passes.
const swaggerUIPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>MegaApp API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css"
        integrity="sha384-wxLW6kwyHktdDGr6Pv1zgm/VGJh99lfUbzSn6HNHBENZlCN7W602k9VkGdxuFvPn" crossorigin="anonymous">
</head>
<body>
  <div id="ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"
          integrity="sha384-wmyclcVGX/WhUkdkATwhaK1X1JtiNrr2EoYJ+diV3vj4v6OC5yCeSu+yW13SYJep" crossorigin="anonymous"></script>
  <script>
    SwaggerUIBundle({
      url: '/api/swagger/openapi.yaml',
      dom_id: '#ui',
      deepLinking: true,
      persistAuthorization: true,
    });
  </script>
</body>
</html>
`

func registerSwagger(router chi.Router) {
	router.Get("/api/swagger", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(swaggerUIPage))
	})
	router.Get("/api/swagger/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(openAPISpec)
	})
}
