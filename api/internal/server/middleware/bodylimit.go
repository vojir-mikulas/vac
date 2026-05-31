package middleware

import "net/http"

// MaxBodyBytes is the default request-body cap for /api routes. 1 MiB is well
// above any legitimate JSON payload VAC accepts.
const MaxBodyBytes = 1 << 20

// BodyLimit wraps r.Body in http.MaxBytesReader so a multi-GB POST cannot OOM
// the process. Decoders that read past the cap will see an error rather than
// the server allocating unbounded memory.
func BodyLimit(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}
