package manager

import (
	"time"

	"github.com/puzpuzpuz/xsync/v4"
)

const (
	// redirectCacheTTL bounds how long a resolved redirect target is reused.
	// Provider CDN links outlive this comfortably; a target that dies sooner
	// is dropped on its first failure.
	redirectCacheTTL = 30 * time.Minute
	// redirectCacheMax caps the number of remembered targets.
	redirectCacheMax = 8192
)

// redirectCache remembers where a provider redirect link leads.
//
// Some providers hand out a link that is itself an API call answering with a
// redirect to the CDN (TorBox: /api/torrents/requestdl?redirect=true). Without
// this cache every stream open, idle reconnect, seek and resume pays one call
// against the provider API. Under load (media server probes, Arr imports)
// those calls run into the provider's rate limit, every retry adds another
// call, and streams stay throttled until the process restarts. Reusing the
// resolved CDN target keeps reconnects off the provider API.
//
// The cache is shared by every session of a Manager so concurrent readers of
// one file resolve it once.
type redirectCache struct {
	m   *xsync.Map[string, redirectTarget]
	ttl time.Duration
	now func() time.Time
}

type redirectTarget struct {
	url string
	at  time.Time
}

func newRedirectCache() *redirectCache {
	return &redirectCache{
		m:   xsync.NewMap[string, redirectTarget](),
		ttl: redirectCacheTTL,
		now: time.Now,
	}
}

// get returns the resolved target for link, if one is remembered and fresh.
func (c *redirectCache) get(link string) (string, bool) {
	if c == nil || link == "" {
		return "", false
	}
	t, ok := c.m.Load(link)
	if !ok {
		return "", false
	}
	if c.now().Sub(t.at) > c.ttl {
		c.m.Delete(link)
		return "", false
	}
	return t.url, true
}

// put remembers that link resolved to target.
func (c *redirectCache) put(link, target string) {
	if c == nil || link == "" || target == "" || link == target {
		return
	}
	if c.m.Size() >= redirectCacheMax {
		c.m.Clear()
	}
	c.m.Store(link, redirectTarget{url: target, at: c.now()})
}

// drop forgets the target for link so the next open resolves it again.
func (c *redirectCache) drop(link string) {
	if c == nil || link == "" {
		return
	}
	c.m.Delete(link)
}
