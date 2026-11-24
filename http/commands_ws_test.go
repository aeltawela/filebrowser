package fbhttp

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/users"
)

type commandTestServer struct {
	srv   *httptest.Server
	token string
}

// newCommandTestServer mounts the real /api/command handler behind a real HTTP
// server so the WebSocket upgrade, authentication, and read limit are exercised.
func newCommandTestServer(t *testing.T, perm users.Permissions, commands []string, enableExec bool) *commandTestServer {
	t.Helper()

	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	if err := os.MkdirAll(userScope, 0o755); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	st := scopedUserStorage(t, userScope, perm, key)

	u, err := st.Users.Get("", false, uint(1))
	if err != nil {
		t.Fatalf("failed to load test user: %v", err)
	}
	u.Commands = commands
	if err := st.Users.Update(u, "Commands"); err != nil {
		t.Fatalf("failed to store test commands: %v", err)
	}

	server := &settings.Server{EnableExec: enableExec}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/command/", func(w http.ResponseWriter, r *http.Request) {
		handle(commandsHandler, "/api/command", st, server).ServeHTTP(w, r)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &commandTestServer{
		srv:   srv,
		token: signToken(t, perm, key),
	}
}

func (s *commandTestServer) dial(t *testing.T) *websocket.Conn {
	t.Helper()

	url := "ws" + s.srv.URL[len("http"):] + "/api/command/"
	conn, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"X-Auth": {s.token}})
	if err != nil {
		if resp != nil {
			t.Fatalf("failed to dial websocket: %v (status %d)", err, resp.StatusCode)
		}
		t.Fatalf("failed to dial websocket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

func readCloseCode(t *testing.T, conn *websocket.Conn) int {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatal("expected the server to close the connection")
	}

	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		t.Fatalf("expected a close error, got %v", err)
	}
	return closeErr.Code
}

// Regression for GHSA-39cx-23x9-5c8p: a message larger than the read limit must
// close the connection instead of being buffered in full.
func TestCommandWSRejectsOversizedMessage(t *testing.T) {
	perm := users.Permissions{Execute: true, Download: true}
	ts := newCommandTestServer(t, perm, []string{"echo"}, true)
	conn := ts.dial(t)

	payload := bytes.Repeat([]byte("A"), maxCommandMessageBytes+1)
	// The write can fail once the server closes the socket mid-frame; the
	// assertion is on the close code, not on the write succeeding.
	_ = conn.WriteMessage(websocket.TextMessage, payload)

	if code := readCloseCode(t, conn); code != websocket.CloseMessageTooBig {
		t.Fatalf("expected close code %d (message too big), got %d", websocket.CloseMessageTooBig, code)
	}
}

// Regression for GHSA-39cx-23x9-5c8p: authorization is checked before any read,
// so a user without Execute permission is disconnected without sending a
// payload at all.
func TestCommandWSRequiresPermissionBeforeRead(t *testing.T) {
	perm := users.Permissions{Download: true}
	ts := newCommandTestServer(t, perm, []string{"echo"}, true)
	conn := ts.dial(t)

	if code := readCloseCode(t, conn); code != websocket.ClosePolicyViolation {
		t.Fatalf("expected close code %d (policy violation), got %d", websocket.ClosePolicyViolation, code)
	}
}

// Disabling the feature globally must reject the connection the same way.
func TestCommandWSDisabledRejectsBeforeRead(t *testing.T) {
	perm := users.Permissions{Execute: true, Download: true}
	ts := newCommandTestServer(t, perm, []string{"echo"}, false)
	conn := ts.dial(t)

	if code := readCloseCode(t, conn); code != websocket.ClosePolicyViolation {
		t.Fatalf("expected close code %d (policy violation), got %d", websocket.ClosePolicyViolation, code)
	}
}

// The normal path must keep working after the ordering change.
func TestCommandWSExecutesAllowedCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX echo binary")
	}

	perm := users.Permissions{Execute: true, Download: true}
	ts := newCommandTestServer(t, perm, []string{"echo"}, true)
	conn := ts.dial(t)

	if err := conn.WriteMessage(websocket.TextMessage, []byte("echo hello")); err != nil {
		t.Fatalf("failed to send command: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read output: %v", err)
	}
	if string(msg) != "hello" {
		t.Fatalf("expected hello, got %q", string(msg))
	}
}
