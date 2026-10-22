# jira-git-cache

A scheduled job copies issues from Jira Data Center into a git branch. Read Jira with `grep`. Nothing writes back to Jira.

Anyone who can read the repo can read every synced issue. Anyone who can push to the repo can change what readers see.

## Install

```sh
go install github.com/rosenhouse-bot-sandbox/jira-git-cache/cmd/jira-git-cache@latest
```

## Set up a cache repo

**You need:**
- A GitHub Enterprise repo whose default branch is `main`, cloned locally, with admin access. Create it with a README so `main` exists.
- A read-only Jira service account and its personal access token. The account must see only issues that every repo reader may see.
- A self-hosted Actions runner, available to this repo, that can reach Jira. It needs `git`, Go, and access to the Go module proxy.
- Trust for Jira's TLS certificate on your laptop and on the runner.
- `gh`, logged in to your GHE host.

**Run:**

```console
$ cd platform-jira
$ jira-git-cache setup
Jira URL: https://jira.example.com
Jira token (hidden; or set JIRA_TOKEN):
✓ Authenticated as svc-jira-cache on Jira Data Center 10.3.4

Projects (comma-separated keys): PLAT, INFRA
Keep every issue updated within days [30]:
Also keep unresolved issues updated within days [180]:
✓ PLAT   1,204 issues in window
✓ INFRA    387 issues in window

Runner labels [self-hosted]: self-hosted, jira
Cache branch [dev/jira-cache]:

Custom fields set on 100 recent issues:
     id                 Jira name       set on
  1  customfield_10002  Story Points       91  *
  2  customfield_10100  Sprint             88  *
  3  customfield_10007  Epic Link          70  *
  4  customfield_10500  Team               52
  5  customfield_10811  Legacy Ticket#      3
Include which fields? [1,2,3]: 1-4

Wrote .jira-git-cache/config.yml
Wrote .jira-git-cache/.gitignore
Wrote .github/workflows/jira-sync.yaml

Create dev/jira-cache on origin with an empty snapshot? [Y/n]
✓ Pushed dev/jira-cache (never synced)

Next steps:
  1. Commit and push main:
       git add .jira-git-cache .github && git commit -m "Configure jira-git-cache" && git push
  2. Create the environment "jira", limit it to the main branch, and store the token there:
       gh secret set JIRA_TOKEN --env jira
  3. Protect main.
  4. Let only GitHub Actions push dev/jira-cache: add a ruleset that blocks updates,
     force pushes, and deletions, with GitHub Actions as the only bypass actor.
  5. Start the first sync:
       gh workflow run jira-sync.yaml
```

**What happens:**
- `setup` checks that `origin`'s default branch is `main`, then checks the token and counts each project's window so typos show up now.
- It samples recent issues to list the custom fields in use. Jira Software's Story Points, Sprint, and Epic Link are preselected.
- It writes these files and does not commit them.

  `.jira-git-cache/config.yml`:

  ```yaml
  jira_url: https://jira.example.com
  cache_branch: dev/jira-cache
  projects: [PLAT, INFRA]
  window:
    days: 30
    unresolved_days: 180
  full_refresh_every: 24h
  concurrency: 8
  fields:
    customfield_10002: story_points
    customfield_10100: sprint
    customfield_10007: epic_link
    customfield_10500: team
  ```

  `.jira-git-cache/.gitignore`:

  ```gitignore
  # .jira-git-cache/.gitignore: pull writes everything else here
  /*
  !/config.yml
  !/.gitignore
  ```

  `.github/workflows/jira-sync.yaml`:

  ```yaml
  name: jira-sync
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
  ```

  The workflow pins the `jira-git-cache` version that ran `setup`.
- The new branch holds only `_meta/snapshot.json` with `"fetched_at": null`. Freshness checks fail until the first sync lands.
- If the config, the workflow, or the branch already exist, `setup` leaves them alone and says so.
- `jira-git-cache help` lists a flag for each question. With `--non-interactive` and `JIRA_TOKEN` set, `setup` asks nothing.

**Then:** finish the printed steps.

## Preview a sync

Check config changes without pushing.

**You need:** a checkout of `main` and network access to Jira.

