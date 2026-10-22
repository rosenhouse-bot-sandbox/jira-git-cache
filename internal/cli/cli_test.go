package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/cli"
)

func run(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = cli.Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestNoArgsPrintsUsageAndFails(t *testing.T) {
	code, stdout, stderr := run()
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "Usage: jira-git-cache <command>") {
		t.Errorf("stderr = %q, want usage", stderr)
	}
}

func TestUnknownCommandFails(t *testing.T) {
	code, _, stderr := run("frobnicate")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, `unknown command "frobnicate"`) {
		t.Errorf("stderr = %q, want unknown command message", stderr)
	}
}

func TestHelpPrintsUsageAndSucceeds(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		code, stdout, stderr := run(arg)
		if code != 0 {
			t.Errorf("%s: exit code = %d, want 0", arg, code)
		}
		if !strings.Contains(stdout, "Usage: jira-git-cache <command>") {
			t.Errorf("%s: stdout = %q, want usage", arg, stdout)
		}
		if stderr != "" {
			t.Errorf("%s: stderr = %q, want empty", arg, stderr)
		}
	}
}

func TestVersionPrintsVersion(t *testing.T) {
	code, stdout, _ := run("version")
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stdout != cli.Version+"\n" {
		t.Errorf("stdout = %q, want %q", stdout, cli.Version+"\n")
	}
}
