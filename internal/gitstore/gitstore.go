// Package gitstore reads and writes snapshot branches with git plumbing.
package gitstore

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// killDelay is how long a cancelled git has to exit before it is killed.
const killDelay = 3 * time.Second

// BotName and BotEmail identify the author and committer of snapshot commits.
const (
	BotName  = "github-actions[bot]"
	BotEmail = "41898282+github-actions[bot]@users.noreply.github.com"
)

// Repo methods return these errors.
var (
	ErrBranchNotFound = errors.New("branch not found on remote")
	ErrRemoteMoved    = errors.New("the cache branch changed on the remote during this run; run again")
)

// Error reports a git command that exited non-zero.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

// Error names the command, its exit status and its stderr.
func (e *Error) Error() string {
	msg := fmt.Sprintf("git %s: %v", strings.Join(e.Args, " "), e.Err)
	if stderr := strings.TrimSpace(e.Stderr); stderr != "" {
		msg += ": " + stderr
	}
	return msg
}

// Unwrap returns the exec error.
func (e *Error) Unwrap() error { return e.Err }

// Repo is a git work tree.
type Repo struct{ Dir string }

// Open returns the repository whose work tree contains dir.
func Open(ctx context.Context, dir string) (*Repo, error) {
	out, err := (&Repo{Dir: dir}).git(ctx, "", nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	return &Repo{Dir: strings.TrimSuffix(out, "\n")}, nil
}

// DefaultBranch returns the branch that remote's HEAD points to.
func (r *Repo) DefaultBranch(ctx context.Context, remote string) (string, error) {
	out, err := r.git(ctx, "", nil, "ls-remote", "--symref", remote, "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if target, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
			return strings.TrimSuffix(target, "\tHEAD"), nil
		}
	}
	return "", fmt.Errorf("%s has no default branch", remote)
}

// RemoteHasBranch reports whether remote has branch.
func (r *Repo) RemoteHasBranch(ctx context.Context, remote, branch string) (bool, error) {
	_, err := r.git(ctx, "", nil, "ls-remote", "--exit-code", "--heads", remote, "refs/heads/"+branch)
	if exitedWith(err, 2) {
		return false, nil
	}
	return err == nil, err
}

// FetchFiles fetches the tip of branch into ref and returns its commit and
// regular files.
func (r *Repo) FetchFiles(ctx context.Context, remote, branch, ref string) (commit string, files map[string][]byte, err error) {
	if _, err := r.git(ctx, "", nil, "fetch", "--no-tags", "--depth", "1", remote, "+refs/heads/"+branch+":"+ref); err != nil {
		if has, hasErr := r.RemoteHasBranch(ctx, remote, branch); hasErr == nil && !has {
			return "", nil, ErrBranchNotFound
		}
		return "", nil, err
	}
	s := &session{ctx: ctx, r: r}
	objects := s.git("", "rev-list", "--objects", "--missing=print", ref)
	commit, _, _ = strings.Cut(objects, "\n")
	// A partial clone would otherwise fetch each missing object in its own request.
	for missing := missingObjects(objects); missing != ""; missing = missingObjects(objects) {
		s.git(missing, "-c", "fetch.negotiationAlgorithm=noop",
			"fetch", remote, "--no-write-fetch-head", "--filter=blob:none", "--stdin")
		objects = s.git("", "rev-list", "--objects", "--missing=print", commit)
	}
	paths, ids := regularFiles(s.git("", "ls-tree", "-r", "-z", commit))
	batch := s.git(ids, "cat-file", "--batch")
	if s.err != nil {
		return "", nil, s.err
	}
	files, err = parseBatch(paths, batch)
	return commit, files, err
}

func missingObjects(revList string) string {
	var missing strings.Builder
	for _, line := range strings.Split(revList, "\n") {
		if id, ok := strings.CutPrefix(line, "?"); ok {
			missing.WriteString(id + "\n")
		}
	}
	return missing.String()
}

func regularFiles(lsTree string) (paths []string, ids string) {
	var b strings.Builder
	for _, entry := range strings.Split(lsTree, "\x00") {
		meta, path, _ := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if len(fields) == 3 && (fields[0] == "100644" || fields[0] == "100755") {
			paths = append(paths, path)
			b.WriteString(fields[2] + "\n")
		}
	}
	return paths, b.String()
}

func parseBatch(paths []string, batch string) (map[string][]byte, error) {
	files := make(map[string][]byte, len(paths))
	for _, path := range paths {
		header, rest, _ := strings.Cut(batch, "\n")
		var id, kind string
		var size int
		if _, err := fmt.Sscanf(header, "%s %s %d", &id, &kind, &size); err != nil {
			return nil, fmt.Errorf("cat-file %q: %w", header, err)
		}
		files[path] = []byte(rest[:size])
		batch = rest[size+1:]
	}
	return files, nil
}

