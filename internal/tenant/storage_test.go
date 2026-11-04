package tenant

import (
	"strings"
	"testing"
)

func TestSanitizeFileName(t *testing.T) {
	for _, good := range []string{"a.txt", "foto-1.JPG", "doc_v2.pdf", "x"} {
		if _, err := SanitizeFileName(good); err != nil {
			t.Fatalf("%q should be accepted: %v", good, err)
		}
	}
	for _, bad := range []string{"", ".", "..", ".htaccess", "../x", "../../x", "a/b", `a\b`, "a b.txt", "a;rm.txt", strings.Repeat("a", 200)} {
		if _, err := SanitizeFileName(bad); err == nil {
			t.Fatalf("%q should be rejected", bad)
		}
	}
}
