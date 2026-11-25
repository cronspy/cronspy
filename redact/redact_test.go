package redact

import "testing"

func TestString(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"bearer", "curl -H 'Authorization: Bearer abc123def456ghi'", "curl -H 'Authorization: Bearer [REDACTED]'"},
		{"basic", "Authorization: Basic dXNlcjpwYXNzd29yZA==", "Authorization: Basic [REDACTED]"},
		{"password flag", "mysql --password=hunter22 -u root", "mysql --password=[REDACTED] -u root"},
		{"env var", "DB_PASSWORD=s3cr3t ./run.sh", "DB_PASSWORD=[REDACTED] ./run.sh"},
		{"yaml", "api_key: abcd1234", "api_key: [REDACTED]"},
		{"json", `{"user":"ana","password":"hunter2"}`, `{"user":"ana","password":"[REDACTED]"}`},
		{"aws secret", "aws_secret_access_key = wJalrXUtnFEMI/K7MDENG", "aws_secret_access_key = [REDACTED]"},
		{"query string", "GET /cb?token=abc.def&page=2", "GET /cb?token=[REDACTED]&page=2"},
		{"connection string", "dial postgres://app:pa55@db:5432/prod failed", "dial postgres://app:[REDACTED]@db:5432/prod failed"},
		{"aws key id", "using AKIAIOSFODNN7EXAMPLE", "using [REDACTED]"},
		{"jwt", "jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJl ok", "jwt [REDACTED] ok"},
		{"openai", "key sk-proj-abcdefghijklmnop1234", "key [REDACTED]"},
		{"anthropic", "sk-ant-api03-abcdefghijklmnopqrst", "[REDACTED]"},
		{"stripe", "sk_live_51Habcdefghijklmnop", "[REDACTED]"},
		{"github", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", "[REDACTED]"},
		{"slack token", "xoxb-1234567890-abcdef", "[REDACTED]"},
		{"slack webhook", "post https://hooks.slack.com/services/T000/B000/XXXX", "post [REDACTED]"},
		{"cronspy", "CRONSPY_KEY cs_ping_ABCDEFGHIJKLMNOPQRSTUVWXYZ", "CRONSPY_KEY [REDACTED]"},
		{"token count", "CRONSPY rows=1204 tokens=5300", "CRONSPY rows=1204 tokens=5300"},
		{"plain log", "Imported 1204 rows in 51s from https://example.com/a:b", "Imported 1204 rows in 51s from https://example.com/a:b"},
		{"author", "author: Ana", "author: Ana"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := String(c.in)
			if got != c.want {
				t.Errorf("String(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
			if again := String(got); again != got {
				t.Errorf("not idempotent: %q became %q", got, again)
			}
		})
	}
}
