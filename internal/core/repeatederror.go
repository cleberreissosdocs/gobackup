package core

import (
	"sync"
	"time"
)

// RepeatedError collapses an error that keeps happening into ONE log line
// per outage, plus one line when it clears.
//
// Why this exists: every loop that retries on a timer (the Central's
// offline checker, an agent's heartbeat) logged its failure on EVERY tick.
// A Postgres blip or a dropped VPN then wrote the same sentence every few
// seconds, so the log filled with hundreds of identical lines exactly when
// someone was reading it to find out what happened — and on the Windows
// agent, whose log rotates at 2 MiB keeping one generation, that spam
// evicts the history that would explain the outage.
//
// What a reader actually needs is three facts: it started, it is still
// going, and it ended. So the first failure is logged, repeats of the SAME
// failure are counted silently, a DIFFERENT failure is logged (reporting
// how many of the previous one were held back, so nothing is silently
// lost), and recovery logs how long the outage lasted and how many
// attempts failed inside it.
//
// The zero value is ready to use. Safe for concurrent use.
type RepeatedError struct {
	mu         sync.Mutex
	message    string // the failure currently being suppressed ("" = none)
	firstAt    time.Time
	suppressed int

	// Now is a seam for tests; nil means time.Now.
	Now func() time.Time
}

func (r *RepeatedError) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Report logs err the first time it is seen and stays quiet while the same
// error repeats. logf receives a format string and arguments, so callers
// can pass log.Printf or any logger with that shape. prefix names the loop
// (e.g. "offline checker").
func (r *RepeatedError) Report(prefix string, err error, logf func(string, ...any)) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	msg := err.Error()
	if msg == r.message {
		r.suppressed++
		return
	}
	// A different failure: say so, and account for the one it replaces
	// instead of letting its repeat count vanish.
	if r.message != "" && r.suppressed > 0 {
		logf("%s: previous error repeated %d more time(s) before changing", prefix, r.suppressed)
	}
	logf("%s: %s", prefix, msg)
	r.message = msg
	r.firstAt = r.now()
	r.suppressed = 0
}

// Resolved reports that the operation succeeded. It logs a single recovery
// line when a failure was in progress — including its duration and how many
// repeats were suppressed — and does nothing when there was no outage, so a
// healthy loop stays silent.
func (r *RepeatedError) Resolved(prefix string, logf func(string, ...any)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.message == "" {
		return
	}
	logf("%s: recovered after %s (%d failed attempt(s), last error: %s)",
		prefix, r.now().Sub(r.firstAt).Round(time.Second), r.suppressed+1, r.message)
	r.message = ""
	r.suppressed = 0
}
