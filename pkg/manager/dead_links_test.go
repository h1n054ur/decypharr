package manager

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirrobot01/decypharr/internal/customerror"
	"github.com/sirrobot01/decypharr/pkg/debrid/types"
	"github.com/sirrobot01/decypharr/pkg/manager/link"
)

// fakeTorBox fronts a byte pattern the way TorBox's requestdl does: the API
// either answers with a JSON error (HTTP 500) or serves the bytes.
type fakeTorBox struct {
	data  []byte
	calls atomic.Int64
	// failFirst > 0 answers that many calls with errBody, then serves.
	// failFirst < 0 answers every call with errBody.
	failFirst atomic.Int64
	errBody   string
}

func (f *fakeTorBox) handler(w http.ResponseWriter, r *http.Request) {
	n := f.calls.Add(1)
	if ff := f.failFirst.Load(); ff < 0 || n <= ff {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, f.errBody)
		return
	}
	_, _ = w.Write(f.data)
}

func startTorBox(t *testing.T, f *fakeTorBox) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	return srv.URL + "/api/torrents/requestdl?torrent_id=1&file_id=2&redirect=true"
}

func fastDeadBackoff(t *testing.T) {
	t.Helper()
	old := deadLinkBackoffBase
	deadLinkBackoffBase = 5 * time.Millisecond
	t.Cleanup(func() { deadLinkBackoffBase = old })
}

func deadTransport(linkURL string, dead *deadLinks, onDead func(string)) *httpTransport {
	return &httpTransport{
		client: http.DefaultClient,
		getLink: func(context.Context) (types.DownloadLink, error) {
			return types.DownloadLink{Filename: "file", DownloadLink: linkURL}, nil
		},
		refresh: func(context.Context, types.DownloadLink) (types.DownloadLink, error) {
			return types.DownloadLink{Filename: "file", DownloadLink: linkURL}, nil
		},
		dead:    dead,
		deadKey: "abc/file",
		onDead:  onDead,
	}
}

const databaseError = `{"success":false,"error":"DATABASE_ERROR","detail":"Database error.","data":null}`

func TestDeadItemFailsFastAfterStrikes(t *testing.T) {
	fastDeadBackoff(t)
	tb := &fakeTorBox{data: testPattern(4096), errBody: databaseError}
	tb.failFirst.Store(-1)
	url := startTorBox(t, tb)
	dead := newDeadLinks()
	var marked []string
	tr := deadTransport(url, dead, func(code string) { marked = append(marked, code) })

	s := newSession(context.Background(), tr, 4096, 0)
	defer s.Close()
	start := time.Now()
	_, err := s.Read(make([]byte, 512))
	if err == nil {
		t.Fatal("read of a dead item succeeded")
	}
	if !customerror.IsPermanentError(err) {
		t.Fatalf("error %v is not permanent, the read path would keep retrying it", err)
	}
	if lerr := link.GetLinkError(err); lerr == nil || lerr.Code != deadLinkCode {
		t.Fatalf("error %v, want code %s", err, deadLinkCode)
	}
	if got := tb.calls.Load(); got != deadLinkStrikes {
		t.Fatalf("provider called %d times, want %d", got, deadLinkStrikes)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %s to fail", time.Since(start))
	}
	if len(marked) != 1 || marked[0] != "DATABASE_ERROR" {
		t.Fatalf("onDead calls %v, want one DATABASE_ERROR", marked)
	}

	// A new session (the next FUSE read, a cache warm) fails at once.
	s2 := newSession(context.Background(), deadTransport(url, dead, func(string) { t.Fatal("marked twice") }), 4096, 0)
	defer s2.Close()
	if _, err := s2.Read(make([]byte, 512)); !customerror.IsPermanentError(err) {
		t.Fatalf("second read error %v, want permanent", err)
	}
	if got := tb.calls.Load(); got != deadLinkStrikes {
		t.Fatalf("second read called the provider (%d calls)", got)
	}
}

