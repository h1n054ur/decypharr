package manager

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sirrobot01/decypharr/internal/config"
	"github.com/sirrobot01/decypharr/internal/customerror"
)

const (
	refusalTTL = 24 * time.Hour
	refusalMax = 1000
)

// Refusal is a release the arr sent that was refused because no provider had
// it cached. The arr counts such a grab as skipped, not failed, and keeps
// retrying the same release from its pending queue. A job that can reach the
// arr reads this list and blocklists the matching pending items, so the arr
// moves on to another release.
type Refusal struct {
	Name     string    `json:"name"`
	InfoHash string    `json:"info_hash"`
	Category string    `json:"category"`
	Reason   string    `json:"reason"`
	Time     time.Time `json:"time"`
	Count    int       `json:"count"`
}

// refusalLog is a bounded, short-lived record of refusals, keyed by
// category and info hash. Repeats refresh the time and bump the count.
type refusalLog struct {
	mu    sync.Mutex
	ttl   time.Duration
	max   int
	items map[string]*Refusal
	now   func() time.Time
}

func newRefusalLog(ttl time.Duration, maxItems int) *refusalLog {
	return &refusalLog{ttl: ttl, max: maxItems, items: make(map[string]*Refusal), now: time.Now}
}

func (l *refusalLog) record(r Refusal) {
	if l == nil || r.InfoHash == "" {
		return
	}
	r.InfoHash = strings.ToLower(r.InfoHash)
	key := r.Category + "|" + r.InfoHash
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
	if old, ok := l.items[key]; ok {
		old.Time = now
		old.Count++
		old.Reason = r.Reason
		if r.Name != "" {
			old.Name = r.Name
		}
		return
	}
	if len(l.items) >= l.max {
		var oldestKey string
		var oldest time.Time
		for k, v := range l.items {
			if oldestKey == "" || v.Time.Before(oldest) {
				oldestKey, oldest = k, v.Time
			}
		}
		delete(l.items, oldestKey)
	}
	r.Time = now
	r.Count = 1
	l.items[key] = &r
}

// list returns the refusals newer than since (all live ones when since is
// zero), newest first.
func (l *refusalLog) list(since time.Time) []Refusal {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.now())
	out := make([]Refusal, 0, len(l.items))
	for _, v := range l.items {
		if v.Time.After(since) {
			out = append(out, *v)
		}
	}
	slices.SortFunc(out, func(a, b Refusal) int { return b.Time.Compare(a.Time) })
	return out
}

func (l *refusalLog) pruneLocked(now time.Time) {
	for k, v := range l.items {
		if now.Sub(v.Time) > l.ttl {
			delete(l.items, k)
		}
	}
}

// isNotCachedRefusal reports whether a submit error means no provider had the
// torrent cached: the provider said so after the add, the pre-add cache check
// said so, or the hearsay gate skipped a hash recently proven uncached.
func isNotCachedRefusal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, customerror.TorrentNotCachedError) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not cached")
}

func (m *Manager) recordRefusal(req *ImportRequest, err error) {
	if m.refusals == nil || req == nil || req.Magnet == nil || !isNotCachedRefusal(err) {
		return
	}
	if !config.Get().QueueCleanup.RecordsUncachedRefusals() {
		return
	}
	reason := "torrent not cached"
	if strings.Contains(strings.ToLower(err.Error()), "recently proven") {
		reason = "recently proven not cached"
	}
	m.refusals.record(Refusal{
		Name:     req.Magnet.Name,
		InfoHash: req.Magnet.InfoHash,
		Category: req.Arr.Name,
		Reason:   reason,
	})
	m.logger.Info().
		Str("arr", req.Arr.Name).
		Str("hash", req.Magnet.InfoHash).
		Str("name", req.Magnet.Name).
		Msg("Recorded uncached refusal")
}

// UncachedRefusals returns the releases refused as not cached since the given
// time, newest first.
func (m *Manager) UncachedRefusals(since time.Time) []Refusal {
	return m.refusals.list(since)
}
