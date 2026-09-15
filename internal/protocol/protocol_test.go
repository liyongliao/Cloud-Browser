package protocol

import "testing"

func TestURLBoundary(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "javascript:alert(1)", "http://localhost", "http://127.0.0.1/", "http://[::1]/", "http://169.254.169.254/", "https://name:pass@example.com", "https://10.0.0.1/", "https://[fc00::1]/"} {
		if ValidateURL(u) == nil {
			t.Errorf("accepted %s", u)
		}
	}
	for _, u := range []string{"https://example.com/path?q=中文#fragment", "http://example.com:8080/"} {
		if e := ValidateURL(u); e != nil {
			t.Error(e)
		}
	}
}