func TestDeadItemBlipRecovers(t *testing.T) {
	fastDeadBackoff(t)
	data := testPattern(4096)
	tb := &fakeTorBox{data: data, errBody: databaseError}
	tb.failFirst.Store(deadLinkStrikes - 1)
	url := startTorBox(t, tb)
	dead := newDeadLinks()
	tr := deadTransport(url, dead, func(string) { t.Fatal("a blip marked the file dead") })

	s := newSession(context.Background(), tr, int64(len(data)), 0)
	defer s.Close()
	buf := make([]byte, 512)
	n, err := s.Read(buf)
	if err != nil || n == 0 {
		t.Fatalf("read after a blip: n=%d err=%v", n, err)
	}
	if _, ok := dead.items["abc/file"]; ok {
		t.Fatal("strikes kept after a good answer")
	}
}

func TestPlainServerErrorKeepsTransientRetry(t *testing.T) {
	// A 500 without a dead-item code keeps the old retryable handling: it is
	// never counted toward a dead mark.
	data := testPattern(4096)
	tb := &fakeTorBox{data: data, errBody: `{"success":false,"error":"UNKNOWN_ERROR"}`}
	tb.failFirst.Store(2)
	url := startTorBox(t, tb)
	dead := newDeadLinks()
	tr := deadTransport(url, dead, func(string) { t.Fatal("marked dead") })
	s := newSession(context.Background(), tr, int64(len(data)), 0)
	defer s.Close()
	if n, err := s.Read(make([]byte, 512)); err != nil || n == 0 {
		t.Fatalf("read: n=%d err=%v", n, err)
	}
	if len(dead.items) != 0 {
		t.Fatal("plain server error counted as a dead strike")
	}
}

func TestDeadLinksWindowAndTTL(t *testing.T) {
	now := time.Date(2026, 10, 17, 3, 0, 0, 0, time.UTC)
	d := newDeadLinks()
	d.now = func() time.Time { return now }

	d.strike("k", "DATABASE_ERROR")
	d.strike("k", "DATABASE_ERROR")
	now = now.Add(deadLinkWindow + time.Second) // old strikes expire
	if d.strike("k", "DATABASE_ERROR") {
		t.Fatal("strikes outside the window marked the file dead")
	}
	d.strike("k", "DATABASE_ERROR")
	if !d.strike("k", "DATABASE_ERROR") {
		t.Fatal("three strikes in the window did not mark the file dead")
	}
	if d.strike("k", "DATABASE_ERROR") {
		t.Fatal("an already dead file was reported again")
	}
	if code, ok := d.dead("k"); !ok || code != "DATABASE_ERROR" {
		t.Fatalf("dead() = %q %v", code, ok)
	}
	now = now.Add(deadLinkTTL + time.Second)
	if _, ok := d.dead("k"); ok {
		t.Fatal("dead mark outlived its TTL")
	}
}

func TestProviderErrorCode(t *testing.T) {
	cases := map[string]string{
		databaseError:                      "DATABASE_ERROR",
		`{"success":true,"error":"X_Y_Z"}`: "",
		`{"error":"not a code"}`:           "",
		`<html>bad gateway</html>`:         "",
		``:                                 "",
		`{"success":false,"error":"ITEM_NOT_FOUND"}`: "ITEM_NOT_FOUND",
	}
	for body, want := range cases {
		if got := providerErrorCode([]byte(body)); got != want {
			t.Errorf("providerErrorCode(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestDeadItemsListed(t *testing.T) {
	m := &Manager{deadItems: newRefusalLog(refusalTTL, refusalMax)}
	m.deadItems.record(Refusal{Name: "Show.S03", InfoHash: "ABC", Category: "sonarr", Reason: "download link dead",
		File: "Show.S03E01.mkv", Provider: "torbox", Error: "DATABASE_ERROR"})
	got := m.DeadItems(time.Time{})
	if len(got) != 1 || got[0].InfoHash != "abc" || got[0].Error != "DATABASE_ERROR" || got[0].File != "Show.S03E01.mkv" {
		t.Fatalf("DeadItems = %+v", got)
	}
}
