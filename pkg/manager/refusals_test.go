package manager

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sirrobot01/decypharr/internal/customerror"
)

func TestIsNotCachedRefusal(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{fmt.Errorf("torrent x: %w", customerror.TorrentNotCachedError), true},
		{errors.Join(fmt.Errorf("torbox: abc recently proven not cached, skipping submit")), true},
		{fmt.Errorf("torrent abc is not cached on torbox"), true},
		{fmt.Errorf("torbox add limit reached"), false},
		{customerror.TorrentBlockedError, false},
	}
	for _, c := range cases {
		if got := isNotCachedRefusal(c.err); got != c.want {
			t.Errorf("isNotCachedRefusal(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestRefusalLogRecordsRepeatsAndExpires(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	l := newRefusalLog(time.Hour, 10)
	l.now = func() time.Time { return now }

	l.record(Refusal{Name: "Show.S02.1080p", InfoHash: "ABC", Category: "sonarr"})
	now = now.Add(time.Minute)
	l.record(Refusal{Name: "Show.S02.1080p", InfoHash: "abc", Category: "sonarr"})
	l.record(Refusal{Name: "Film.2010", InfoHash: "def", Category: "radarr"})

	got := l.list(time.Time{})
	if len(got) != 2 {
		t.Fatalf("want 2 refusals, got %d", len(got))
	}
	var show Refusal
	for _, r := range got {
		if r.InfoHash == "abc" {
			show = r
		}
	}
	if show.Count != 2 || !show.Time.Equal(now) {
		t.Fatalf("repeat not merged: %+v", show)
	}
	if n := len(l.list(now)); n != 0 {
		t.Fatalf("since filter: want 0, got %d", n)
	}

	now = now.Add(2 * time.Hour)
	if n := len(l.list(time.Time{})); n != 0 {
		t.Fatalf("expired refusals kept: %d", n)
	}
}

func TestRefusalLogBounded(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	l := newRefusalLog(time.Hour, 2)
	l.now = func() time.Time { return now }
	for i, h := range []string{"a", "b", "c"} {
		now = now.Add(time.Duration(i+1) * time.Second)
		l.record(Refusal{InfoHash: h, Category: "sonarr"})
	}
	got := l.list(time.Time{})
	if len(got) != 2 || got[0].InfoHash != "c" || got[1].InfoHash != "b" {
		t.Fatalf("want newest two [c b], got %+v", got)
	}
}

func TestRefusalLogIgnoresEmptyHash(t *testing.T) {
	l := newRefusalLog(time.Hour, 2)
	l.record(Refusal{Name: "x"})
	if n := len(l.list(time.Time{})); n != 0 {
		t.Fatalf("want 0, got %d", n)
	}
}
