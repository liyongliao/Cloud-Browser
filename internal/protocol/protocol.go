package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func ValidID(s string) bool { return idPattern.MatchString(s) }
func ValidateURL(s string) error {
	if len(s) > 8192 {
		return errors.New("网址过长")
	}
	u, e := url.Parse(s)
	if e != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("仅支持 HTTP/HTTPS 网址")
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return errors.New("不允许访问本地地址")
	}
	if ip := net.ParseIP(h); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()) {
		return errors.New("不允许访问私网地址")
	}
	return nil
}
func Env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func Error(w http.ResponseWriter, status int, code string) {
	JSON(w, status, map[string]string{"error": code})
}
func Decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		Error(w, 400, "INVALID_REQUEST")
		return false
	}
	return true
}
func UnixTransport(path string) *http.Transport {
	return &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}, ResponseHeaderTimeout: 90 * time.Second}
}
