package manager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"
	"github.com/sirrobot01/decypharr/internal/config"
	"github.com/sirrobot01/decypharr/pkg/storage"
)

// newEndlessDownloadServer streams a large body slowly until the client goes
// away. started is closed when the first GET arrives, stopped when the server
// sees that request's context end.
func newEndlessDownloadServer(t *testing.T) (*httptest.Server, <-chan struct{}, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	stopped := make(chan struct{})
	var startedOnce, stoppedOnce sync.Once
	chunk := make([]byte, 32<<10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(1<<30))
		if r.Method == http.MethodHead {
			return
		}
		startedOnce.Do(func() { close(started) })
		flusher, _ := w.(http.Flusher)
		for {
			select {
			case <-r.Context().Done():
				stoppedOnce.Do(func() { close(stopped) })
				return
			case <-time.After(5 * time.Millisecond):
			}
			if _, err := w.Write(chunk); err != nil {
				continue
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, started, stopped
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// Deletion stops HTTP writes and removes partial files even when the caller
// keeps the download folder. Final progress writes must not revive the entry.
func TestQueueDeleteCancelsInflightDownload(t *testing.T) {
	for _, deleteFiles := range []bool{false, true} {
		t.Run(fmt.Sprintf("deleteFiles=%t", deleteFiles), func(t *testing.T) {
			config.Reset()
			config.SetConfigPath(t.TempDir())
			t.Cleanup(config.Reset)
			queue, entry, downloadedPath := newQueueDeleteTest(t)
			server, started, stopped := newEndlessDownloadServer(t)
			d := &Downloader{
				manager: &Manager{ctx: t.Context(), streamClient: server.Client(), queue: queue},
				logger:  zerolog.Nop(),
			}

			ctx, release := queue.track(t.Context(), entry.InfoHash)
			t.Cleanup(release)
			destPath := filepath.Join(downloadedPath, "partial.mkv")
			progressed := make(chan struct{}, 1)
			result := make(chan error, 1)
			go func() {
				defer release()
				result <- d.downloadFile(ctx, server.URL, destPath, nil, func(delta, speed int64) {
					entry.SizeDownloaded += delta
					entry.Speed = speed
					_ = queue.Update(entry)
					select {
					case progressed <- struct{}{}:
					default:
					}
				})
			}()
			waitFor(t, started, "download to start")
			waitFor(t, progressed, "first progress update")
			if info, err := os.Stat(destPath); err != nil || info.Size() == 0 {
				t.Fatalf("partial download was not written: %v", err)
			}

			if err := queue.Delete(entry.InfoHash, deleteFiles, nil); err != nil {
				t.Fatalf("delete queued entry: %v", err)
			}
			select {
			case err := <-result:
				if !errors.Is(err, errEntryDeleted) {
					t.Fatalf("download error = %v, want %v", err, errEntryDeleted)
				}
			default:
				t.Fatal("Delete returned while the download was still running")
			}
			waitFor(t, stopped, "server to see the request cancelled")
			if _, err := os.Stat(destPath); !os.IsNotExist(err) {
				t.Fatalf("partial file still present after delete: %v", err)
			}
			_, err := os.Stat(downloadedPath)
			if deleteFiles && !os.IsNotExist(err) {
				t.Fatalf("download folder still present after delete: %v", err)
			}
			if !deleteFiles && err != nil {
				t.Fatalf("download folder was not retained: %v", err)
			}
			if _, err := queue.GetTorrent(entry.InfoHash); err == nil {
				t.Fatal("deleted entry was written back to the queue")
			}
		})
	}
}

// A worker cancelled by deletion must not save its final error or progress.
func TestQueueUpdateRefusedForDeletedInflightEntry(t *testing.T) {
	queue, entry, _ := newQueueDeleteTest(t)
	ctx, release := queue.track(t.Context(), strings.ToUpper(entry.InfoHash))
	t.Cleanup(release)
	lateWrite := make(chan error, 1)
	go func() {
		defer release()
		<-ctx.Done()
		entry.MarkAsError(context.Cause(ctx))
		lateWrite <- queue.Update(entry)
	}()
	if err := queue.Delete(entry.InfoHash, false, nil); err != nil {
		t.Fatalf("delete queued entry: %v", err)
	}
	if err := <-lateWrite; !errors.Is(err, errEntryDeleted) {
		t.Fatalf("late update error = %v, want %v", err, errEntryDeleted)
	}
	if _, err := queue.GetTorrent(entry.InfoHash); err == nil {
		t.Fatal("deleted entry was written back to the queue")
	}
	if err := queue.Add(entry); err != nil {
		t.Fatalf("re-add entry: %v", err)
	}
	if err := queue.Update(entry); err != nil {
		t.Fatalf("update re-added entry: %v", err)
	}
}

// Shutdown must still allow interrupted work to save its state for restart.
func TestQueueUpdateAllowedAfterShutdownCancel(t *testing.T) {
	queue, entry, _ := newQueueDeleteTest(t)
	parent, shutdown := context.WithCancel(t.Context())
	ctx, release := queue.track(parent, entry.InfoHash)
	defer release()
	shutdown()
	<-ctx.Done()
	if isEntryDeleted(ctx) {
		t.Fatal("shutdown cancellation reported as entry deletion")
	}
	entry.IsDownloading = false
	if err := queue.Update(entry); err != nil {
		t.Fatalf("update after shutdown: %v", err)
	}
}

func TestQueueDeletesWaitForInflightRelease(t *testing.T) {
	for _, method := range []string{"Delete", "DeleteWhere", "DeleteStalled"} {
		t.Run(method, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				queue, entry, downloadedPath := newQueueDeleteTest(t)
				entry.AddedOn = time.Now().Add(-time.Hour)
				if err := queue.Update(entry); err != nil {
					t.Fatal(err)
				}
				ctx, release := queue.track(t.Context(), entry.InfoHash)
				defer release()
				result := make(chan error, 1)
				cleaned := false
				cleanup := func(*storage.Entry) error {
					cleaned = true
					return nil
				}
				go func() {
					switch method {
					case "Delete":
						result <- queue.Delete(entry.InfoHash, true, cleanup)
					case "DeleteWhere":
						result <- queue.DeleteWhere("", config.ProtocolAll, "", []string{entry.InfoHash}, cleanup)
					case "DeleteStalled":
						result <- queue.DeleteStalled(func(string) bool { return false })
					}
				}()
				synctest.Wait()
				if !isEntryDeleted(ctx) {
					t.Fatalf("worker cancellation cause = %v", context.Cause(ctx))
				}
				select {
				case err := <-result:
					t.Fatalf("delete returned before worker release: %v", err)
				default:
				}
				if cleaned {
					t.Fatal("cleanup ran before worker release")
				}
				if _, err := os.Stat(downloadedPath); err != nil {
					t.Fatalf("files removed before worker release: %v", err)
				}
				if _, err := queue.GetTorrent(entry.InfoHash); err != nil {
					t.Fatalf("entry removed before worker release: %v", err)
				}
				release()
				if err := <-result; err != nil {
					t.Fatal(err)
				}
				if _, err := queue.GetTorrent(entry.InfoHash); err == nil {
					t.Fatal("entry was not removed after worker release")
				}
				_, err := os.Stat(downloadedPath)
				if method == "DeleteStalled" {
					if err != nil {
						t.Fatalf("stalled deletion did not retain files: %v", err)
					}
				} else if !os.IsNotExist(err) || !cleaned {
					t.Fatalf("delete cleanup incomplete: file error = %v, callback = %t", err, cleaned)
				}
			})
		})
	}
}

