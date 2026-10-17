// Package i18n localizes the messages the admin API returns to the WebUI.
//
// Only the admin API is localized. The OpenAI-compatible /v1 API keeps its
// English messages on purpose, because clients, SDKs, and proxies may match on
// their text.
//
// English is the source of truth: messages are written in English in the code
// and the catalog only holds the other languages. A missing entry, an
// unsupported Accept-Language, or a language added later therefore never
// changes the English output.
package i18n

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Lang identifies a supported language. The tags match what the WebUI sends in
// Accept-Language and keeps in localStorage.
type Lang string

const (
	// EN is the default and fallback language. It needs no catalog entries,
	// because every message is already written in English.
	EN Lang = "en"
	// VI is Vietnamese.
	VI Lang = "vi"
)

// translations holds "English source -> localized text" per language. A source
// containing a fmt verb is a template: the lookup happens first and the
// argument is formatted into the localized template, never into the English one.
var translations = map[Lang]map[string]string{
	VI: {
		// adminAuth
		"missing Authorization header":                  "Thiếu header Authorization",
		"invalid admin password":                        "Mật khẩu quản trị không đúng",
		"too many failed attempts, retry in %d seconds": "Quá nhiều lần thất bại, thử lại sau %d giây",

		// request bodies
		"request body is required": "Nội dung yêu cầu không được để trống",

		// accounts
		"account not found":                       "Không tìm thấy tài khoản",
		"api_key is required":                     "api_key là bắt buộc",
		"api_key cannot be empty":                 "api_key không được để trống",
		"an account with this key already exists": "Đã tồn tại tài khoản với key này",

		// client keys
		"key not found":                        "Không tìm thấy khóa",
		"a key with this value already exists": "Đã tồn tại khóa với giá trị này",

		// settings
		"admin_password must be at least 8 characters": "admin_password phải có ít nhất 8 ký tự",
		"base_url cannot be empty":                     "base_url không được để trống",
		"current admin password is incorrect":          "Mật khẩu quản trị hiện tại không đúng",

		// OAuth
		"oauth callback url must use http or https":       "URL callback phải dùng http hoặc https",
		"oauth callback url must include a host":          "URL callback phải bao gồm host",
		"oauth callback url path must end with /callback": "Đường dẫn URL callback phải kết thúc bằng /callback",
		"OAuth flow is no longer pending":                 "Luồng OAuth không còn ở trạng thái chờ",
		"OAuth flow canceled":                             "Luồng OAuth đã bị hủy",
		"state token mismatch (possible tampering)":       "state token không khớp (có thể bị giả mạo)",
		"api_key and state are required":                  "Thiếu api_key hoặc state",
		"no pending OAuth flow":                           "Không có luồng OAuth nào đang chờ",
		"method not allowed":                              "Phương thức không được phép",
		"invalid JSON":                                    "JSON không hợp lệ",
		"callback link is required":                       "Vui lòng dán liên kết callback",
		"that is the authorization link, not the callback link: finish authorizing in the browser, then copy the link from the address bar": "Đó là liên kết ủy quyền, không phải liên kết callback: hãy hoàn tất ủy quyền trong trình duyệt rồi sao chép liên kết từ thanh địa chỉ",
		"the link is missing apiKey / state: copy the full link from the address bar after the redirect":                                    "Liên kết thiếu apiKey / state: hãy sao chép toàn bộ liên kết từ thanh địa chỉ sau khi chuyển hướng",

		// quota
		"quota endpoints returned no data": "Các endpoint hạn mức không trả về dữ liệu",
		"quota service unavailable":        "Dịch vụ hạn mức không khả dụng",
	},
}