// Publish pushes files to branch as one parentless commit by the bot. The
// push fails with ErrRemoteMoved unless branch is at expect, or absent when
// expect is empty.
func (r *Repo) Publish(ctx context.Context, remote, branch, expect string, files map[string][]byte, message string, when time.Time) (commit string, err error) {
	tmp, err := os.MkdirTemp("", "jira-git-cache-")
	if err != nil {
		return "", fmt.Errorf("publish: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	paths := slices.Sorted(maps.Keys(files))
	var blobs strings.Builder
	for i, path := range paths {
		name := filepath.Join(tmp, strconv.Itoa(i))
		if err := os.WriteFile(name, files[path], 0o600); err != nil {
			return "", fmt.Errorf("publish: %w", err)
		}
		blobs.WriteString(name + "\n")
	}

	date := fmt.Sprintf("@%d +0000", when.Unix())
	s := &session{ctx: ctx, r: r, env: []string{
		"GIT_INDEX_FILE=" + filepath.Join(tmp, "index"),
		"GIT_AUTHOR_NAME=" + BotName, "GIT_AUTHOR_EMAIL=" + BotEmail, "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=" + BotName, "GIT_COMMITTER_EMAIL=" + BotEmail, "GIT_COMMITTER_DATE=" + date,
	}}
	var index strings.Builder
	for i, id := range strings.Fields(s.git(blobs.String(), "hash-object", "-w", "--no-filters", "--stdin-paths")) {
		fmt.Fprintf(&index, "100644 %s\t%s\x00", id, paths[i])
	}
	s.git(index.String(), "update-index", "-z", "--index-info")
	// update-index skips invalid paths and still exits 0.
	if rejected := unlisted(paths, s.git("", "ls-files", "-z")); s.err == nil && len(rejected) > 0 {
		s.err = fmt.Errorf("publish: git rejected the paths %q", rejected)
	}
	tree := strings.TrimSpace(s.git("", "write-tree"))
	commit = strings.TrimSpace(s.git("", "commit-tree", tree, "-m", message))
	target := "refs/heads/" + branch
	s.env = nil
	s.git("", "push", "--force-with-lease="+target+":"+expect, remote, commit+":"+target)

	var gitErr *Error
	if errors.As(s.err, &gitErr) && leaseFailed(gitErr.Stderr) {
		return "", ErrRemoteMoved
	}
	if s.err != nil {
		return "", s.err
	}
	return commit, nil
}

// leaseFailed reports whether a push failed because the remote ref was not at
// the expected commit. The client reports "stale info" before it sends. The
// server reports "but expected" when the ref moves during the push.
func leaseFailed(stderr string) bool {
	return strings.Contains(stderr, "(stale info)") || strings.Contains(stderr, " but expected ")
}

// NotIgnored returns the paths that git does not ignore. Tracked paths are
// never ignored.
func (r *Repo) NotIgnored(ctx context.Context, paths []string) ([]string, error) {
	var input strings.Builder
	for _, path := range paths {
		input.WriteString(path + "\x00")
	}
	out, err := r.git(ctx, input.String(), nil, "check-ignore", "-z", "--stdin")
	if err != nil && !exitedWith(err, 1) {
		return nil, err
	}
	return unlisted(paths, out), nil
}

// Tracked reports whether the index holds path.
func (r *Repo) Tracked(ctx context.Context, path string) (bool, error) {
	_, err := r.git(ctx, "", nil, "ls-files", "--error-unmatch", "--", path)
	if exitedWith(err, 1) {
		return false, nil
	}
	return err == nil, err
}

// unlisted returns the paths missing from a NUL-separated list.
func unlisted(paths []string, list string) []string {
	listed := map[string]bool{}
	for _, path := range strings.FieldsFunc(list, func(r rune) bool { return r == 0 }) {
		listed[path] = true
	}
	var missing []string
	for _, path := range paths {
		if !listed[path] {
			missing = append(missing, path)
		}
	}
	return missing
}

func (r *Repo) git(ctx context.Context, stdin string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	// SIGKILL would leave git's lock files, such as .git/shallow.lock, behind.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = killDelay
	cmd.Dir = r.Dir
	cmd.Env = append(append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0"), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return string(out), nil
}

// session runs git commands until one fails. It skips the commands after
// that, and err holds the failure.
type session struct {
	ctx context.Context
	r   *Repo
	env []string
	err error
}

func (s *session) git(stdin string, args ...string) string {
	if s.err != nil {
		return ""
	}
	var out string
	out, s.err = s.r.git(s.ctx, stdin, s.env, args...)
	return out
}

func exitedWith(err error, code int) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == code
}
