package splitter

import (
	"context"
	"testing"
	"time"

	atproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/events"
)

func testSplitter(t *testing.T, conf SplitterConfig) *Splitter {
	t.Helper()
	s, err := NewSplitter(conf, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// playbackDuration subscribes a consumer at remoteAddr with a cursor, and
// returns how long it took to be sent the n stored events.
func playbackDuration(t *testing.T, s *Splitter, remoteAddr string, n int) time.Duration {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, _ = s.withPlaybackConsumer(ctx, remoteAddr)

	since := int64(0)
	start := time.Now()
	evts, cleanup, err := s.events.Subscribe(ctx, remoteAddr, nil, &since)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	for i := range n {
		select {
		case _, ok := <-evts:
			if !ok {
				t.Fatalf("stream closed after %d events, want %d", i, n)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("got %d events, want %d", i, n)
		}
	}
	return time.Since(start)
}

func TestPlaybackRateLimit(t *testing.T) {
	exempt, err := ParsePlaybackExempt([]string{"10.0.0.0/24", "2001:db8::1"})
	if err != nil {
		t.Fatal(err)
	}

	const n = 60
	for _, tc := range []struct {
		name       string
		conf       SplitterConfig
		remoteAddr string
		throttled  bool
	}{
		{"no limit", SplitterConfig{}, "192.0.2.1", false},
		{"per consumer", SplitterConfig{PlaybackRateLimit: 100}, "192.0.2.1", true},
		{"global", SplitterConfig{PlaybackGlobalRateLimit: 100}, "192.0.2.1", true},
		{"exempt range", SplitterConfig{PlaybackRateLimit: 100, PlaybackGlobalRateLimit: 100, PlaybackExempt: exempt}, "10.0.0.7", false},
		{"exempt address", SplitterConfig{PlaybackRateLimit: 100, PlaybackExempt: exempt}, "2001:db8::1", false},
		{"outside exempt range", SplitterConfig{PlaybackRateLimit: 100, PlaybackExempt: exempt}, "10.0.1.7", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testSplitter(t, tc.conf)
			for seq := int64(1); seq <= n; seq++ {
				evt := &events.XRPCStreamEvent{RepoCommit: &atproto.SyncSubscribeRepos_Commit{Repo: "did:example:123", Seq: seq}}
				if err := s.events.AddEvent(context.Background(), evt); err != nil {
					t.Fatal(err)
				}
			}

			// 60 events at 100/s with a burst of 10 take half a second
			took := playbackDuration(t, s, tc.remoteAddr, n)
			if tc.throttled && took < 400*time.Millisecond {
				t.Fatalf("playback took %s, want it paced to about 500ms", took)
			}
			if !tc.throttled && took > 200*time.Millisecond {
				t.Fatalf("playback took %s, want it unpaced", took)
			}
		})
	}
}

func TestPlaybackLimitStopsWithConsumer(t *testing.T) {
	s := testSplitter(t, SplitterConfig{PlaybackRateLimit: 1})
	for seq := int64(1); seq <= 10; seq++ {
		evt := &events.XRPCStreamEvent{RepoCommit: &atproto.SyncSubscribeRepos_Commit{Repo: "did:example:123", Seq: seq}}
		if err := s.events.AddEvent(context.Background(), evt); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	ctx, _ = s.withPlaybackConsumer(ctx, "192.0.2.1")
	since := int64(0)
	evts, cleanup, err := s.events.Subscribe(ctx, "192.0.2.1", nil, &since)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	time.AfterFunc(50*time.Millisecond, cancel)

	// at one event a second the playback is waiting on the limiter when the
	// consumer goes away, and has to give up instead of sitting out the wait
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-evts:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("playback kept waiting on the limiter after the consumer went away")
		}
	}
}

func TestParsePlaybackExempt(t *testing.T) {
	for _, bad := range []string{"nope", "10.0.0.0/33", "10.0.0.1/"} {
		if _, err := ParsePlaybackExempt([]string{bad}); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	got, err := ParsePlaybackExempt([]string{"", " 10.0.0.1 ", "::ffff:192.0.2.9"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].String() != "10.0.0.1/32" || got[1].String() != "192.0.2.9/32" {
		t.Errorf("got %v", got)
	}
}
