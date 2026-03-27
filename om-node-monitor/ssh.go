package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Known auth log paths in order of preference.
var authLogCandidates = []string{
	"/var/log/auth.log",  // Debian / Ubuntu
	"/var/log/secure",    // RHEL / CentOS / Fedora / Arch
	"/var/log/messages",  // Some legacy distros
}

// sshEvent holds compiled regex and handler metadata.
type sshEvent struct {
	re       *regexp.Regexp
	handle   func(m []string)
}

// Brute-force detection: track failed logins per source IP.
var (
	bfMu     sync.Mutex
	bfCounts = make(map[string]*bfTracker)
)

type bfTracker struct {
	count int
	since time.Time
}

// trackFailedLogin increments the counter for ip and returns true if the
// brute-force threshold is reached within the window.
func trackFailedLogin(ip string) bool {
	bfMu.Lock()
	defer bfMu.Unlock()

	bfWindow := time.Duration(config.SSHBruteForceWindow) * time.Second
	t, ok := bfCounts[ip]
	if !ok || time.Since(t.since) > bfWindow {
		bfCounts[ip] = &bfTracker{count: 1, since: time.Now()}
		return false
	}
	t.count++
	return t.count >= config.SSHBruteForceThreshold
}

// watchSSH finds the best log source and tails it for SSH events.
func watchSSH() {
	// Try direct log file first.
	for _, path := range authLogCandidates {
		if _, err := os.Stat(path); err == nil {
			emitEvent(fmt.Sprintf("SSH monitoring started (file: %s)", path), nil)
			tailAuthLog(path)
			return
		}
	}

	// Fall back to journalctl.
	if _, err := exec.LookPath("journalctl"); err == nil {
		emitEvent("SSH monitoring started (source: journalctl)", nil)
		tailJournalctl()
		return
	}

	emitError("SSH monitoring: no suitable log source found (no auth.log/secure and journalctl not available)")
}

// tailAuthLog tails a syslog-style file for SSH messages.
// Handles log rotation via inode comparison.
func tailAuthLog(path string) {
	f, err := os.Open(path)
	if err != nil {
		emitError(fmt.Sprintf("ssh: cannot open %s: %v", path, err))
		return
	}
	defer f.Close()

	// Seek to end so we only process new events.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		emitError(fmt.Sprintf("ssh: seek failed on %s: %v", path, err))
		return
	}

	reader := bufio.NewReader(f)

	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			processAuthLine(strings.TrimRight(line, "\r\n"))
		}
		if err == io.EOF {
			// Check for log rotation (inode change).
			if rotated, newFile := checkRotation(f, path); rotated && newFile != nil {
				f.Close()
				f = newFile
				reader = bufio.NewReader(f)
			} else {
				time.Sleep(500 * time.Millisecond)
			}
			continue
		}
		if err != nil {
			emitError(fmt.Sprintf("ssh: read error on %s: %v", path, err))
			return
		}
	}
}

// checkRotation returns (true, newFile) if the log file at path has been
// rotated since f was opened.
func checkRotation(f *os.File, path string) (bool, *os.File) {
	oldStat, err1 := f.Stat()
	newStat, err2 := os.Stat(path)
	if err1 != nil || err2 != nil {
		return false, nil
	}
	if os.SameFile(oldStat, newStat) {
		return false, nil
	}
	newFile, err := os.Open(path)
	if err != nil {
		return false, nil
	}
	return true, newFile
}

