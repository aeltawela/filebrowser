package fbhttp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/storage"
	"github.com/filebrowser/filebrowser/v2/users"
)

// newTusTestServer mounts the TUS handlers behind a real HTTP server, under the
// same "/api/tus" prefix used in production, so tests exercise connection
// handling and header round-tripping instead of a bare ResponseRecorder.
func newTusTestServer(t *testing.T, st *storage.Storage, cache UploadCache) *httptest.Server {
	t.Helper()

	server := &settings.Server{}
	post := handle(tusPostHandler(cache), "/api/tus", st, server)
	head := handle(tusHeadHandler(cache), "/api/tus", st, server)
	patch := handle(tusPatchHandler(cache), "/api/tus", st, server)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tus/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			post.ServeHTTP(w, r)
		case http.MethodHead:
			head.ServeHTTP(w, r)
		case http.MethodPatch:
			patch.ServeHTTP(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// tusTestFixture wires a scoped user, an upload cache and a live server.
type tusTestFixture struct {
	srv    *httptest.Server
	client *http.Client
	token  string
	scope  string
}

func newTusTestFixture(t *testing.T) *tusTestFixture {
	t.Helper()

	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	if err := os.MkdirAll(userScope, 0o755); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Modify: true}
	st := scopedUserStorage(t, userScope, perm, key)

	cache := newMemoryUploadCache()
	t.Cleanup(cache.Close)

	srv := newTusTestServer(t, st, cache)
	return &tusTestFixture{
		srv:    srv,
		client: srv.Client(),
		token:  signToken(t, perm, key),
		scope:  userScope,
	}
}

// create sends the TUS creation request and returns the absolute upload URL.
func (f *tusTestFixture) create(t *testing.T, name string, length int) string {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/tus/"+name+"?override=false", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Auth", f.token)
	req.Header.Set("Upload-Length", strconv.Itoa(length))

	res, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("POST create: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)

	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST create: status %d body %q", res.StatusCode, body)
	}
	location := res.Header.Get("Location")
	if location == "" {
		t.Fatal("POST create: missing Location header")
	}
	return f.srv.URL + location
}

// patch sends one chunk and reports the response plus whether the underlying
// connection was reused from the pool.
func (f *tusTestFixture) patch(t *testing.T, url string, offset int, chunk []byte) (*http.Response, bool) {
	t.Helper()

	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(chunk))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Auth", f.token)
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", strconv.Itoa(offset))

	var reused bool
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))

	res, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("PATCH at offset %d: %v", offset, err)
	}
	return res, reused
}

func testPayload(size int) []byte {
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	return payload
}

// A file larger than the chunk size must upload across several PATCH requests
// over a single reused connection, exactly as the web UI drives it. Each chunk
// has to be acknowledged with 204 and the new Upload-Offset, and the resulting
// file must be byte-identical to the source.
func TestTusMultiChunkUpload(t *testing.T) {
	const chunkSize = 64 * 1024
	const fileSize = chunkSize*3 + 1234

	f := newTusTestFixture(t)
	payload := testPayload(fileSize)
	uploadURL := f.create(t, "movie.bin", fileSize)

	for offset := 0; offset < fileSize; {
		end := min(offset+chunkSize, fileSize)

		res, _ := f.patch(t, uploadURL, offset, payload[offset:end])
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()

		if res.StatusCode != http.StatusNoContent {
			t.Fatalf("PATCH at offset %d: status %d body %q", offset, res.StatusCode, body)
		}
		if got, want := res.Header.Get("Upload-Offset"), strconv.Itoa(end); got != want {
			t.Fatalf("PATCH at offset %d: Upload-Offset = %q, want %q", offset, got, want)
		}

		offset = end
	}

	got, err := os.ReadFile(filepath.Join(f.scope, "movie.bin"))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("uploaded file differs from source (got %d bytes, want %d)", len(got), len(payload))
	}
}

