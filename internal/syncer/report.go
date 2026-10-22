package syncer

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/textfmt"
)

// CommitMessage summarizes the sync, such as "sync: PLAT=1204 INFRA=387 fetched=37 reused=1554 removed=4".
func (r Result) CommitMessage() string {
	var b strings.Builder
	b.WriteString("sync:")
	for _, p := range r.Projects {
		fmt.Fprintf(&b, " %s=%d", p.Project, r.Snapshot.Meta.Projects[p.Project].Count)
	}
	fmt.Fprintf(&b, " fetched=%d reused=%d removed=%d", r.Fetched, r.Reused, r.Removed)
	return b.String()
}

// Report writes one line of counts per project, then the number of issues fetched in elapsed.
func (r Result) Report(w io.Writer, elapsed time.Duration) {
	keyWidth := 0
	var widths [5]int
	rows := make([][5]string, len(r.Projects))
	for i, p := range r.Projects {
		keyWidth = max(keyWidth, len(p.Project)+3)
		for j, n := range [5]int{p.InWindow, p.New, p.Changed, p.Reused, p.Removed} {
			rows[i][j] = textfmt.Count(n)
			widths[j] = max(widths[j], len(rows[i][j]))
		}
	}
	unconfigured := r.Removed
	for i, p := range r.Projects {
		c := rows[i]
		fmt.Fprintf(w, "%-*s%*s in window: %*s new, %*s changed, %*s reused, %*s removed\n", keyWidth, p.Project,
			widths[0], c[0], widths[1], c[1], widths[2], c[2], widths[3], c[3], widths[4], c[4])
		unconfigured -= p.Removed
	}
	if unconfigured > 0 {
		fmt.Fprintf(w, "removed %s of unconfigured projects\n", textfmt.Issues(unconfigured))
	}
	fmt.Fprintf(w, "fetched %s in %s\n", textfmt.Issues(r.Fetched), textfmt.Duration(elapsed))
}
