// Package redact hides secrets in job logs. The cronspy wrapper runs it before a log leaves the
// machine, and the CronSpy server runs it again on arrival, which also covers pings sent with
// curl.
package redact

import (
	"regexp"
	"strings"
)

// Marker replaces every secret.
const Marker = "[REDACTED]"

// Secrets with a recognizable shape, replaced whole.
var tokens = regexp.MustCompile(strings.Join([]string{
	`eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`, // JWT
	`\bsk-[A-Za-z0-9_-]{16,}`,                                    // OpenAI, Anthropic
	`\b[rs]k_(?:live|test)_[A-Za-z0-9]{16,}`,                     // Stripe
	`\bgh[pousr]_[A-Za-z0-9]{36,}`,                               // GitHub
	`\bgithub_pat_[A-Za-z0-9_]{22,}`,                             // GitHub fine-grained
	`\bxox[abprs]-[A-Za-z0-9-]{10,}`,                             // Slack
	`https://hooks\.slack\.com/services/[A-Za-z0-9/]+`,           // Slack webhook
	`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`,                              // AWS access key id
	`\bcs_(?:ping|read|live)_[A-Za-z0-9]{8,}`,                    // CronSpy
}, "|"))

// Authorization: Bearer … and Basic …; the scheme stays.
var authorization = regexp.MustCompile(`(?i)(\b(?:bearer|basic)\s+)[A-Za-z0-9._~+/=-]{8,}`)

// scheme://user:password@host; only the password goes.
var connectionString = regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://[^\s:/@]+:)[^\s@/]+(@)`)

// key=value, key: value and "key": "value" where the key names a secret; the key stays.
var keyValue = regexp.MustCompile(`(?i)((?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)[A-Za-z0-9_-]*["']?\s*[:=]\s*["']?)([^\s"'&,;]+)`)

// String returns s with its secrets replaced by Marker. Running it twice changes nothing.
func String(s string) string {
	s = tokens.ReplaceAllString(s, Marker)
	s = authorization.ReplaceAllString(s, "${1}"+Marker)
	s = connectionString.ReplaceAllString(s, "${1}"+Marker+"${2}")
	return keyValue.ReplaceAllStringFunc(s, func(m string) string {
		parts := keyValue.FindStringSubmatch(m)
		key, value := strings.ToLower(parts[1]), parts[2]
		// A count of tokens (an LLM job's "tokens=1204") is a metric, not a secret.
		if strings.HasPrefix(key, "token") && isDigits(value) {
			return m
		}
		return parts[1] + Marker
	})
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
