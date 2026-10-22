package syncer_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/snapshot"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/syncer"
)

func exampleResult() syncer.Result {
	return syncer.Result{
		Snapshot: snapshot.Snapshot{Meta: snapshot.Meta{Projects: map[string]snapshot.ProjectMeta{
			"PLAT": {Count: 1204}, "INFRA": {Count: 387},
		}}},
		Projects: []syncer.ProjectStats{
			{Project: "PLAT", InWindow: 1204, New: 1204},
			{Project: "INFRA", InWindow: 387, New: 387},
		},
		Fetched: 1591,
	}
}

func TestReport(t *testing.T) {
	var b strings.Builder

	exampleResult().Report(&b, 161*time.Second)

	want := "" +
		"PLAT    1,204 in window: 1,204 new, 0 changed, 0 reused, 0 removed\n" +
		"INFRA     387 in window:   387 new, 0 changed, 0 reused, 0 removed\n" +
		"fetched 1,591 issues in 2m41s\n"
	if b.String() != want {
		t.Errorf("Report =\n%s\nwant\n%s", b.String(), want)
	}
}

func TestReportAlignsEachColumn(t *testing.T) {
	var b strings.Builder
	r := syncer.Result{Projects: []syncer.ProjectStats{
		{Project: "A", InWindow: 5, New: 1, Changed: 1000, Reused: 4, Removed: 10},
		{Project: "LONGER", InWindow: 1000, New: 22, Changed: 3, Reused: 975, Removed: 0},
	}, Fetched: 1025}

	r.Report(&b, 2*time.Hour)

	want := "" +
		"A            5 in window:  1 new, 1,000 changed,   4 reused, 10 removed\n" +
		"LONGER   1,000 in window: 22 new,     3 changed, 975 reused,  0 removed\n" +
		"fetched 1,025 issues in 2h\n"
	if b.String() != want {
		t.Errorf("Report =\n%s\nwant\n%s", b.String(), want)
	}
}

func TestCommitMessage(t *testing.T) {
	r := exampleResult()
	r.Fetched, r.Reused, r.Removed = 37, 1554, 4

	if got, want := r.CommitMessage(), "sync: PLAT=1204 INFRA=387 fetched=37 reused=1554 removed=4"; got != want {
		t.Errorf("CommitMessage = %q, want %q", got, want)
	}
}
