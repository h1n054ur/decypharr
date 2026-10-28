package manager

import (
	"slices"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/sirrobot01/decypharr/internal/config"
	"github.com/sirrobot01/decypharr/pkg/debrid/types"
	"github.com/sirrobot01/decypharr/pkg/storage"
)

func refreshGraceEntry(hash string, addedAt time.Time, downloadedAt *time.Time, providers ...string) *storage.Entry {
	entry := &storage.Entry{
		InfoHash: hash, Name: "pack-" + hash[:4], Protocol: config.ProtocolTorrent,
		ActiveProvider: providers[0], IsComplete: true,
		Providers: map[string]*storage.ProviderEntry{},
		Files:     map[string]*storage.File{"video.mkv": {Name: "video.mkv", Size: 1, InfoHash: hash}},
	}
	for _, p := range providers {
		entry.Providers[p] = &storage.ProviderEntry{
			Provider: p, ID: "id-" + p, Status: types.TorrentStatusDownloaded,
			AddedAt: addedAt, DownloadedAt: downloadedAt,
			Files: map[string]*storage.ProviderFile{"video.mkv": {Id: "1"}},
		}
	}
	return entry
}

func TestRefreshKeepsFreshPlacementMissingFromRemote(t *testing.T) {
	config.Reset()
	config.SetConfigPath(t.TempDir())
	t.Cleanup(config.Reset)
	store, err := storage.NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := &Manager{storage: store, logger: zerolog.Nop()}

	now := time.Now()
	old := now.Add(-2 * time.Hour)
	recentDone := now.Add(-5 * time.Minute)
	const (
		fresh      = "1111111111111111111111111111111111111111"
		stale      = "2222222222222222222222222222222222222222"
		doneLately = "3333333333333333333333333333333333333333"
		listed     = "4444444444444444444444444444444444444444"
		shared     = "5555555555555555555555555555555555555555"
	)
	entries := []*storage.Entry{
		refreshGraceEntry(fresh, now.Add(-2*time.Minute), nil, "torbox"),
		refreshGraceEntry(stale, old, &old, "torbox"),
		refreshGraceEntry(doneLately, old, &recentDone, "torbox"),
		refreshGraceEntry(listed, old, &old, "torbox"),
		refreshGraceEntry(shared, old, &old, "torbox", "realdebrid"),
	}
	if err := store.BatchAddOrUpdate(entries); err != nil {
		t.Fatal(err)
	}
	remote := map[string]*types.Torrent{
		listed: {InfoHash: listed, Id: "id-torbox", Debrid: "torbox", Status: types.TorrentStatusDownloaded},
	}

	tests := []struct {
		name        string
		grace       time.Duration
		wantDeleted []string
		wantUpdated []string
	}{
		{"grace keeps fresh placements", 30 * time.Minute, []string{stale}, []string{shared}},
		{"grace off drops every missing placement", 0, []string{fresh, stale, doneLately}, []string{shared}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newTorrents, toUpdate, toDelete, err := m.detectTorrentChanges("torbox", remote, tt.grace)
			if err != nil {
				t.Fatal(err)
			}
			if len(newTorrents) != 0 {
				t.Fatalf("new torrents = %d, want 0", len(newTorrents))
			}
			slices.Sort(toDelete)
			if !slices.Equal(toDelete, tt.wantDeleted) {
				t.Fatalf("deleted = %v, want %v", toDelete, tt.wantDeleted)
			}
			var updated []string
			for _, e := range toUpdate {
				updated = append(updated, e.InfoHash)
			}
			if !slices.Equal(updated, tt.wantUpdated) {
				t.Fatalf("updated = %v, want %v", updated, tt.wantUpdated)
			}
		})
	}
}

func TestRefreshMissingGraceParsing(t *testing.T) {
	for value, want := range map[string]time.Duration{
		"":      30 * time.Minute,
		"30m":   30 * time.Minute,
		"1h":    time.Hour,
		"0":     0,
		"bogus": 30 * time.Minute,
		"-5m":   30 * time.Minute,
	} {
		if got := refreshMissingGrace(value); got != want {
			t.Errorf("refreshMissingGrace(%q) = %v, want %v", value, got, want)
		}
	}
}
