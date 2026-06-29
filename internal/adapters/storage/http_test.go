package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// newHTTPFixture serves a small index + one .gpkg file, optionally behind basic
// auth, and returns an HTTPStorage configured against it.
func newHTTPFixture(t *testing.T, user, pass string) *HTTPStorage {
	t.Helper()
	const indexBody = "# comment line\n\nregions.gpkg\nignore.txt\nNESTED/area.GPKG\n"
	const fileBody = "fake-geopackage-bytes"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user != "" {
			u, p, ok := r.BasicAuth()
			if !ok || u != user || p != pass {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		switch r.URL.Path {
		case "/index.txt":
			_, _ = w.Write([]byte(indexBody))
		case "/regions.gpkg":
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			_, _ = w.Write([]byte(fileBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	return NewHTTPStorage(HTTPConfig{BaseURL: srv.URL, Username: user, Password: pass})
}

func TestHTTPStorageListParsesIndex(t *testing.T) {
	s := newHTTPFixture(t, "", "")
	objs, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Comments, blank lines and non-.gpkg entries are skipped; .GPKG is kept.
	got := make(map[string]bool)
	for _, o := range objs {
		got[o.Key] = true
	}
	if len(objs) != 2 || !got["regions.gpkg"] || !got["NESTED/area.GPKG"] {
		t.Errorf("List = %+v, want [regions.gpkg, NESTED/area.GPKG]", objs)
	}
}

func TestHTTPStorageListBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	s := NewHTTPStorage(HTTPConfig{BaseURL: srv.URL})
	if _, err := s.List(context.Background()); err == nil {
		t.Error("List should error on non-200 index response")
	}
}

func TestHTTPStorageDownload(t *testing.T) {
	s := newHTTPFixture(t, "", "")
	dest := filepath.Join(t.TempDir(), "nested", "regions.gpkg")

	if err := s.Download(context.Background(), "regions.gpkg", dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(body) != "fake-geopackage-bytes" {
		t.Errorf("downloaded body = %q", string(body))
	}
}

func TestHTTPStorageDownloadNotFound(t *testing.T) {
	s := newHTTPFixture(t, "", "")
	err := s.Download(context.Background(), "missing.gpkg", filepath.Join(t.TempDir(), "x.gpkg"))
	if err == nil {
		t.Error("Download should error on 404")
	}
}

func TestHTTPStorageGetReader(t *testing.T) {
	s := newHTTPFixture(t, "", "")
	rc, err := s.GetReader(context.Background(), "regions.gpkg")
	if err != nil {
		t.Fatalf("GetReader: %v", err)
	}
	defer func() { _ = rc.Close() }()
	buf := make([]byte, 64)
	n, _ := rc.Read(buf)
	if string(buf[:n]) != "fake-geopackage-bytes" {
		t.Errorf("reader body = %q", string(buf[:n]))
	}

	if _, err := s.GetReader(context.Background(), "missing.gpkg"); err == nil {
		t.Error("GetReader should error on 404")
	}
}

func TestHTTPStorageExists(t *testing.T) {
	s := newHTTPFixture(t, "", "")
	ok, err := s.Exists(context.Background(), "regions.gpkg")
	if err != nil || !ok {
		t.Errorf("Exists(regions.gpkg) = %v, %v; want true, nil", ok, err)
	}
	ok, err = s.Exists(context.Background(), "missing.gpkg")
	if err != nil || ok {
		t.Errorf("Exists(missing) = %v, %v; want false, nil", ok, err)
	}
}

// A transport error (unreachable host) must surface as an error, not be
// silently reported as "not found" (tech-debt D2).
func TestHTTPStorageExistsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // connections now refused → transport error

	s := NewHTTPStorage(HTTPConfig{BaseURL: url})
	ok, err := s.Exists(context.Background(), "x.gpkg")
	if err == nil || ok {
		t.Errorf("Exists(transport error) = %v, %v; want false, non-nil error", ok, err)
	}
}

func TestHTTPStorageBasicAuth(t *testing.T) {
	s := newHTTPFixture(t, "user", "pass")
	// Correct credentials configured → succeeds.
	if _, err := s.List(context.Background()); err != nil {
		t.Fatalf("List with valid auth: %v", err)
	}

	// Wrong credentials → index fetch returns 401 → error.
	bad := NewHTTPStorage(HTTPConfig{BaseURL: s.baseURL, Username: "user", Password: "wrong"})
	if _, err := bad.List(context.Background()); err == nil {
		t.Error("List with wrong credentials should fail")
	}
}

func TestNewHTTPStorageDefaults(t *testing.T) {
	s := NewHTTPStorage(HTTPConfig{BaseURL: "https://example.com/"})
	if s.indexFile != "index.txt" {
		t.Errorf("default indexFile = %q, want index.txt", s.indexFile)
	}
	if s.baseURL != "https://example.com" {
		t.Errorf("baseURL = %q, want trailing slash trimmed", s.baseURL)
	}
	if s.client.Timeout == 0 {
		t.Error("default timeout should be set")
	}
}
