// Package httpx holds HTTP helpers the API and the gateway share.
package httpx

import (
	"errors"
	"io"
	"net/http"
	"os"
	"time"
)

// BodyDeadline gives each request that has a body timeout(r) to send all of it, so a body
// trickled in a byte at a time can't hold a connection: not in a handler reading it, and
// not in net/http discarding what a handler left unread.
//
// The deadline is lifted once the body has been read to the end. Left in place, it would
// fire while the handler is still working (a config apply, a streamed answer) and net/http
// would cancel the request. Requests without a body get no deadline for the same reason.
func BodyDeadline(next http.Handler, timeout func(*http.Request) time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 0 { // -1 is a body of unknown length
			rc := http.NewResponseController(w)
			// A writer without deadlines (httptest.ResponseRecorder) just reads without one.
			if rc.SetReadDeadline(time.Now().Add(timeout(r))) == nil {
				r.Body = &deadlineBody{ReadCloser: r.Body, rc: rc}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// deadlineBody lifts the read deadline when the body has been read to the end.
type deadlineBody struct {
	io.ReadCloser
	rc *http.ResponseController
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.rc.SetReadDeadline(time.Time{})
	}
	return n, err
}

// TimedOut reports whether a body read failed because its deadline passed.
func TimedOut(err error) bool {
	return errors.Is(err, os.ErrDeadlineExceeded)
}
