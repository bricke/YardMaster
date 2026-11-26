package httpx

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const timeout = 200 * time.Millisecond

// serve runs handler behind BodyDeadline in a real server, so the deadline applies to a
// real connection.
func serve(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(BodyDeadline(handler, func(*http.Request) time.Duration { return timeout }))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// readThenWait reads the body, then works well past the deadline before answering, as a
// config apply or a streamed answer would.
func readThenWait(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if TimedOut(err) {
			http.Error(w, "timed out", http.StatusRequestTimeout)
			return
		}
		select {
		case <-time.After(3 * timeout):
		case <-r.Context().Done():
			t.Error("the request was cancelled after its body was read")
			return
		}
		fmt.Fprintf(w, "read %q", body)
	}
}

// send writes a request whose body is sent as parts, pausing between them, and returns
// the response's status line and how long it took to arrive.
func send(t *testing.T, addr, method string, contentLength int, pause time.Duration, parts ...string) (string, time.Duration) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	start := time.Now()
	fmt.Fprintf(conn, "%s / HTTP/1.1\r\nHost: test\r\nContent-Length: %d\r\n\r\n", method, contentLength)
	for i, p := range parts {
		if i > 0 {
			time.Sleep(pause)
		}
		conn.Write([]byte(p))
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("no response: %v", err)
	}
	return strings.TrimSpace(status), time.Since(start)
}

func TestSlowBodyTimesOut(t *testing.T) {
	addr := serve(t, readThenWait(t))
	// Half the body, then nothing: the deadline must end the wait.
	if got, _ := send(t, addr, "POST", 10, 0, "hello"); got != "HTTP/1.1 408 Request Timeout" {
		t.Fatalf("got %q, want 408", got)
	}
}

func TestDeadlineIsLiftedOnceTheBodyIsRead(t *testing.T) {
	addr := serve(t, readThenWait(t))
	if got, _ := send(t, addr, "POST", 10, timeout/4, "hello", "world"); got != "HTTP/1.1 200 OK" {
		t.Fatalf("got %q, want 200", got)
	}
}

func TestNoDeadlineWithoutABody(t *testing.T) {
	addr := serve(t, readThenWait(t))
	if got, _ := send(t, addr, "GET", 0, 0); got != "HTTP/1.1 200 OK" {
		t.Fatalf("got %q, want 200", got)
	}
}

// A handler that answers without reading the body, as a 401 does: net/http then reads
// what's left before replying, and the deadline must bound that too.
func TestUnreadBodyDoesntHoldTheConnection(t *testing.T) {
	addr := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	})
	got, took := send(t, addr, "POST", 1<<10, 0, "x")
	if got != "HTTP/1.1 401 Unauthorized" || took > 2*time.Second {
		t.Fatalf("got %q after %v, want 401 within about %v", got, took, timeout)
	}
}