func TestQueueDeleteWaitIsBoundedAndLateUpdateRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue, entry, _ := newQueueDeleteTest(t)
		ctx, release := queue.track(t.Context(), entry.InfoHash)
		defer release()
		started := time.Now()
		if err := queue.Delete(entry.InfoHash, false, nil); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed != 15*time.Second {
			t.Fatalf("delete waited %s, want 15s", elapsed)
		}
		if !isEntryDeleted(ctx) {
			t.Fatalf("worker cancellation cause = %v", context.Cause(ctx))
		}
		if _, err := queue.GetTorrent(entry.InfoHash); err == nil {
			t.Fatal("timed out delete did not remove entry")
		}
		if err := queue.Update(entry); !errors.Is(err, errEntryDeleted) {
			t.Fatalf("late update error = %v, want %v", err, errEntryDeleted)
		}
		if _, err := queue.GetTorrent(entry.InfoHash); err == nil {
			t.Fatal("late update resurrected deleted entry")
		}
	})
}

// The entry's own worker must not cancel or wait on itself for action "none".
func TestQueueRemoveDoesNotCancelOwnWork(t *testing.T) {
	queue, entry, _ := newQueueDeleteTest(t)
	ctx, release := queue.track(t.Context(), entry.InfoHash)
	defer release()
	done := make(chan error, 1)
	go func() { done <- queue.remove(entry.InfoHash, false, nil) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("remove: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("remove waited on the entry's own work")
	}
	if ctx.Err() != nil {
		t.Fatal("remove cancelled the entry's own work")
	}
}
