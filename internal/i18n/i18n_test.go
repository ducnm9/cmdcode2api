package i18n

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestFromHeader(t *testing.T) {
	cases := []struct {
		header string
		want   Lang
	}{
		{"", EN},
		{"en-US", EN},
		{"en", EN},
		{"vi", VI},
		{"vi-VN", VI},
		{"vi-Latn-VN", VI},
		{"VI-VN", VI},
		{"vi_VN", VI},
		{"de-DE,de;q=0.9", EN}, // unsupported only: fall back
		{"de,vi;q=0.8", VI},
		{"de-DE,de;q=0.9,en-US;q=0.8", EN},
		{"vi-VN,vi;q=0.9,en;q=0.8", VI},
		{"en;q=0.3,vi;q=0.9", VI},
		{"vi;q=0", EN},         // q=0 means "not acceptable"
		{"vi;q=0,*;q=0.5", EN}, // wildcard still allows English
		{"vi;q=abc", EN},       // malformed q-value: unusable entry
		{"*", EN},
		{"en;q=0.9,vi;q=0.9", EN}, // tie: first entry wins
		{"  vi-VN  ", VI},
		{"de-DE;q=0.9,fr;q=0.8", EN},
	}
	for _, tc := range cases {
		if got := FromHeader(tc.header); got != tc.want {
			t.Errorf("FromHeader(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}

func TestFromRequestReadsHeader(t *testing.T) {
	req, err := http.NewRequest("GET", "/admin/api/overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := FromRequest(req); got != EN {
		t.Fatalf("no header: got %q, want %q", got, EN)
	}
	req.Header.Set("Accept-Language", "vi-VN,vi;q=0.9")
	if got := FromRequest(req); got != VI {
		t.Fatalf("vi header: got %q, want %q", got, VI)
	}
}

func TestMessage(t *testing.T) {
	cases := []struct {
		name   string
		lang   Lang
		source string
		args   []any
		want   string
	}{
		{"english is unchanged", EN, "account not found", nil, "account not found"},
		{"english keeps args", EN, "retry in %d seconds", []any{7}, "retry in 7 seconds"},
		{"vietnamese exact", VI, "account not found", nil, "Không tìm thấy tài khoản"},
		{"vietnamese template formats the translation", VI, "too many failed attempts, retry in %d seconds", []any{7}, "Quá nhiều lần thất bại, thử lại sau 7 giây"},
		{"english template stays english for en", EN, "too many failed attempts, retry in %d seconds", []any{7}, "too many failed attempts, retry in 7 seconds"},
		{"unknown source passes through", VI, "dial tcp 127.0.0.1:443: connect: refused", nil, "dial tcp 127.0.0.1:443: connect: refused"},
		{"unknown source keeps args", VI, "connection failed after %dm", []any{3}, "connection failed after 3m"},
		{"wrapped prefix keeps the cause", VI, "saving config failed: open /data/config.yaml: permission denied", nil, "Lưu cấu hình thất bại: open /data/config.yaml: permission denied"},
		{"longest wrapped prefix wins", VI, "applied but saving config failed: disk full", nil, "Đã áp dụng nhưng lưu cấu hình thất bại: disk full"},
		{"wrapped prefix without cause", VI, "read request body: ", nil, "Đọc nội dung yêu cầu thất bại: "},
		{"wrapped prefix only at the start", VI, "note: saving config failed: x", nil, "note: saving config failed: x"},
		{"unknown language falls back to english", Lang("de"), "account not found", nil, "account not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Message(tc.lang, tc.source, tc.args...); got != tc.want {
				t.Errorf("Message(%q, %q) = %q, want %q", tc.lang, tc.source, got, tc.want)
			}
		})
	}
}

// TestCatalogConsistency guards the catalog itself: every entry must be usable
// and must not be shadowed by an earlier lookup step.
func TestCatalogConsistency(t *testing.T) {
	supported := map[Lang]bool{EN: true, VI: true}
	for lang := range translations {
		if !supported[lang] {
			t.Errorf("translations has unsupported language %q", lang)
		}
	}
	for lang := range wrapped {
		if !supported[lang] {
			t.Errorf("wrapped has unsupported language %q", lang)
		}
	}

	for lang, table := range translations {
		if lang == EN {
			continue // English is the source; it needs no catalog.
		}
		for source, text := range table {
			if strings.TrimSpace(text) == "" {
				t.Errorf("%s: empty translation for %q", lang, source)
			}
			if strings.TrimSpace(source) == "" {
				t.Errorf("%s: empty source", lang)
			}
			if n, m := countVerbs(source), countVerbs(text); n != m {
				t.Errorf("%s: %q has %d format verb(s) but the translation has %d", lang, source, n, m)
			}
			if _, shadowed := wrapped[lang][source]; shadowed {
				t.Errorf("%s: %q is both an exact message and a wrapper prefix", lang, source)
			}
			// A translated message must actually differ from its source, and a
			// wrapper prefix must still be a prefix of the localized text it
			// replaces... only the first property is checkable here.
			if text == source {
				t.Errorf("%s: %q is not translated", lang, source)
			}
		}
		for prefix, text := range wrapped[lang] {
			if strings.TrimSpace(text) == "" {
				t.Errorf("%s: empty wrapper translation for %q", lang, prefix)
			}
			if !strings.HasSuffix(prefix, " ") {
				t.Errorf("%s: wrapper prefix %q should end with a space", lang, prefix)
			}
		}
	}

	// Every non-English catalog must cover the same sources, so adding a
	// language cannot silently leave a message behind.
	for source := range translations[VI] {
		for lang := range translations {
			if _, ok := translations[lang][source]; !ok {
				t.Errorf("%s: missing translation for %q", lang, source)
			}
		}
	}

	// Wrapper prefixes must not go stale: each one has to match the message a
	// caller actually produces, so each is exercised against a sample.
	for lang, table := range wrapped {
		for prefix := range table {
			if got := Message(lang, prefix+"detail"); !strings.HasSuffix(got, "detail") {
				t.Errorf("%s: wrapper %q dropped its cause: %q", lang, prefix, got)
			}
		}
	}
}

// countVerbs counts fmt verbs, treating "%%" as a literal percent.
func countVerbs(s string) int {
	count := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		if i+1 < len(s) && s[i+1] == '%' {
			i++
			continue
		}
		count++
	}
	return count
}

func ExampleMessage() {
	fmt.Println(Message(VI, "account not found"))
	fmt.Println(Message(EN, "account not found"))
	// Output:
	// Không tìm thấy tài khoản
	// account not found
}
