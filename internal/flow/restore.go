package flow

import (
	"log"
	"regexp"
	"strconv"
	"time"
)

// Persistence: loading flow records back at startup. Everything else about a
// restarted flow is re-derived, not replayed — see Restore.

// flowIDPattern matches the IDs this package assigns, used to recover the
// f<n> sequence across a restart so a new flow cannot reuse a live ID. It is
// queue's taskIDPattern with f for t.
var flowIDPattern = regexp.MustCompile(`^f(\d+)$`)

// Restore reads every persisted flow and returns the records together with
// the highest f<n> sequence number seen, which the manager continues minting
// from.
//
// Restore does nothing else. There is deliberately no event replay: the next
// reconciler pass re-derives each non-terminal flow's position from its
// tasks, which the queue has already restored on its own terms (a task
// recorded running comes back blocked with its attempt refunded). That is
// what makes the two awkward restart cases fall out for free — a review task
// that reached done while the daemon was dead is simply read next pass, and a
// re-run review attempt rewrites the same r<N>-a<attempt> verdict path, so no
// ordering between this call and the queue starting is load-bearing.
//
// Parse failures are logged and skipped rather than returned — one corrupt
// state file must not stop the daemon from serving the other flows.
func Restore(s *Store) ([]*Flow, int64) {
	records, problems := s.Load()
	for _, p := range problems {
		log.Printf("flow: %v", p)
	}

	var (
		flows []*Flow
		seq   int64
	)
	for i := range records {
		rec := records[i]
		if rec.ID == "" {
			continue
		}
		f := rec
		if f.CreatedAt.IsZero() {
			f.CreatedAt = time.Now()
		}
		// A record whose rounds slice was written as null reads back nil;
		// callers iterate it, and an empty slice keeps the wire shape stable.
		if f.Rounds == nil {
			f.Rounds = []*Round{}
		}
		flows = append(flows, &f)
		if n := flowIDSeq(f.ID); n > seq {
			seq = n
		}
	}
	return flows, seq
}

// flowIDSeq extracts the numeric suffix of a flow ID ("f7" -> 7), returning
// 0 for anything this package did not mint.
func flowIDSeq(id string) int64 {
	match := flowIDPattern.FindStringSubmatch(id)
	if match == nil {
		return 0
	}
	n, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0
	}
	return n
}
