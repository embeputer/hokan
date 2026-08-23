package avatar

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hokan/hokan/internal/store"
)

type memUsers struct {
	byName map[string]*store.User
}

func (m *memUsers) GetByUsername(_ context.Context, name string) (*store.User, error) {
	if m == nil {
		return nil, store.ErrNotFound
	}
	if u, ok := m.byName[strings.ToLower(name)]; ok {
		return u, nil
	}
	return nil, store.ErrNotFound
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBlobatarURL(t *testing.T) {
	u := BlobatarURL("", "Alice", 64)
	if !strings.Contains(u, "https://blobatar.dev/avatar/alice") {
		t.Fatalf("url = %s", u)
	}
	if !strings.Contains(u, "size=64") || !strings.Contains(u, "background=squircle") || !strings.Contains(u, "gen=2") {
		t.Fatalf("missing params: %s", u)
	}
	u = BlobatarURL("https://blobatar.dev", "user/name", 0)
	if !strings.Contains(u, "user%2Fname") {
		t.Fatalf("expected escaped name: %s", u)
	}
}

func TestFallbackSVGStable(t *testing.T) {
	a := FallbackSVG("alice", 64)
	b := FallbackSVG("alice", 64)
	c := FallbackSVG("bob", 64)
	if !bytes.Equal(a, b) {
		t.Fatal("same seed should be identical")
	}
	if bytes.Equal(a, c) {
		t.Fatal("different seeds should differ")
	}
	if !bytes.Contains(a, []byte("<svg")) {
		t.Fatal("expected svg")
	}
}

func TestSaveAndServeCustom(t *testing.T) {
	dir := t.TempDir()
	u := &store.User{ID: "u1", Username: "alice", HasAvatar: true}
	svc := New(dir, &memUsers{byName: map[string]*store.User{"alice": u}})
	if err := svc.SaveCustom("u1", testPNG(t)); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/avatars/alice", nil)
	svc.Serve(rr, req, "alice")
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "png") {
		t.Fatalf("content-type %s", ct)
	}
	if rr.Body.Len() < 20 {
		t.Fatal("empty body")
	}
}

func TestServeBlobatarThenFallback(t *testing.T) {
	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/avatar/carol" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = io.WriteString(w, `<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	}))
	t.Cleanup(blob.Close)

	dir := t.TempDir()
	svc := New(dir, &memUsers{byName: map[string]*store.User{
		"carol": {ID: "c1", Username: "carol"},
	}})
	svc.Origin = blob.URL

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/avatars/carol", nil)
	svc.Serve(rr, req, "carol")
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("<svg")) {
		t.Fatalf("body %s", rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "cache")); err != nil {
		t.Fatal("expected cache dir")
	}

	down := New(t.TempDir(), &memUsers{byName: map[string]*store.User{
		"dave": {ID: "d1", Username: "dave"},
	}})
	down.Origin = "http://127.0.0.1:1"
	down.Client = &http.Client{Timeout: 50 * time.Millisecond}
	rr2 := httptest.NewRecorder()
	down.Serve(rr2, httptest.NewRequest(http.MethodGet, "/avatars/dave", nil), "dave")
	if rr2.Code != 200 {
		t.Fatalf("fallback status %d", rr2.Code)
	}
	if !bytes.Contains(rr2.Body.Bytes(), []byte("<svg")) {
		t.Fatal("expected fallback svg")
	}
}

func TestServeUnknownSkipsBlobatar(t *testing.T) {
	hits := 0
	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.NotFound(w, r)
	}))
	t.Cleanup(blob.Close)

	dir := t.TempDir()
	svc := New(dir, &memUsers{byName: map[string]*store.User{}})
	svc.Origin = blob.URL

	rr := httptest.NewRecorder()
	svc.Serve(rr, httptest.NewRequest(http.MethodGet, "/avatars/ghost", nil), "ghost")
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	if hits != 0 {
		t.Fatalf("blobatar hits %d, want 0", hits)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("<svg")) {
		t.Fatal("expected fallback svg")
	}
	if rr.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
		t.Fatalf("csp %q", rr.Header().Get("Content-Security-Policy"))
	}
	if _, err := os.Stat(filepath.Join(dir, "cache")); !os.IsNotExist(err) {
		t.Fatalf("unknown users should not write cache: %v", err)
	}
}

func TestServeNilUsersSkipsBlobatar(t *testing.T) {
	hits := 0
	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.NotFound(w, r)
	}))
	t.Cleanup(blob.Close)
	svc := New(t.TempDir(), nil)
	svc.Origin = blob.URL
	rr := httptest.NewRecorder()
	svc.Serve(rr, httptest.NewRequest(http.MethodGet, "/avatars/anyone", nil), "anyone")
	if hits != 0 {
		t.Fatalf("blobatar hits %d, want 0", hits)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("<svg")) {
		t.Fatal("expected fallback svg")
	}
}

func TestSaveRejectsNonImage(t *testing.T) {
	svc := New(t.TempDir(), nil)
	if err := svc.SaveCustom("u", []byte("not-an-image")); err == nil {
		t.Fatal("expected error")
	}
}

type errFlags struct{ err error }

func (f errFlags) SetHasAvatar(context.Context, string, bool) error { return f.err }

func TestAttachCustomRestoresPrevious(t *testing.T) {
	dir := t.TempDir()
	svc := New(dir, nil)
	first := testPNG(t)
	if err := svc.SaveCustom("u1", first); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(svc.CustomPath("u1"))
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.White)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := svc.AttachCustom(context.Background(), errFlags{err: io.ErrUnexpectedEOF}, "u1", buf.Bytes()); err == nil {
		t.Fatal("expected flag error")
	}
	after, err := os.ReadFile(svc.CustomPath("u1"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("previous custom avatar should be restored")
	}
}