// wrapped holds English prefixes whose remainder (a wrapped error, a path, a
// duration) is produced outside this package and stays as it is. The longest
// matching prefix wins, so a specific entry such as "account added but saving
// config failed: " takes precedence over the generic "saving config failed: ".
var wrapped = map[Lang]map[string]string{
	VI: {
		"saving config failed: ":                   "Lưu cấu hình thất bại: ",
		"account added but saving config failed: ": "Đã thêm tài khoản nhưng lưu cấu hình thất bại: ",
		"key created but saving config failed: ":   "Đã tạo khóa nhưng lưu cấu hình thất bại: ",
		"applied but saving config failed: ":       "Đã áp dụng nhưng lưu cấu hình thất bại: ",
		"read request body: ":                      "Đọc nội dung yêu cầu thất bại: ",
		"invalid JSON body: ":                      "Nội dung JSON không hợp lệ: ",
		"parse oauth callback url: ":               "Phân tích URL callback thất bại: ",
		"generate oauth state: ":                   "Tạo OAuth state thất bại: ",
		"start callback server: ":                  "Khởi động máy chủ callback thất bại: ",
		"authorization canceled: ":                 "Ủy quyền bị hủy: ",
		"OAuth timed out after ":                   "OAuth hết thời gian chờ sau ",
		"decode response: ":                        "Giải mã phản hồi thất bại: ",
		"API key rejected: ":                       "API key bị từ chối: ",
		"all quota endpoints failed: ":             "Tất cả endpoint hạn mức đều thất bại: ",
		"parse callback link: ":                    "Phân tích liên kết callback thất bại: ",
		"authorization denied: ":                   "Ủy quyền bị từ chối: ",
	},
}

// Message returns source in lang, formatted with args. Sources that the catalog
// does not know, and every message for EN, are returned unchanged.
func Message(lang Lang, source string, args ...any) string {
	if text, ok := translations[lang][source]; ok {
		if len(args) > 0 {
			return fmt.Sprintf(text, args...)
		}
		return text
	}
	if len(args) > 0 {
		source = fmt.Sprintf(source, args...)
	}
	if text, ok := wrappedMessage(lang, source); ok {
		return text
	}
	return source
}

// wrappedMessage localizes source when it starts with a known wrapper prefix,
// keeping everything after that prefix untouched.
func wrappedMessage(lang Lang, source string) (string, bool) {
	table := wrapped[lang]
	longest := ""
	for prefix := range table {
		if len(prefix) > len(longest) && strings.HasPrefix(source, prefix) {
			longest = prefix
		}
	}
	if longest == "" {
		return "", false
	}
	return table[longest] + source[len(longest):], true
}

// FromRequest negotiates the response language of an admin request.
func FromRequest(r *http.Request) Lang {
	return FromHeader(r.Header.Get("Accept-Language"))
}

// FromHeader picks the best supported language from an Accept-Language header,
// honouring q-values and falling back to EN. Unsupported languages, and entries
// with q=0 (RFC 9110: "not acceptable"), never win.
func FromHeader(header string) Lang {
	best := EN
	bestQ := 0.0
	for _, item := range strings.Split(header, ",") {
		tag, q := parseAcceptLanguageItem(item)
		if q <= 0 {
			continue
		}
		lang, ok := matchLang(tag)
		if !ok {
			continue
		}
		// Strictly greater keeps the earliest entry on ties, and header order
		// is the client's own preference order.
		if q > bestQ {
			best, bestQ = lang, q
		}
	}
	return best
}

// parseAcceptLanguageItem splits one "tag;q=value" entry. A malformed q-value
// makes the entry unusable, which q=0 encodes.
func parseAcceptLanguageItem(item string) (string, float64) {
	parts := strings.Split(item, ";")
	tag := strings.ToLower(strings.TrimSpace(parts[0]))
	q := 1.0
	for _, param := range parts[1:] {
		param = strings.TrimSpace(param)
		if !strings.HasPrefix(param, "q=") && !strings.HasPrefix(param, "Q=") {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(param[2:]), 64)
		if err != nil {
			return tag, 0
		}
		q = value
	}
	return tag, q
}

// matchLang maps a language tag (or "*") to a supported language. Every vi
// variant resolves to Vietnamese, so a regional variant still gets Vietnamese
// rather than English.
func matchLang(tag string) (Lang, bool) {
	tag = strings.ReplaceAll(tag, "_", "-")
	switch {
	case tag == "*" || tag == "en" || strings.HasPrefix(tag, "en-"):
		return EN, true
	case tag == "vi" || strings.HasPrefix(tag, "vi-"):
		return VI, true
	default:
		return EN, false
	}
}
