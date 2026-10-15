package models

import (
	"sync"
	"testing"
)

// The feed is written by the walking and organizing goroutines while the HTTP
// handler reads it to answer a poll, so the ring must stay consistent under both
// at once. The race detector cannot run here (it needs cgo), so this exercises
// the interleaving directly and checks the invariants that matter: the sequence
// never goes backwards or repeats, the ring stays bounded, and a snapshot is
// always internally consistent.
func TestScanProgressConcurrentAccess(t *testing.T) {
	p := NewScanProgress(7)
	p.Set(ScanState{LibraryID: 7, Running: true, Phase: "walk"})

	const writers, perWriter = 4, 250
	var wg sync.WaitGroup

	// One goroutine reads snapshots the way the status endpoint does, until the
	// writers are done. It reports through a channel because t.Errorf is not safe
	// from a goroutine that may outlive the test body.
	bad := make(chan string, 1)
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			st, ev := p.Snapshot(10)
			if st.LibraryID != 7 {
				select {
				case bad <- "snapshot lost the library id":
				default:
				}
				return
			}
			// Events come oldest-first with strictly rising sequence numbers.
			for i := 1; i < len(ev); i++ {
				if ev[i].Seq <= ev[i-1].Seq {
					select {
					case bad <- "events out of order in a snapshot":
					default:
					}
					return
				}
			}
			if len(ev) > 10 {
				select {
				case bad <- "snapshot returned more events than asked for":
				default:
				}
				return
			}
		}
	}()

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				p.Update(func(st *ScanState) {
					st.Done++
					st.Current = "x"
				})
				p.AddEvent("dir", "d")
			}
		}()
	}

	close(stop)
	wg.Wait()
	select {
	case msg := <-bad:
		t.Fatal(msg)
	default:
	}

	st, ev := p.Snapshot(0)
	if st.Done != writers*perWriter {
		t.Errorf("Update lost writes: Done = %d, want %d", st.Done, writers*perWriter)
	}
	if len(ev) != scanEventRing {
		t.Errorf("ring holds %d events after %d adds, want the cap %d",
			len(ev), writers*perWriter, scanEventRing)
	}
	// The surviving window is the newest one, so the last seq is the total added.
	if want := int64(writers * perWriter); ev[len(ev)-1].Seq != want {
		t.Errorf("newest event seq = %d, want %d", ev[len(ev)-1].Seq, want)
	}
	// And the oldest surviving seq is exactly one ring behind the newest: the
	// window slid rather than resetting.
	if want := int64(writers*perWriter - scanEventRing + 1); ev[0].Seq != want {
		t.Errorf("oldest event seq = %d, want %d", ev[0].Seq, want)
	}
}

// A snapshot must be a copy: the caller keeps it while the scan writes on.
func TestScanProgressSnapshotIsACopy(t *testing.T) {
	p := NewScanProgress(1)
	p.AddEvent("dir", "first")
	_, ev := p.Snapshot(0)
	if len(ev) != 1 || ev[0].Text != "first" {
		t.Fatalf("unexpected snapshot: %+v", ev)
	}
	p.AddEvent("dir", "second")
	if len(ev) != 1 {
		t.Errorf("the earlier snapshot changed under the caller: %+v", ev)
	}
	if ev[0].Text != "first" {
		t.Errorf("the earlier snapshot's contents changed: %+v", ev)
	}
	// want == 0 means everything held.
	if _, all := p.Snapshot(0); len(all) != 2 {
		t.Errorf("Snapshot(0) returned %d events, want both", len(all))
	}
}

// An empty event is not worth a line in the feed; scans emit them for "no news"
// callbacks and they must not push real lines out of the ring.
func TestScanProgressIgnoresEmptyEvents(t *testing.T) {
	p := NewScanProgress(1)
	p.AddEvent("meta", "")
	if _, ev := p.Snapshot(0); len(ev) != 0 {
		t.Errorf("an empty event was recorded: %+v", ev)
	}
}
