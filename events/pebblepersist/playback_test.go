package pebblepersist

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	atproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/events"
)

func testEvent(seq int64) *events.XRPCStreamEvent {
	return &events.XRPCStreamEvent{
		RepoIdentity: &atproto.SyncSubscribeRepos_Identity{
			Did: "did:example:123",
			Seq: seq,
		},
	}
}

func testPebble(t *testing.T, opts PebblePersistOptions) *PebblePersist {
	t.Helper()
	opts.DbPath = filepath.Join(t.TempDir(), "pebble.db")
	pp, err := NewPebblePersistance(&opts)
	if err != nil {
		t.Fatal(err)
	}
	pp.SetEventBroadcaster(func(*events.XRPCStreamEvent) {})
	t.Cleanup(func() { pp.Shutdown(context.Background()) })
	return pp
}

func TestRawPlayback(t *testing.T) {
	ctx := context.Background()
	pp := testPebble(t, PebblePersistOptions{NoSync: true, RawPlayback: true})

	var want [][]byte
	for seq := int64(1); seq <= 20; seq++ {
		evt := testEvent(seq)
		if err := pp.Persist(ctx, evt); err != nil {
			t.Fatal(err)
		}
		want = append(want, evt.Preserialized)
	}

	next := int64(1)
	err := pp.Playback(ctx, 1, func(evt *events.XRPCStreamEvent) error {
		if evt.RepoIdentity != nil {
			t.Fatalf("seq %d: event was decoded", next)
		}
		if got := evt.Sequence(); got != next {
			t.Fatalf("got seq %d, want %d", got, next)
		}
		if seq, ok := evt.GetSequence(); !ok || seq != next {
			t.Fatalf("GetSequence: got %d %v, want %d", seq, ok, next)
		}
		var buf bytes.Buffer
		if err := evt.Serialize(&buf); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), want[next-1]) {
			t.Fatalf("seq %d: serialized bytes differ from what was persisted", next)
		}
		next++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if next != 21 {
		t.Fatalf("played back up to seq %d, want 20", next-1)
	}
}

// Events written while a playback is running are part of it: it ends at the
// end of the log, not at what was there when it started.
func TestPlaybackFollowsLog(t *testing.T) {
	for _, raw := range []bool{false, true} {
		ctx := context.Background()
		pp := testPebble(t, PebblePersistOptions{RawPlayback: raw})

		head := int64(0)
		persist := func(n int) {
			for range n {
				head++
				if err := pp.Persist(ctx, testEvent(head)); err != nil {
					t.Fatal(err)
				}
			}
		}
		persist(10)

		var got []int64
		err := pp.Playback(ctx, 1, func(evt *events.XRPCStreamEvent) error {
			got = append(got, evt.Sequence())
			// the log grows twice behind the playback's back
			if len(got) == 5 || len(got) == 15 {
				persist(10)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 30 {
			t.Fatalf("raw=%v: played back %d events, want 30: %v", raw, len(got), got)
		}
		for i, seq := range got {
			if seq != int64(i+1) {
				t.Fatalf("raw=%v: event %d has seq %d: %v", raw, i, seq, got)
			}
		}
	}
}