// tailJournalctl runs "journalctl -u sshd -u ssh -f --output=cat --no-pager"
// and processes each output line.
func tailJournalctl() {
	cmd := exec.Command("journalctl",
		"-u", "sshd", "-u", "ssh",
		"-f", "--output=cat", "--no-pager", "--lines=0",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		emitError("ssh: journalctl pipe: " + err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		emitError("ssh: journalctl start: " + err.Error())
		return
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		processAuthLine(scanner.Text())
	}
	if err := cmd.Wait(); err != nil {
		emitError("ssh: journalctl exited: " + err.Error())
	}
}

// ---- SSH log line parser ----------------------------------------------------

// Pre-compiled regexes for common sshd log patterns.
var (
	// Accepted password|publickey|gssapi-* for USER from IP port PORT
	reAccepted = regexp.MustCompile(
		`Accepted (\S+) for (\S+) from (\S+) port (\d+)`)

	// Failed password for [invalid user] USER from IP port PORT
	reFailed = regexp.MustCompile(
		`Failed \S+ for (?:invalid user )?(\S+) from (\S+) port (\d+)`)

	// Invalid user USER from IP [port PORT]
	reInvalidUser = regexp.MustCompile(
		`Invalid user (\S+) from (\S+)`)

	// Disconnected from [authenticating|invalid] user USER IP port PORT [preauth]
	reDisconnect = regexp.MustCompile(
		`Disconnected from (?:\S+ user )?(\S+) (\S+) port (\d+)`)

	// Connection closed by [authenticating|invalid] user USER IP port PORT [preauth]
	reConnClosed = regexp.MustCompile(
		`Connection closed by (?:\S+ user )?(\S+) port (\d+)`)

	// pam_unix(sshd:session): session opened/closed for user USER
	reSessionOpen  = regexp.MustCompile(`session opened for user (\S+)`)
	reSessionClose = regexp.MustCompile(`session closed for user (\S+)`)

	// Maximum authentication attempts exceeded
	reMaxAuth = regexp.MustCompile(
		`maximum authentication attempts exceeded.*from (\S+) port (\d+)`)
)

// processAuthLine extracts SSH events from a single syslog line.
func processAuthLine(line string) {
	// Quick filter: must mention sshd.
	if !strings.Contains(line, "sshd") {
		return
	}

	// --- Successful login ---
	if m := reAccepted.FindStringSubmatch(line); m != nil {
		// m[1]=method m[2]=user m[3]=ip m[4]=port
		emitEvent(
			fmt.Sprintf("SSH login: user=%s method=%s from=%s:%s", m[2], m[1], m[3], m[4]),
			map[string]string{
				"severity": "info",
				"user":     m[2],
				"src_ip":   m[3],
				"method":   m[1],
			},
		)
		return
	}

	// --- Failed password ---
	if m := reFailed.FindStringSubmatch(line); m != nil {
		// m[1]=user m[2]=ip m[3]=port
		ip := m[2]
		brute := trackFailedLogin(ip)
		if brute && canAlert("ssh_brute_"+ip) {
			emitAlert(
				fmt.Sprintf("SSH brute-force detected: %d+ failed logins from %s", config.SSHBruteForceThreshold, ip),
				map[string]string{
					"severity": "critical",
					"src_ip":   ip,
					"user":     m[1],
				},
				fmt.Sprintf("ssh-brute-%s-%d", ip, time.Now().Unix()),
			)
		} else if canAlert("ssh_fail_"+ip) {
			emitAlert(
				fmt.Sprintf("SSH failed login: user=%s from=%s:%s", m[1], ip, m[3]),
				map[string]string{
					"severity": "warning",
					"src_ip":   ip,
					"user":     m[1],
				},
				fmt.Sprintf("ssh-fail-%s-%d", ip, time.Now().Unix()),
			)
		}
		return
	}

	// --- Invalid user ---
	if m := reInvalidUser.FindStringSubmatch(line); m != nil {
		// m[1]=user m[2]=ip
		if canAlert("ssh_invalid_" + m[2]) {
			emitIssue(
				fmt.Sprintf("SSH invalid user: user=%s from=%s", m[1], m[2]),
				map[string]string{
					"severity": "warning",
					"src_ip":   m[2],
					"user":     m[1],
				},
				"",
			)
		}
		return
	}

	// --- Max auth attempts exceeded ---
	if m := reMaxAuth.FindStringSubmatch(line); m != nil {
		// m[1]=ip m[2]=port
		if canAlert("ssh_maxauth_" + m[1]) {
			emitAlert(
				fmt.Sprintf("SSH max auth attempts exceeded from %s:%s", m[1], m[2]),
				map[string]string{
					"severity": "warning",
					"src_ip":   m[1],
				},
				fmt.Sprintf("ssh-maxauth-%s-%d", m[1], time.Now().Unix()),
			)
		}
		return
	}

	// --- Session opened ---
	if m := reSessionOpen.FindStringSubmatch(line); m != nil {
		emitEvent(
			fmt.Sprintf("SSH session opened for user %s", m[1]),
			map[string]string{"severity": "info", "user": m[1]},
		)
		return
	}

	// --- Session closed ---
	if m := reSessionClose.FindStringSubmatch(line); m != nil {
		emitEvent(
			fmt.Sprintf("SSH session closed for user %s", m[1]),
			map[string]string{"severity": "info", "user": m[1]},
		)
		return
	}

	// --- Preauth disconnect (connection scanning / probing) ---
	if strings.Contains(line, "[preauth]") {
		if m := reDisconnect.FindStringSubmatch(line); m != nil {
			// Only emit if we haven't seen many of these from the same IP recently.
			if canAlert("ssh_preauth_" + m[2]) {
				emitEvent(
					fmt.Sprintf("SSH preauth disconnect: user=%s from=%s:%s", m[1], m[2], m[3]),
					map[string]string{"severity": "info", "src_ip": m[2], "user": m[1]},
				)
			}
		} else if m := reConnClosed.FindStringSubmatch(line); m != nil {
			if canAlert("ssh_preauth_closed_" + m[1]) {
				emitEvent(
					fmt.Sprintf("SSH connection probe from %s:%s", m[1], m[2]),
					map[string]string{"severity": "info", "src_ip": m[1]},
				)
			}
		}
	}
}
