// Execution-error truncation, shared by both agents' "job_history" command
// handler (internal/poller/executor.go on Linux, internal/winagentcore's
// Executor on Windows) so a single history payload sent back to Central can
// never balloon to megabytes.
//
// A failed rsync run using --info=progress2 redraws its progress line with
// a bare '\r' (no '\n'). bufio.Scanner's default ScanLines split function
// only terminates tokens on '\n', so every '\r' update gets silently
// concatenated into ONE ever-growing token; when rsync finally exits
// non-zero that whole blob becomes the stored error message. Seen in
// production: a single execution's error reaching ~9 MB, 18 executions
// totalling ~43 MB for one job_history reply -- enough to make the
// Central dashboard's per-agent timeline page fail to render for that
// agent (looks like "nothing shows up"), even though the agent itself
// and its other jobs are healthy.
//
// This does not fix the underlying progress-parsing bug (see
// internal/backup's \r-aware scanner split function for that); it is a
// second, independent safety net so a message this large can never leave
// an agent over the command-queue channel in the first place, regardless
// of which agent build produced it.
package core

// MaxExecutionErrorLen is the cap applied to an execution's stored error
// message before it is included in a "job_history" command response.
const MaxExecutionErrorLen = 4000

// TruncateExecutionError shortens msg to MaxExecutionErrorLen runes,
// appending a marker that states how many characters were cut so nothing
// is silently hidden -- consistent with the project's rule that a
// truncation must always say what it dropped.
func TruncateExecutionError(msg string) string {
	r := []rune(msg)
	if len(r) <= MaxExecutionErrorLen {
		return msg
	}
	cut := len(r) - MaxExecutionErrorLen
	return string(r[:MaxExecutionErrorLen]) + "… (mensagem truncada, " + itoa(cut) + " caractere(s) a mais omitido(s))"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
