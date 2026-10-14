package config

import (
	"net/url"
	"strings"
)

// RedactURL strips everything after the host from a URL, keeping scheme
// and host only. RPC endpoints commonly carry API keys in query strings
// or paths; logs and error messages must show the redacted form
// (spec §5.4, §12: 日志中不得输出 API Key 等敏感内容).
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// Not parseable: keep only what is certainly a scheme.
		if i := strings.Index(raw, "://"); i > 0 {
			return raw[:i] + "://<redacted>"
		}
		return "<redacted>"
	}
	return u.Scheme + "://" + u.Host
}
