# cronspy

The open source companion of [CronSpy](https://cronspy.com), the monitor for cron jobs and
background jobs. `cronspy run` wraps a job and tells CronSpy when it started, how it ended, how
long it took, what it reported and the end of its log. It always exits with the job's own exit
code, whatever happens to CronSpy.

```sh
export CRONSPY_KEY=cs_ping_…        # a ping key from CronSpy › Integrations › API keys
cronspy run --monitor import-sales -- ./import.sh --full
```

In a crontab:

```
0 3 * * * CRONSPY_KEY=cs_ping_… cronspy run --monitor nightly-backup -- /usr/local/bin/backup.sh
```

## Install

- Homebrew: `brew install cronspy/tap/cronspy`
- Debian and Ubuntu, Fedora and RHEL: the `.deb` and `.rpm` on the [releases](https://github.com/cronspy/cronspy/releases)
- Go: `go install github.com/cronspy/cronspy@latest`
- Or the binary for Linux or macOS (amd64, arm64) from the releases

In a container (a Kubernetes CronJob, a scheduled task), copy it into your job's image from
`ghcr.io/cronspy/cronspy`:

```dockerfile
COPY --from=ghcr.io/cronspy/cronspy:0.1.1 /usr/bin/cronspy /usr/bin/cronspy
ENTRYPOINT ["cronspy", "run", "--monitor", "nightly-backup", "--"]
CMD ["/app/backup.sh"]
```

Every release has a `checksums.txt` signed with [cosign](https://docs.sigstore.dev/), keyless,
by this repository's release workflow. To check a download:

```sh
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/cronspy/cronspy/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt
```

## What it does

1. Sends `start` with a new run id, in the background: the job never waits for it.
2. Runs the command. Its output goes to your terminal or log untouched, and its exit code is
   cronspy's exit code. `SIGINT`, `SIGTERM` and `SIGHUP` are passed on to the job and its
   children.
3. Keeps the last 64 KB of output and reads metrics from it (see below).
4. Sends `success` (exit code 0) or `fail` with the duration, exit code, metrics and the end of
   the log, with secrets hidden.

If CronSpy cannot be reached, the end of the run is retried for up to 2 minutes, with growing
waits, and then saved in `~/.cronspy/spool` to go out with the next run. A wrong key or an
unknown monitor is reported on stderr and not retried. Without `--monitor` or a key, the job
still runs, unmonitored.

## Metrics

Print a line like this from the job, and CronSpy alerts when a job "works" but does nothing
(`rows=0`) or a metric goes missing:

```
CRONSPY rows=1204 files=3
```

Or, from inside the job, `cronspy metric rows=1204`.

## What is sent, and where

Only to the endpoint (`https://ping.cronspy.com` unless you pass `--endpoint`), with your key in
the `Authorization` header:

- the monitor's slug and a random run id;
- when the run started and ended, its duration and exit code;
- the metrics from `CRONSPY` lines and `cronspy metric`;
- the last 64 KB of the job's stdout and stderr, unless `--no-log`. Before it leaves the
  machine, the [`redact`](redact) package replaces secrets with `[REDACTED]`: bearer and basic
  credentials, values of keys named like `password`, `secret`, `token` or `api_key`, passwords in
  connection strings, AWS access key ids, JWTs, and OpenAI, Anthropic, Stripe, GitHub, Slack and
  CronSpy keys. CronSpy redacts the log again on arrival.

Nothing else: no environment variables, no hostnames, no files. The spool holds pending events
and a fingerprint of the key, never the key itself. `--dry-run` runs the job and prints what
would be sent, sending nothing.

## Flags

| Flag | Default | |
| --- | --- | --- |
| `--monitor` | `CRONSPY_MONITOR` | Monitor slug |
| `--key` | `CRONSPY_KEY` | Ping key |
| `--endpoint` | `https://ping.cronspy.com` | Also `CRONSPY_ENDPOINT` |
| `--tail-bytes` | `65536` | Output kept for the log, at most 64 KB |
| `--timeout` | `10s` | Timeout of each request |
| `--retry-max-time` | `2m` | How long to retry the end of a run before saving it |
| `--no-log` | | Send no output |
| `--dry-run` | | Print what would be sent, send nothing |

The exit code is the job's: 128 + n when signal n ended it, 127 when the command was not found,
126 when it could not be run, and 2 when cronspy's own arguments are wrong.

## License

MIT
