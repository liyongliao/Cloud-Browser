package files

import (
	"cloudbrowser/internal/auth"
	"cloudbrowser/internal/protocol"
	"encoding/base64"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const MaxUpload int64 = 200 << 20
const SoftLimit int64 = 10 << 30

type Entry struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Directory string    `json:"directory"`
	Size      int64     `json:"size"`
	Modified  time.Time `json:"modified"`
}

var uploadMu sync.Mutex

func Usage(root string) (int64, error) {
	var n int64
	e := filepath.WalkDir(root, func(_ string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type().IsRegular() {
			i, e := d.Info()
			if e != nil {
				return e
			}
			n += i.Size()
		}
		return nil
	})
	return n, e
}
func DiskFree(root string) (float64, error) {
	var s syscall.Statfs_t
	if e := syscall.Statfs(root, &s); e != nil {
		return 0, e
	}
	if s.Blocks == 0 {
		return 0, errors.New("empty filesystem")
	}
	return float64(s.Bavail) / float64(s.Blocks), nil
}
func validName(s string) bool {
	return s != "" && s != "." && s != ".." && len(s) <= 240 && !strings.ContainsAny(s, "/\\\x00\r\n") && !strings.HasPrefix(s, ".") && !strings.HasSuffix(s, ".crdownload") && !strings.HasSuffix(s, ".part")
}
func fileID(dir, name string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(dir + "/" + name))
}
func parseID(s string) (string, string, error) {
	b, e := base64.RawURLEncoding.DecodeString(s)
	parts := strings.Split(string(b), "/")
	if e != nil || len(parts) != 2 || (parts[0] != "Uploads" && parts[0] != "Downloads") || !validName(parts[1]) {
		return "", "", errors.New("invalid file id")
	}
	return parts[0], parts[1], nil
}
func openDir(home *os.Root, dir string) (*os.Root, error) {
	s, e := home.Lstat(dir)
	if e != nil || s == nil || !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("invalid file directory")
	}
	return home.OpenRoot(dir)
}
func Handler(homePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		home, e := os.OpenRoot(homePath)
		if e != nil {
			protocol.Error(w, 404, "PROFILE_NOT_INITIALIZED")
			return
		}
		defer home.Close()
		if r.Method == "POST" {
			upload(w, r, home, homePath)
			return
		}
		if r.PathValue("file") == "" {
			entries := []Entry{}
			for _, dir := range []string{"Uploads", "Downloads"} {
				root, e := openDir(home, dir)
				if e != nil {
					continue
				}
				f, e := root.Open(".")
				if e != nil {
					root.Close()
					continue
				}
				items, e := f.ReadDir(-1)
				f.Close()
				if e != nil {
					root.Close()
					continue
				}
				for _, d := range items {
					if !d.Type().IsRegular() || !validName(d.Name()) {
						continue
					}
					i, e := d.Info()
					if e == nil {
						entries = append(entries, Entry{fileID(dir, d.Name()), d.Name(), dir, i.Size(), i.ModTime()})
					}
				}
				root.Close()
			}
			used, e := Usage(homePath)
			if e != nil {
				protocol.Error(w, 500, "STORAGE_UNAVAILABLE")
				return
			}
			protocol.JSON(w, 200, map[string]any{"files": entries, "usedBytes": used, "softLimitBytes": SoftLimit})
			return
		}
		dir, name, e := parseID(r.PathValue("file"))
		if e != nil {
			protocol.Error(w, 400, "INVALID_FILE_ID")
			return
		}
		root, e := openDir(home, dir)
		if e != nil {
			protocol.Error(w, 404, "FILE_NOT_FOUND")
			return
		}
		defer root.Close()
		f, e := openNoFollow(root, name)
		if e != nil {
			protocol.Error(w, 404, "FILE_NOT_FOUND")
			return
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() {
			protocol.Error(w, 400, "NOT_REGULAR_FILE")
			return
		}
		if r.Method == "DELETE" {
			if e = root.Remove(name); e != nil {
				protocol.Error(w, 500, "DELETE_FAILED")
				return
			}
			protocol.JSON(w, 200, map[string]bool{"ok": true})
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		w.Header().Set("Content-Security-Policy", "sandbox")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, name, info.ModTime(), f)
	}
}
func upload(w http.ResponseWriter, r *http.Request, home *os.Root, homePath string) {
	uploadMu.Lock()
	defer uploadMu.Unlock()
	free, e := DiskFree(homePath)
	if e != nil || free < 0.15 {
		protocol.Error(w, 507, "DISK_LOW")
		return
	}
	used, e := Usage(homePath)
	if e != nil || used >= SoftLimit {
		protocol.Error(w, 507, "QUOTA_EXCEEDED")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxUpload+(1<<20))
	reader, e := r.MultipartReader()
	if e != nil {
		protocol.Error(w, 400, "INVALID_MULTIPART")
		return
	}
	part, e := reader.NextPart()
	if e != nil || part.FormName() != "file" || !validName(part.FileName()) {
		protocol.Error(w, 400, "INVALID_FILENAME")
		return
	}
	defer part.Close()
	root, e := openDir(home, "Uploads")
	if e != nil {
		protocol.Error(w, 500, "STORAGE_UNAVAILABLE")
		return
	}
	defer root.Close()
	name := auth.ID()[:8] + "-" + part.FileName()
	tmp := ".upload-" + auth.ID()
	f, e := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		protocol.Error(w, 500, "UPLOAD_FAILED")
		return
	}
	defer root.Remove(tmp)
	limit := min(MaxUpload, SoftLimit-used)
	n, e := io.Copy(f, io.LimitReader(part, limit+1))
	if e == nil {
		e = f.Sync()
	}
	_ = f.Close()
	if e != nil || n > limit {
		protocol.Error(w, 413, "UPLOAD_TOO_LARGE")
		return
	}
	if _, e = reader.NextPart(); e != io.EOF {
		protocol.Error(w, 400, "ONE_FILE_PER_REQUEST")
		return
	}
	homeInfo, statErr := home.Stat(".")
	if statErr != nil {
		protocol.Error(w, 500, "STORAGE_UNAVAILABLE")
		return
	}
	owner := homeInfo.Sys().(*syscall.Stat_t)
	if e = root.Chown(tmp, int(owner.Uid), int(owner.Gid)); e != nil {
		protocol.Error(w, 500, "UPLOAD_OWNERSHIP_FAILED")
		return
	}
	if e = root.Rename(tmp, name); e != nil {
		protocol.Error(w, 500, "UPLOAD_FAILED")
		return
	}
	protocol.JSON(w, 201, Entry{ID: fileID("Uploads", name), Name: name, Directory: "Uploads", Size: n, Modified: time.Now().UTC()})
}

// os.Root resolves symlinks internally, even when OpenFile is given O_NOFOLLOW.
// Open the single basename relative to a held directory descriptor instead.
func openNoFollow(root *os.Root, name string) (*os.File, error) {
	dir, e := root.Open(".")
	if e != nil {
		return nil, e
	}
	defer dir.Close()
	fd, e := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	return os.NewFile(uintptr(fd), name), nil
}