// Between chunks a client may re-synchronise with a HEAD request. It must
// report the bytes already on disk plus the declared total, so the client can
// resume from the right offset.
func TestTusHeadReportsOffsetBetweenChunks(t *testing.T) {
	const chunkSize = 32 * 1024
	const fileSize = chunkSize * 2

	f := newTusTestFixture(t)
	payload := testPayload(fileSize)
	uploadURL := f.create(t, "resume.bin", fileSize)

	res, _ := f.patch(t, uploadURL, 0, payload[:chunkSize])
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("first PATCH: status %d", res.StatusCode)
	}

	req, err := http.NewRequest(http.MethodHead, uploadURL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Auth", f.token)

	headRes, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	defer headRes.Body.Close()

	if headRes.StatusCode != http.StatusOK {
		t.Fatalf("HEAD: status %d", headRes.StatusCode)
	}
	if got, want := headRes.Header.Get("Upload-Offset"), strconv.Itoa(chunkSize); got != want {
		t.Fatalf("HEAD Upload-Offset = %q, want %q", got, want)
	}
	if got, want := headRes.Header.Get("Upload-Length"), strconv.Itoa(fileSize); got != want {
		t.Fatalf("HEAD Upload-Length = %q, want %q", got, want)
	}
}

// A PATCH that the handler rejects before reading the body must still leave the
// connection usable. Go abandons draining an unread request body after 256 KiB
// and then closes the socket, which turns a plain 409 into an opaque transport
// error for the client and stalls the upload. Rejecting a chunk-sized body must
// not cost the connection.
func TestTusPatchRejectionKeepsConnectionUsable(t *testing.T) {
	const chunkSize = 1024 * 1024
	const fileSize = chunkSize * 2

	f := newTusTestFixture(t)
	payload := testPayload(fileSize)
	uploadURL := f.create(t, "conflict.bin", fileSize)

	// Land the first chunk so the file size no longer matches offset 0.
	res, _ := f.patch(t, uploadURL, 0, payload[:chunkSize])
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("first PATCH: status %d", res.StatusCode)
	}

	// Replaying offset 0 with a full chunk must be answered with a real 409.
	res, _ = f.patch(t, uploadURL, 0, payload[:chunkSize])
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("stale-offset PATCH: status %d body %q, want 409", res.StatusCode, body)
	}

	// And the connection must survive it, so the client's next attempt is not
	// reported as a network failure.
	res, reused := f.patch(t, uploadURL, chunkSize, payload[chunkSize:])
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("PATCH after rejection: status %d body %q", res.StatusCode, body)
	}
	if !reused {
		t.Fatal("connection was dropped by the rejected PATCH instead of being reused")
	}

	got, err := os.ReadFile(filepath.Join(f.scope, "conflict.bin"))
	if err != nil {
		t.Fatalf("read uploaded file: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("uploaded file differs from source (got %d bytes, want %d)", len(got), len(payload))
	}
}

// Same requirement for a rejection raised once the write is already underway:
// an over-length body is refused after part of it has been read, and the rest
// still has to be drained for the answer to survive.
func TestTusOverLengthPatchKeepsConnectionUsable(t *testing.T) {
	const declared = 1024

	f := newTusTestFixture(t)
	payload := testPayload(4 << 20)
	uploadURL := f.create(t, "toolong.bin", declared)

	res, _ := f.patch(t, uploadURL, 0, payload)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-length PATCH: status %d body %q, want 413", res.StatusCode, body)
	}

	res, reused := f.patch(t, uploadURL, 0, payload[:declared])
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("PATCH after rejection: status %d body %q", res.StatusCode, body)
	}
	if !reused {
		t.Fatal("connection was dropped by the over-length PATCH instead of being reused")
	}
}

// A Create-only user can complete a TUS upload to a new path: writing the
// upload body is the TUS equivalent of a Create-gated POST-new, so requiring
// Modify on PATCH would strand legitimate create-only uploads. Overwriting an
// existing file still requires Modify (enforced at override-POST time).
func TestTUSCreateOnlyUserCanCompleteNewUpload(t *testing.T) {
	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	if err := os.MkdirAll(userScope, 0o755); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Download: true}
	st := scopedUserStorage(t, userScope, perm, key)
	cache := newMemoryUploadCache()
	t.Cleanup(cache.Close)
	srv := newTusTestServer(t, st, cache)
	token := signToken(t, perm, key)

	create, err := http.NewRequest(http.MethodPost, srv.URL+"/api/tus/new.bin", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	create.Header.Set("X-Auth", token)
	create.Header.Set("Upload-Length", "5")
	res, err := srv.Client().Do(create)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST create = %d; want 201", res.StatusCode)
	}

	patch, err := http.NewRequest(http.MethodPatch, srv.URL+res.Header.Get("Location"), bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatal(err)
	}
	patch.Header.Set("X-Auth", token)
	patch.Header.Set("Content-Type", "application/offset+octet-stream")
	patch.Header.Set("Upload-Offset", "0")
	pres, err := srv.Client().Do(patch)
	if err != nil {
		t.Fatal(err)
	}
	pres.Body.Close()
	if pres.StatusCode != http.StatusNoContent && pres.StatusCode != http.StatusOK && pres.StatusCode != 204 {
		t.Fatalf("PATCH = %d; Create-only users must complete new uploads", pres.StatusCode)
	}
	if got, err := os.ReadFile(filepath.Join(userScope, "new.bin")); err != nil || string(got) != "hello" {
		t.Fatalf("uploaded content = %q, err = %v", got, err)
	}
}

