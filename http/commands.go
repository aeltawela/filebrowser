package fbhttp

import (
	"bufio"
	"io"
	"log"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/filebrowser/filebrowser/v2/runner"
)

const (
	WSWriteDeadline = 10 * time.Second

	// commandReadDeadline bounds how long a client may hold the socket open
	// before sending its first command.
	commandReadDeadline = 30 * time.Second

	// maxCommandMessageBytes bounds a single inbound WebSocket message. Without
	// a limit gorilla/websocket buffers the complete message before the handler
	// can reject it, so an unauthorized client could make the process allocate
	// an arbitrary amount of memory.
	maxCommandMessageBytes = 64 << 10

	// maxCommandOutputLine bounds one line of command output. Output is still
	// relayed line by line, so a single unbounded line must not stall or
	// exhaust the scanner.
	maxCommandOutputLine = 1 << 20
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

var (
	cmdNotAllowed = []byte("Command not allowed.")
)

func wsErr(ws *websocket.Conn, r *http.Request, status int, err error) {
	txt := http.StatusText(status)
	if err != nil || status >= 400 {
		log.Printf("%s: %v %s %v", r.URL.Path, status, r.RemoteAddr, err)
	}
	if err := ws.WriteControl(websocket.CloseInternalServerErr, []byte(txt), time.Now().Add(WSWriteDeadline)); err != nil {
		log.Print(err)
	}
}

var commandsHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	defer conn.Close()

	// Bound every read before the first one, including the rejection path
	// below. A message above the limit closes the connection with 1009 instead
	// of being buffered.
	conn.SetReadLimit(maxCommandMessageBytes)

	// Fail fast, before reading anything. The route is registered even when the
	// feature is disabled, so an authenticated user without Execute permission
	// must not be able to make the process buffer a payload.
	if !d.server.EnableExec || !d.user.Perm.Execute {
		msg := websocket.FormatCloseMessage(websocket.ClosePolicyViolation, string(cmdNotAllowed))
		_ = conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(WSWriteDeadline))
		return 0, nil
	}

	if err := conn.SetReadDeadline(time.Now().Add(commandReadDeadline)); err != nil {
		wsErr(conn, r, http.StatusInternalServerError, err)
		return 0, nil
	}

	var raw string

	for {
		messageType, msg, err := conn.ReadMessage()
		if err != nil {
			wsErr(conn, r, http.StatusInternalServerError, err)
			return 0, nil
		}

		if messageType != websocket.TextMessage {
			if err := conn.WriteMessage(websocket.TextMessage, []byte("Only text commands are accepted.")); err != nil {
				wsErr(conn, r, http.StatusInternalServerError, err)
			}
			return 0, nil
		}

		raw = strings.TrimSpace(string(msg))
		if raw != "" {
			break
		}
	}

	// The command has been received; output may legitimately take longer than
	// the inbound read timeout.
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		wsErr(conn, r, http.StatusInternalServerError, err)
		return 0, nil
	}

	command, name, err := runner.ParseCommand(d.settings, raw)
	if err != nil {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(err.Error())); err != nil {
			wsErr(conn, r, http.StatusInternalServerError, err)
		}
		return 0, nil
	}

	if !slices.Contains(d.user.Commands, name) {
		if err := conn.WriteMessage(websocket.TextMessage, cmdNotAllowed); err != nil {
			wsErr(conn, r, http.StatusInternalServerError, err)
		}

		return 0, nil
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = d.user.FullPath(r.URL.Path)

	// Merge stdout and stderr through one pipe and drain it while the process
	// runs. Reading the two pipes in sequence can deadlock when the child fills
	// the stderr pipe while the parent is blocked on stdout.
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		wsErr(conn, r, http.StatusInternalServerError, err)
		return 0, nil
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
		_ = pw.Close()
	}()

	s := bufio.NewScanner(pr)
	s.Buffer(make([]byte, 64*1024), maxCommandOutputLine)
	for s.Scan() {
		_ = conn.SetWriteDeadline(time.Now().Add(WSWriteDeadline))
		if err := conn.WriteMessage(websocket.TextMessage, s.Bytes()); err != nil {
			log.Print(err)
			// The client is gone; stop the process instead of leaving it
			// blocked on a full pipe.
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			break
		}
	}
	if err := s.Err(); err != nil {
		log.Print(err)
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}

	if err := <-waitCh; err != nil {
		wsErr(conn, r, http.StatusInternalServerError, err)
	}

	return 0, nil
})
