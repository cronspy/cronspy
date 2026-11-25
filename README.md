# cronspy

The open source companion of [CronSpy](https://cronspy.com), the monitor for cron jobs and
background jobs.

- `cronspy run` (coming soon) wraps a job: it reports start, success or failure, duration,
  metrics and the end of the log, and always exits with the job's own exit code.
- [`redact`](redact) hides secrets in a log before it leaves the machine. The CronSpy server
  runs the same package again on arrival.

## What `redact` hides

Bearer tokens, values of keys named like `password`, `secret`, `token` or `api_key`, the
password in connection strings (`postgres://user:pass@host`), AWS access key ids, JWTs, and
API keys of OpenAI and Anthropic (`sk-…`), Stripe, GitHub, Slack and CronSpy (`cs_…`). Each is
replaced by `[REDACTED]`. The patterns and their tests are in [redact](redact).

## License

MIT
