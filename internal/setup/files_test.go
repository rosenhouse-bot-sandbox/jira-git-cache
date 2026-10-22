package setup_test

import (
	"strings"
	"testing"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/setup"
)

const issue2Workflow = `name: jira-sync
on:
  schedule: [{cron: "*/30 * * * *"}]
  workflow_dispatch: {inputs: {full: {type: boolean, default: false}}}
concurrency: {group: jira-sync, cancel-in-progress: false}
permissions: {contents: write}
jobs:
  sync:
    runs-on: [self-hosted, jira]
    environment: jira
    timeout-minutes: 60
    steps:
      - uses: actions/checkout@v4
      - run: go run "$TOOL" sync --full="$FULL"
        env:
          TOOL: github.com/rosenhouse-bot-sandbox/jira-git-cache/cmd/jira-git-cache@v0.1.0
          FULL: ${{ inputs.full == true }}
          JIRA_TOKEN: ${{ secrets.JIRA_TOKEN }}
`

func TestWorkflowMatchesIssue2(t *testing.T) {
	t.Parallel()
	if got := string(setup.Workflow([]string{"self-hosted", "jira"}, "v0.1.0")); got != issue2Workflow {
		t.Errorf("got:\n%s\nwant:\n%s", got, issue2Workflow)
	}
}

func TestWorkflowUsesLabelsAndVersion(t *testing.T) {
	t.Parallel()
	got := string(setup.Workflow([]string{"linux"}, "v1.2.3-0.20261022070000-abcdef123456"))
	for _, want := range []string{
		"    runs-on: [linux]\n",
		"jira-git-cache@v1.2.3-0.20261022070000-abcdef123456\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("workflow lacks %q:\n%s", want, got)
		}
	}
}

func TestGitignoreMatchesIssue2(t *testing.T) {
	t.Parallel()
	const want = "# .jira-git-cache/.gitignore: pull writes everything else here\n/*\n!/config.yml\n!/.gitignore\n"
	if setup.Gitignore != want {
		t.Errorf("got %q", setup.Gitignore)
	}
}
