package avatar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hokan/hokan/internal/store"
)

// Lookup finds a user by username. *store.UserStore satisfies this.
type Lookup interface {
	GetByUsername(ctx context.Context, username string) (*store.User, error)
}

const (
	MaxBytes      = 2 << 20
	maxDim        = 2048
	defaultSz     = 128
	Generation    = 2
	DefaultOrigin = "https://blobatar.dev"
)

type Service struct {
	Dir       string
	Users     Lookup
	Client    *http.Client
	Origin    string
	mu        sync.Mutex
	downUntil time.Time
}

func New(dir string, users Lookup) *Service {
	return &Service{
		Dir:    dir,
		Users:  users,
		Client: &http.Client{Timeout: 4 * time.Second},
		Origin: DefaultOrigin,
	}
}

func BlobatarURL(origin, username string, size int) string {
	if origin == "" {
		origin = DefaultOrigin
	}
	if size <= 0 {
		size = defaultSz
	}
	name := strings.ToLower(strings.TrimSpace(username))
	if name == "" {
		name = "_"
	}
	return fmt.Sprintf("%s/avatar/%s?size=%d&background=squircle&gen=%d",
		strings.TrimRight(origin, "/"), url.PathEscape(name), size, Generation)
}

func (s *Service) origin() string {
	if s.Origin != "" {
		return s.Origin
	}
	return DefaultOrigin
}

func (s *Service) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 4 * time.Second}
}

func (s *Service) CustomPath(userID string) string {
	return filepath.Join(s.Dir, userID+".png")
}

func cachePath(dir, username string) string {
	key := strings.ToLower(strings.TrimSpace(username))
	if key == "" {
		key = "_"
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(dir, "cache", hex.EncodeToString(sum[:16])+".svg")
}

func (s *Service) SaveCustom(userID string, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty image")
	}
	if len(data) > MaxBytes {
		return fmt.Errorf("image too large (max %d bytes)", MaxBytes)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("not a supported image (png, jpeg, gif)")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxDim || cfg.Height > maxDim {
		return fmt.Errorf("image dimensions must be between 1 and %d", maxDim)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid image")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	path := s.CustomPath(userID)
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	err = png.Encode(f, img)
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, path)
}

func (s *Service) RemoveCustom(userID string) error {
	err := os.Remove(s.CustomPath(userID))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Service) Serve(w http.ResponseWriter, r *http.Request, username string) {
	username = strings.TrimSpace(username)
	if username == "" {
		s.writeSVG(w, FallbackSVG("_", defaultSz))
		return
	}
	if s.Users != nil {
		if u, err := s.Users.GetByUsername(r.Context(), username); err == nil && u.HasAvatar {
			if s.serveFile(w, r, s.CustomPath(u.ID), "image/png") {
				return
			}
		}
	}
	s.serveDefault(w, r, username)
}

func (s *Service) serveDefault(w http.ResponseWriter, r *http.Request, username string) {
	cpath := cachePath(s.Dir, username)
	if s.serveFile(w, r, cpath, "image/svg+xml") {
		return
	}
	if svg, ok := s.fetchBlobatar(r.Context(), username); ok {
		_ = os.MkdirAll(filepath.Dir(cpath), 0o755)
		_ = os.WriteFile(cpath, svg, 0o644)
		s.writeSVG(w, svg)
		return
	}
	s.writeSVG(w, FallbackSVG(username, defaultSz))
}

func (s *Service) fetchBlobatar(ctx context.Context, username string) ([]byte, bool) {
	s.mu.Lock()
	down := time.Now().Before(s.downUntil)
	s.mu.Unlock()
	if down {
		return nil, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BlobatarURL(s.origin(), username, defaultSz), nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Accept", "image/svg+xml,image/*")
	resp, err := s.client().Do(req)
	if err != nil {
		s.markDown()
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= 500 {
			s.markDown()
		}
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil || len(body) == 0 {
		return nil, false
	}
	ct := resp.Header.Get("Content-Type")
	if !bytes.Contains(body, []byte("<svg")) && !strings.Contains(ct, "svg") {
		return nil, false
	}
	return body, true
}

func (s *Service) markDown() {
	s.mu.Lock()
	s.downUntil = time.Now().Add(30 * time.Second)
	s.mu.Unlock()
}

func (s *Service) serveFile(w http.ResponseWriter, r *http.Request, path, contentType string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return false
	}
	etag := fmt.Sprintf(`"%x-%x"`, st.ModTime().UnixNano(), st.Size())
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
	return true
}

func (s *Service) writeSVG(w http.ResponseWriter, svg []byte) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(svg)
}

func FallbackSVG(seed string, size int) []byte {
	if size < 16 {
		size = defaultSz
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(seed))))
	n := h.Sum32()
	hue := n % 360
	hue2 := (hue + 40) % 360
	cx := 50 + int(n%7) - 3
	cy := 52 + int((n>>8)%7) - 3
	rx := 28 + int((n>>16)%6)
	ry := 24 + int((n>>20)%6)
	eye := 5 + int((n>>24)%3)
	return []byte(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" width="%d" height="%d" role="img" aria-hidden="true">
<rect width="100" height="100" rx="22" fill="hsl(%d,42%%,18%%)"/>
<ellipse cx="%d" cy="%d" rx="%d" ry="%d" fill="hsl(%d,55%%,52%%)"/>
<circle cx="%d" cy="%d" r="%d" fill="#1a1408"/>
<circle cx="%d" cy="%d" r="%d" fill="#1a1408"/>
<circle cx="%d" cy="%d" r="1.6" fill="#f4ead4"/>
<circle cx="%d" cy="%d" r="1.6" fill="#f4ead4"/>
</svg>`,
		size, size,
		hue2,
		cx, cy, rx, ry, hue,
		cx-8, cy-4, eye,
		cx+8, cy-4, eye,
		cx-7, cy-5,
		cx+9, cy-5,
	))
}
