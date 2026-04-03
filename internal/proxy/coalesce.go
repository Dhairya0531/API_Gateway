package proxy

import (
	"net/http"
	"net/http/httptest"

	"golang.org/x/sync/singleflight"
)

// Coalescer prevents the "thundering herd" problem by coalescing concurrent
// identical requests into a single upstream request.
//
// If 100 clients request GET /users/123 simultaneously, the Coalescer ensures
// only 1 request goes to the upstream. The response is shared with all 100 clients.
type Coalescer struct {
	group singleflight.Group
}

func NewCoalescer() *Coalescer {
	return &Coalescer{}
}

// Do executes a function ensuring only one execution per key runs concurrently.
// This is a thin wrapper around singleflight.Group that handles HTTP responses.
//
// For this to work with HTTP, the fn must return a fully buffered response
// because the response body can only be read once.
func (c *Coalescer) Do(r *http.Request, fn func() (*httptest.ResponseRecorder, error)) (*httptest.ResponseRecorder, bool, error) {
	// Only coalesce safe methods (GET, HEAD, OPTIONS)
	if !isSafeMethod(r.Method) {
		rec, err := fn()
		return rec, false, err
	}

	// Cache key: HTTP Method + URL Path + RawQuery
	// We don't include headers because it would defeat coalescing if clients
	// have different User-Agents or tokens.
	// NOTE: If responses vary by token/auth, the key MUST include the identity.
	// We assume Auth middleware handles authorization BEFORE coalescing.
	key := r.Method + ":" + r.URL.Path
	if r.URL.RawQuery != "" {
		key += "?" + r.URL.RawQuery
	}

	v, err, shared := c.group.Do(key, func() (interface{}, error) {
		// Only one goroutine executes this per key
		return fn()
	})

	if err != nil {
		return nil, shared, err
	}

	rec := v.(*httptest.ResponseRecorder)
	return rec, shared, nil
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}
