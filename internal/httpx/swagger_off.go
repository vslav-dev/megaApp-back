//go:build !swagger

package httpx

import "github.com/go-chi/chi/v5"

// registerSwagger does nothing without `-tags swagger`: the prod build carries no spec, no UI page
// and no route, so there is nothing to disable or protect.
func registerSwagger(chi.Router) {}