**Run:**

```console
$ jira-git-cache preview /tmp/jira-preview
Jira token (hidden; or set JIRA_TOKEN):
full refresh: no previous full refresh
PLAT    1,204 in window: 1,204 new, 0 changed, 0 reused, 0 removed
INFRA     387 in window:   387 new, 0 changed, 0 reused, 0 removed
fetched 1,591 issues in 2m41s
wrote /tmp/jira-preview (not pushed)
```

**What happens:** `preview` does what `sync` does, then renders the result as `pull` would instead of pushing. It fetches the tip of `dev/jira-cache` first, so the counts approximate the next real run.
`DIR` must be missing, empty, or an earlier preview.

**Then:** browse `/tmp/jira-preview` as you would `.jira-git-cache/` below.

## The scheduled sync

**What happens on each run:**
1. Fetch the tip of `dev/jira-cache` as the previous snapshot.
2. List every issue in the window, per project, asking only for key and `updated`.
3. Fetch issues that are new or whose `updated` changed. Reuse the rest. Drop issues that left the window.
4. Write the snapshot and force-push it as a single commit with no parent. The push fails if the branch changed during the run.

The branch holds data only:

```
_meta/snapshot.json    # fetched_at, last_full_at, jira_url, per-project JQL and count, schema_version, tool_version, fields_hash
_meta/fields.json      # every custom field's id and name, from Jira
PLAT/PLAT-123.json     # one issue, comments included
```

Unchanged Jira data yields byte-identical files, so git stores and sends each unchanged issue only once.

A full refresh refetches every issue. It runs when the last one is older than `full_refresh_every`, when started with `-f full=true`, and when `schema_version`, the `fields` map, or `jira_url` changes. It catches edits that don't bump `updated`, such as a renamed sprint.

If any fetch fails, the run fails red and pushes nothing. The cache stays complete but stale.
An issue deleted during the run is skipped with a warning.

**Force a full refresh now:**

```sh
gh workflow run jira-sync.yaml -f full=true
```

## Read the cache

**You need:** read access to the repo and `jira-git-cache`. `jq` is optional.

**Run:**

```console
$ git clone https://ghe.example.com/platform/platform-jira.git
$ cd platform-jira
$ jira-git-cache pull
✓ .jira-git-cache/ now holds the snapshot fetched 2026-10-22T05:00:41Z (12m ago)
  PLAT 1,204 issues, INFRA 387 issues
```

Run `pull` again to refresh. Agents should pass `--max-age`:

```console
$ jira-git-cache pull --max-age 2h
✗ .jira-git-cache/ holds the snapshot fetched 2026-10-22T02:30:12Z (3h11m ago), older than 2h
$ echo $?
1
```

**What happens:**
- `pull` reads `cache_branch` from `.jira-git-cache/config.yml` and fetches that branch from `origin`. It keeps the previous snapshot as a ref, so git usually sends only files that changed. It needs no Jira access.
- It renders `issue.md`, `_index.md`, and `issue.json` from the branch's JSON. `issue.json` uses your version's schema. Rendering ships with `jira-git-cache`, so a new release can change the view without a Jira refetch.
- It reads every older `schema_version`. It refuses a newer one and tells you to upgrade.
- It warns when the snapshot's `tool_version` is newer than yours, because your view may differ from other readers'.
- It works from any directory in the clone.
- It refuses to run unless `.jira-git-cache/.gitignore` ignores everything it writes.
- It renders into a temporary directory, then moves each project directory and `_meta/` into place. Readers never see a half-written project. Concurrent pulls wait for each other.
- Local edits outside `config.yml` and `.gitignore` are lost on the next `pull`.
- With `--max-age`, it exits 1 if the snapshot is older than the limit or no sync has run.
- If the fetch fails, it leaves `.jira-git-cache/` alone and exits 2.

**What you get:**

```
.jira-git-cache/config.yml                # tracked on main
.jira-git-cache/_meta/snapshot.json        # copied from the branch
.jira-git-cache/_meta/fields.json          # every custom field's id and name
.jira-git-cache/PLAT/_index.md             # one line per issue: key | status | assignee | type | summary
.jira-git-cache/PLAT/PLAT-123/issue.md     # the issue, comments included
.jira-git-cache/PLAT/PLAT-123/issue.json   # the issue's data, normalized
```

