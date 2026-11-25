// Command cronspy runs a cron job and tells CronSpy how it went: when it started and ended,
// whether it failed, its metrics and the end of its log, with secrets hidden. It always exits
// with the job's own exit code.
package main

import (
	"net/http"
	"os"

	"github.com/cronspy/cronspy/internal/cli"
	"github.com/cronspy/cronspy/internal/spool"
)

// version is set by the release build.
var version = "dev"

func main() {
	os.Exit(cli.Main(os.Args[1:], cli.Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv,
		Spool: spool.Default(), HTTP: http.DefaultClient, Version: version,
	}))
}
