package fbhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/asdine/storm/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"

	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/storage"
	"github.com/filebrowser/filebrowser/v2/storage/bolt"
	"github.com/filebrowser/filebrowser/v2/users"
)

func TestPrintTokenIncludesBookmarks(t *testing.T) {
	key := []byte("test-signing-key")
	bookmarks := []users.Bookmark{
		{Path: "/docs", IsDir: true},
		{Path: "/docs/readme.txt", IsDir: false},
	}
	recorder := httptest.NewRecorder()

	status, err := printToken(recorder, httptest.NewRequest(http.MethodPost, "/api/renew", nil), &data{
		settings: &settings.Settings{Key: key},
	}, &users.User{
		ID:        1,
		Username:  "alice",
		Bookmarks: bookmarks,
	}, DefaultTokenExpirationTime)
	if err != nil {
		t.Fatalf("printToken() error = %v", err)
	}
	if status != 0 {
		t.Fatalf("printToken() status = %d", status)
	}

	var claims authToken
	token, err := jwt.ParseWithClaims(recorder.Body.String(), &claims, func(*jwt.Token) (interface{}, error) {
		return key, nil
	})
	if err != nil {
		t.Fatalf("failed to parse token: %v", err)
	}
	if !token.Valid {
		t.Fatal("expected valid token")
	}

	if len(claims.User.Bookmarks) != len(bookmarks) {
		t.Fatalf("expected %d bookmarks, got %d", len(bookmarks), len(claims.User.Bookmarks))
	}
	for i := range bookmarks {
		if claims.User.Bookmarks[i] != bookmarks[i] {
			t.Fatalf("bookmark %d: expected %#v, got %#v", i, bookmarks[i], claims.User.Bookmarks[i])
		}
	}
}

func TestUserPutHandlerAllowsSelfBookmarkUpdate(t *testing.T) {
	key := []byte("test-signing-key")
	st := newBookmarkTestStorage(t, key)
	signed := signToken(t, users.Permissions{}, key)

	body := map[string]any{
		"what":  "user",
		"which": []string{"bookmarks"},
		"data": map[string]any{
			"id": 1,
			"bookmarks": []map[string]any{
				{"path": "/docs/", "isDir": true},
				{"path": "/docs/readme.txt", "isDir": false},
			},
		},
	}

	req := bookmarkUpdateRequest(t, signed, body)
	recorder := httptest.NewRecorder()
	handle(userPutHandler, "", st, &settings.Server{}).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, recorder.Code, recorder.Body.String())
	}

	user, err := st.Users.Get("", uint(1))
	if err != nil {
		t.Fatalf("failed to fetch user: %v", err)
	}
	want := []users.Bookmark{
		{Path: "/docs", IsDir: true},
		{Path: "/docs/readme.txt", IsDir: false},
	}
	if len(user.Bookmarks) != len(want) {
		t.Fatalf("expected %d bookmarks, got %d: %#v", len(want), len(user.Bookmarks), user.Bookmarks)
	}
	for i := range want {
		if user.Bookmarks[i] != want[i] {
			t.Fatalf("bookmark %d: expected %#v, got %#v", i, want[i], user.Bookmarks[i])
		}
	}
}

func TestUserPutHandlerRejectsInvalidBookmarks(t *testing.T) {
	key := []byte("test-signing-key")
	st := newBookmarkTestStorage(t, key)
	signed := signToken(t, users.Permissions{}, key)

	body := map[string]any{
		"what":  "user",
		"which": []string{"bookmarks"},
		"data": map[string]any{
			"id": 1,
			"bookmarks": []map[string]any{
				{"path": "relative", "isDir": true},
			},
		},
	}

	req := bookmarkUpdateRequest(t, signed, body)
	recorder := httptest.NewRecorder()
	handle(userPutHandler, "", st, &settings.Server{}).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, recorder.Code, recorder.Body.String())
	}
}

func newBookmarkTestStorage(t *testing.T, key []byte) *storage.Storage {
	t.Helper()

	db, err := storm.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	st, err := bolt.NewStorage(db)
	if err != nil {
		t.Fatalf("failed to get storage: %v", err)
	}
	if err := st.Users.Save(&users.User{Username: "alice", Password: "pw"}); err != nil {
		t.Fatalf("failed to save user: %v", err)
	}
	if err := st.Settings.Save(&settings.Settings{Key: key}); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	return st
}

func bookmarkUpdateRequest(t *testing.T, signed string, body map[string]any) *http.Request {
	t.Helper()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/1", bytes.NewReader(payload))
	req.Header.Set("X-Auth", signed)
	req = mux.SetURLVars(req, map[string]string{"id": "1"})
	return req
}
