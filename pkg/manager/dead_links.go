package manager

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sirrobot01/decypharr/pkg/manager/link"
	"github.com/sirrobot01/decypharr/pkg/storage"
)

const (
	// deadLinkStrikes is how many dead-item answers in a row, within
	// deadLinkWindow, mark a file dead. One such answer can be a provider
	// blip, so a few tries with backoff come first.
	deadLinkStrikes = 3
	// deadLinkWindow bounds how far apart those answers may be. Older strikes
	// are forgotten, so rare blips over hours never add up to a dead file.
	deadLinkWindow = 2 * time.Minute
	// deadLinkTTL is how long a dead file fails at once on open, without a
	// provider call.
	deadLinkTTL = 6 * time.Hour
	// deadLinkMax caps the number of tracked files.
	deadLinkMax = 4096
	// deadLinkCode is the link error code a session returns for a dead file.
	deadLinkCode = "dead_link"
)

// deadLinkBackoffBase is the first wait between dead-item answers (doubled
// after each). With deadLinkStrikes = 3 the file is marked dead after about
// 6 seconds of waits, well inside a reader's own timeout. A var for tests.
var deadLinkBackoffBase = 2 * time.Second

// deadItemCodes are provider error codes on the download-link call that mean
// the item itself can't be served, as opposed to a busy or throttled API.
// TorBox answers requestdl with HTTP 500 and {"error":"DATABASE_ERROR"} for
// every file of such an item, also after a re-add.
var deadItemCodes = map[string]bool{
	"DATABASE_ERROR": true,
	"ITEM_NOT_FOUND": true,
	"LINK_OFFLINE":   true,
}

var providerCodeRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)

// providerErrorCode reads the error code from a provider API error body
// ({"success": false, "error": "CODE", ...}). Empty when the body is not one.
func providerErrorCode(body []byte) string {
	var v struct {
		Success *bool  `json:"success"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	if v.Success != nil && *v.Success {
		return ""
	}
	code := strings.TrimSpace(v.Error)
	if !providerCodeRe.MatchString(code) {
		return ""
	}
	return code
}

// isDeadItemCode reports whether a link error carries a dead-item code.
func isDeadItemCode(lerr *link.Error) bool {
	return lerr != nil && deadItemCodes[lerr.Code]
}

// deadLinks remembers files whose provider keeps answering that the item
// can't be served. Without it the read path retries such a file without end
// (session resumes, chunk retries, a fresh downloader per waiter, the breaker
// reset after its cooldown), and a media probe over the mount hangs.
type deadLinks struct {
	mu    sync.Mutex
	items map[string]*deadLink
	now   func() time.Time
}

type deadLink struct {
	strikes   int
	first     time.Time
	code      string
	deadUntil time.Time
}

func newDeadLinks() *deadLinks {
	return &deadLinks{items: make(map[string]*deadLink), now: time.Now}
}

// strike records one dead-item answer for key. It returns true when this
// answer marks the file dead.
func (d *deadLinks) strike(key, code string) bool {
	if d == nil || key == "" {
		return false
	}
	now := d.now()
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.items[key]
	if !ok {
		if len(d.items) >= deadLinkMax {
			d.pruneLocked(now)
			if len(d.items) >= deadLinkMax {
				d.items = make(map[string]*deadLink)
			}
		}
		e = &deadLink{}
		d.items[key] = e
	}
	if now.Before(e.deadUntil) {
		return false // already dead; the first marking reported it
	}
	if e.strikes == 0 || now.Sub(e.first) > deadLinkWindow {
		e.strikes, e.first = 0, now
	}
	e.strikes++
	e.code = code
	if e.strikes < deadLinkStrikes {
		return false
	}
	e.deadUntil = now.Add(deadLinkTTL)
	return true
}

// dead returns the provider code when key is marked dead and the mark is fresh.
func (d *deadLinks) dead(key string) (string, bool) {
	if d == nil || key == "" {
		return "", false
	}
	now := d.now()
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.items[key]
	if !ok || !now.Before(e.deadUntil) {
		return "", false
	}
	return e.code, true
}

// clear forgets key after a successful open.
func (d *deadLinks) clear(key string) {
	if d == nil || key == "" {
		return
	}
	d.mu.Lock()
	delete(d.items, key)
	d.mu.Unlock()
}

func (d *deadLinks) pruneLocked(now time.Time) {
	for k, e := range d.items {
		if !now.Before(e.deadUntil) && now.Sub(e.first) > deadLinkWindow {
			delete(d.items, k)
		}
	}
}

func deadLinkKey(entry *storage.Entry, filename string) string {
	if entry == nil {
		return ""
	}
	return strings.ToLower(entry.InfoHash) + "/" + filename
}

func deadLinkError(code string) *link.Error {
	return link.NewPermanentError(
		fmt.Errorf("provider answered %s for this file %d times, marked dead for %s", code, deadLinkStrikes, deadLinkTTL),
		deadLinkCode)
}

func deadLinkBackoff(attempt int) time.Duration {
	return deadLinkBackoffBase << min(max(attempt, 0), 4)
}

// recordDeadItem lists a file marked dead at GET /api/refusals/dead, so jobs
// next to the arrs can drop the download before an import reaches it.
func (m *Manager) recordDeadItem(entry *storage.Entry, filename, code string) {
	if entry == nil {
		return
	}
	m.logger.Warn().
		Str("hash", entry.InfoHash).
		Str("name", entry.Name).
		Str("file", filename).
		Str("provider", entry.ActiveProvider).
		Str("error", code).
		Msg("Download link dead, reads of this file now fail at once")
	if m.deadItems == nil {
		return
	}
	m.deadItems.record(Refusal{
		Name:     entry.Name,
		InfoHash: entry.InfoHash,
		Category: entry.Category,
		Reason:   "download link dead",
		File:     filename,
		Provider: entry.ActiveProvider,
		Error:    code,
	})
}

// DeadItems returns the items with a file marked dead since the given time,
// newest first.
func (m *Manager) DeadItems(since time.Time) []Refusal {
	return m.deadItems.list(since)
}
