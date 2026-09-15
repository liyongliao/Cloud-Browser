package auth

import "testing"

func TestPasswords(t *testing.T) {
	a, e := Password("a long correct password")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := Password("a long correct password")
	if a == b {
		t.Fatal("salt reused")
	}
	if !Verify(a, "a long correct password") || Verify(a, "wrong") {
		t.Fatal("password verification incorrect")
	}
	for _, bad := range []string{"", "a2id$invalid$invalid", "a2id$AA$AA"} {
		if Verify(bad, "anything") {
			t.Fatal("accepted invalid hash")
		}
	}
	if _, e = Password("short"); e == nil {
		t.Fatal("short password accepted")
	}
}