// A negative declared length must be rejected before the target is touched:
// validating after OpenFile(O_TRUNC) would empty an existing file and then
// fail the request.
func TestTUSNegativeLengthDoesNotTruncate(t *testing.T) {
	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	if err := os.MkdirAll(userScope, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userScope, "keep.bin"), []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Modify: true, Download: true}
	st := scopedUserStorage(t, userScope, perm, key)
	cache := newMemoryUploadCache()
	t.Cleanup(cache.Close)

	post := handle(tusPostHandler(cache), "/api/tus", st, &settings.Server{})
	req, _ := http.NewRequest(http.MethodPost, "/keep.bin?override=true", strings.NewReader(""))
	req.Header.Set("X-Auth", signToken(t, perm, key))
	req.Header.Set("Upload-Length", "-1")
	rec := httptest.NewRecorder()
	post.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("negative length returned %d, body=%q; want 400", rec.Code, rec.Body.String())
	}
	if got, err := os.ReadFile(filepath.Join(userScope, "keep.bin")); err != nil || string(got) != "precious" {
		t.Fatalf("VULNERABLE: existing file was truncated before the rejection: %q, %v", got, err)
	}
}

// Aborting a TUS upload must purge shares like a regular delete: a surviving
// link would serve whatever file later appears at that path.
func TestTUSDeletePurgesShares(t *testing.T) {
	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	if err := os.MkdirAll(userScope, 0o755); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Modify: true, Delete: true, Download: true, Share: true}
	st := scopedUserStorage(t, userScope, perm, key)
	cache := newMemoryUploadCache()
	t.Cleanup(cache.Close)
	token := signToken(t, perm, key)

	// Create the upload through the real POST handler.
	post := handle(tusPostHandler(cache), "/api/tus", st, &settings.Server{})
	req, _ := http.NewRequest(http.MethodPost, "/abort.bin", strings.NewReader(""))
	req.Header.Set("X-Auth", token)
	req.Header.Set("Upload-Length", "5")
	rec := httptest.NewRecorder()
	post.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST create = %d, body=%q; want 201", rec.Code, rec.Body.String())
	}

	// Share the (partial) upload path through the real share handler.
	shareReq, _ := http.NewRequest(http.MethodPost, "/abort.bin", strings.NewReader(`{"password":"","expires":"","unit":""}`))
	shareReq.Header.Set("X-Auth", token)
	shareReq.Header.Set("Content-Type", "application/json")
	shareRec := httptest.NewRecorder()
	handle(sharePostHandler, "", st, &settings.Server{}).ServeHTTP(shareRec, shareReq)
	if shareRec.Code != http.StatusOK {
		t.Fatalf("share create = %d, body=%q; want 200", shareRec.Code, shareRec.Body.String())
	}
	var created struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(shareRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// Abort the upload through the real DELETE handler.
	del := handle(tusDeleteHandler(cache), "/api/tus", st, &settings.Server{})
	delReq, _ := http.NewRequest(http.MethodDelete, "/abort.bin", http.NoBody)
	delReq.Header.Set("X-Auth", token)
	delRec := httptest.NewRecorder()
	del.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d, body=%q; want 204", delRec.Code, delRec.Body.String())
	}
	if _, err := st.Share.GetByHash(created.Hash); err == nil {
		t.Fatal("VULNERABLE: share survived the TUS abort and can serve a future file")
	}
}
