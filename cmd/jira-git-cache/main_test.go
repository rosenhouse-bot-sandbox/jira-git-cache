//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func openPTY(t *testing.T) (controller, terminal *os.File) {
	t.Helper()
	controller, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	fd := int(controller.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	return controller, terminal
}

func echoes(t *testing.T, terminal *os.File) bool {
	t.Helper()
	termios, err := unix.IoctlGetTermios(int(terminal.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	return termios.Lflag&unix.ECHO != 0
}

func waitFor(t *testing.T, path, want string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if data, _ := os.ReadFile(path); strings.Contains(string(data), want) {
			return
		}
	}
	t.Fatalf("%s never showed %q", path, want)
}

func TestInterruptStopsTokenPrompt(t *testing.T) {
	controller, terminal := openPTY(t)
	stderrPath := filepath.Join(t.TempDir(), "stderr")
	stderr, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	stdin, oldStderr := os.Stdin, os.Stderr
	os.Stdin, os.Stderr = terminal, stderr
	t.Cleanup(func() { os.Stdin, os.Stderr = stdin, oldStderr })
	t.Setenv("JIRA_TOKEN", "")

	code := make(chan int, 1)
	go func() { code <- run([]string{"preview", t.TempDir()}) }()
	waitFor(t, stderrPath, "Jira token (hidden; or set JIRA_TOKEN): ")
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-code:
		if got != 1 {
			t.Errorf("exit %d, want 1", got)
		}
	case <-time.After(5 * time.Second):
		_, _ = controller.WriteString("\n")
		t.Fatal("still waiting for the token after SIGINT")
	}
	if !echoes(t, terminal) {
		t.Error("terminal echo left off")
	}
	_, _ = controller.WriteString("\n")
	if got, _ := os.ReadFile(stderrPath); string(got) != "Jira token (hidden; or set JIRA_TOKEN): \n✗ read token: context canceled\n" {
		t.Errorf("stderr %q", got)
	}
}
