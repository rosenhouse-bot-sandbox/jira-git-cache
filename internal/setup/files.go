package setup

import (
	"fmt"
	"strings"
)

// Gitignore is .jira-git-cache/.gitignore: pull owns every other path there.
const Gitignore = "# .jira-git-cache/.gitignore: pull writes everything else here\n/*\n!/config.yml\n!/.gitignore\n"

const workflowPath = ".github/workflows/jira-sync.yaml"

const workflowTemplate = `name: jira-sync
on:
  schedule: [{cron: "*/30 * * * *"}]
  workflow_dispatch: {inputs: {full: {type: boolean, default: false}}}
concurrency: {group: jira-sync, cancel-in-progress: false}
permissions: {contents: write}
jobs:
  sync:
    runs-on: [%s]
    environment: jira
    timeout-minutes: 60
    steps:
      - uses: actions/checkout@v4
      - run: go run "$TOOL" sync --full="$FULL"
        env:
          TOOL: github.com/rosenhouse-bot-sandbox/jira-git-cache/cmd/jira-git-cache@%s
          FULL: ${{ inputs.full == true }}
          JIRA_TOKEN: ${{ secrets.JIRA_TOKEN }}
`

// Workflow returns .github/workflows/jira-sync.yaml, running on labels and pinned to toolVersion.
func Workflow(labels []string, toolVersion string) []byte {
	return fmt.Appendf(nil, workflowTemplate, strings.Join(labels, ", "), toolVersion)
}

const nextSteps = `Next steps:
  1. Commit and push main:
       git add .jira-git-cache .github && git commit -m "Configure jira-git-cache" && git push
  2. Create the environment "jira", limit it to the main branch, and store the token there:
       gh secret set JIRA_TOKEN --env jira
  3. Protect main.
  4. Let only GitHub Actions push %s: add a ruleset that blocks updates,
     force pushes, and deletions, with GitHub Actions as the only bypass actor.
  5. Start the first sync:
       gh workflow run jira-sync.yaml
`