`fetched_at` is UTC, to the second, ending in `Z`. In `_index.md`, a `|` inside a value becomes `/`.

**Look things up:**

```sh
cd .jira-git-cache
cat PLAT/PLAT-123/issue.md                                       # one issue
grep '| In Progress |' PLAT/_index.md                            # the In Progress column
grep -h '| Jane Doe |' */_index.md                               # Jane's issues, all projects
grep -rli 'watch leak' --include=issue.md .                      # full-text search
grep -rlw 'PLAT-100' --include=issue.md . | grep -v /PLAT-100/   # issues that mention PLAT-100
grep -rlE '^- Sprint: (.*, )?Sprint 42(,|$)' --include=issue.md . # Sprint 42's issues
```

`issue.md` puts each field on its own line with a fixed label. Multiple values are joined with `, `. Custom fields follow the built-in fields, sorted by configured name, labelled by title-casing it (`story_points` becomes `Story Points`):

```
# PLAT-123: Controller leaks watches on restart

- Status: In Progress
- Resolution: Unresolved
- Type: Bug
- Priority: High
- Assignee: Jane Doe
- Reporter: John Smith
- Labels: controller, k8s
- Parent: PLAT-100
- Links: blocks PLAT-130, relates to INFRA-77
- Created: 2026-10-01T14:02:11Z
- Updated: 2026-10-20T09:15:40Z
- URL: https://jira.example.com/browse/PLAT-123
- Epic Link: PLAT-90
- Sprint: Sprint 41, Sprint 42
- Story Points: 3
- Team: Platform

## Description

...

## Comments

### Jane Doe — 2026-10-19T17:30:02Z

...
```

For structured queries, use `issue.json`. Mapped custom fields sit under `custom` by their configured name:

```sh
jq -r 'select(.resolution == null and .custom.story_points >= 5) | .key' */*/issue.json
```

Issues outside the window are not in the cache. A missing file does not mean the issue doesn't exist; look it up in Jira.

Send changes to Jira. The cache catches up on the next run.

## Change what's synced

**You need:** write access to `main`.

**Run:** edit `.jira-git-cache/config.yml` or the workflow, and merge to `main`.

| Change | Edit | Effect on the next run |
|---|---|---|
| Add a project | `projects` | Its issues are fetched. |
| Remove a project | `projects` | Its directory disappears. |
| Widen or narrow the window | `window` | Issues entering are fetched. Issues leaving disappear. |
| Map, unmap, or rename a custom field | `fields` | Full refresh. |
| Point at another Jira | `jira_url` | Full refresh. |
| Change how often it syncs | the workflow's `cron` | New schedule. |
| Change how often it fully refreshes | `full_refresh_every` | New cadence. |
| Upgrade `jira-git-cache` | the workflow's `TOOL` | Full refresh if `schema_version` changed. Older readers are warned, or refused if `schema_version` changed. |

`.jira-git-cache/_meta/fields.json` lists every custom field's id, for finding ids to map.

## When the cache is stale

`fetched_at` stops advancing when runs fail. Find the failure:

```sh
gh run list --workflow jira-sync.yaml --limit 5
gh run view <run-id> --log-failed
```

| Symptom | Fix |
|---|---|
| `401 Unauthorized: Jira rejected the token` | The token is invalid or expired. Update the `JIRA_TOKEN` secret in the `jira` environment. |
| `x509: certificate signed by unknown authority` | Trust Jira's CA on the runner. |
| Connection timeout to Jira | The runner can't reach Jira. Check its network. |
| `429` after retries | Jira rate-limits the service account. Lower `concurrency`. |
| Push rejected | The ruleset on `dev/jira-cache` doesn't let GitHub Actions bypass it, or the branch changed during the run; run again. |
| Job exceeded its time limit | A full refresh outgrew `timeout-minutes`. Raise it. |
| Runs stay queued | No runner matches `runs-on`, or the runner group excludes this repo. |
| No runs at all | The workflow isn't on the default branch. |
