package files

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func setup(t *testing.T) (string, http.Handler) {
	t.Helper()
	home := t.TempDir()
	for _, d := range []string{"Uploads", "Downloads"} {
		if e := os.Mkdir(filepath.Join(home, d), 0700); e != nil {
			t.Fatal(e)
		}
	}
	m := http.NewServeMux()
	h := Handler(home)
	m.HandleFunc("GET /files", h)
	m.HandleFunc("POST /files", h)
	m.HandleFunc("GET /files/{file}/download", h)
	m.HandleFunc("DELETE /files/{file}", h)
	return home, m
}
func TestFileIsolation(t *testing.T) {
	home, h := setup(t)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("sensitive"), 0600)
	os.Symlink(outside, filepath.Join(home, "Downloads", "escape"))
	os.WriteFile(filepath.Join(home, "Downloads", "safe"), []byte("hello"), 0600)
	os.Symlink("safe", filepath.Join(home, "Downloads", "alias"))
	os.WriteFile(filepath.Join(home, "Downloads", "partial.crdownload"), []byte("partial"), 0600)
	for _, id := range []string{fileID("Downloads", "escape"), fileID("Downloads", "alias"), fileID("..", "secret"), fileID("Downloads", "../secret"), fileID("Downloads", "partial.crdownload")} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/files/"+id+"/download", nil))
		if w.Code == 200 {
			t.Errorf("unsafe download %s", id)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/files/"+fileID("Downloads", "safe")+"/download", nil))
	if w.Code != 200 || w.Body.String() != "hello" || w.Header().Get("Content-Disposition") == "" {
		t.Fatalf("download: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/files", nil))
	var out struct{ Files []Entry }
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Files) != 1 {
		t.Fatalf("unsafe list: %s", w.Body.String())
	}
}
func TestUploadAndDelete(t *testing.T) {
	_, h := setup(t)
	var b bytes.Buffer
	m := multipart.NewWriter(&b)
	f, _ := m.CreateFormFile("file", "报告.txt")
	io.WriteString(f, "你好")
	m.Close()
	q := httptest.NewRequest("POST", "/files", &b)
	q.Header.Set("Content-Type", m.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, q)
	if w.Code != 201 {
		t.Fatalf("upload %d %s", w.Code, w.Body.String())
	}
	var out struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &out)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/files/"+out.ID+"/download", nil))
	if w.Body.String() != "你好" {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("DELETE", "/files/"+out.ID, nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/files/"+out.ID+"/download", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
