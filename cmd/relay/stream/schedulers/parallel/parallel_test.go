package parallel

import (
	"context"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/cmd/relay/stream"
	"github.com/stretchr/testify/require"
)

func commitEvt(seq int64) *stream.XRPCStreamEvent {
	return &stream.XRPCStreamEvent{RepoCommit: &atproto.SyncSubscribeRepos_Commit{Repo: "did:example:1", Seq: seq}}
}

func TestAddWorkBlocksWhenQueueFull(t *testing.T) {
	require := require.New(t)

	release := make(chan struct{})
	done := make(chan int64, 10)
	sched := NewScheduler(1, 3, "test", func(ctx context.Context, evt *stream.XRPCStreamEvent) error {
		<-release
		done <- evt.Sequence()
		return nil
	})

	// One event in flight and two queued behind it for the same repo: this
	// used to grow without bound as long as all events were for active repos.
	for seq := int64(1); seq <= 3; seq++ {
		require.NoError(sched.AddWork(t.Context(), "did:example:1", commitEvt(seq)))
	}

	// the queue is full, so AddWork waits (and gives up when its context ends)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	require.ErrorIs(sched.AddWork(ctx, "did:example:1", commitEvt(4)), context.DeadlineExceeded)

	added := make(chan error, 1)
	go func() { added <- sched.AddWork(t.Context(), "did:example:1", commitEvt(4)) }()
	select {
	case <-added:
		t.Fatal("AddWork returned while the queue was full")
	case <-time.After(50 * time.Millisecond):
	}

	// finishing one item makes room
	release <- struct{}{}
	require.Equal(int64(1), <-done)
	select {
	case err := <-added:
		require.NoError(err)
	case <-time.After(time.Second):
		t.Fatal("AddWork still blocked after an item finished")
	}

	close(release)
	for want := int64(2); want <= 4; want++ {
		require.Equal(want, <-done)
	}
	sched.Shutdown()
	require.Equal(int64(4), sched.LastSeq())
}

func TestAddWorkUnlimitedQueue(t *testing.T) {
	require := require.New(t)

	release := make(chan struct{})
	sched := NewScheduler(1, 0, "test-unlimited", func(ctx context.Context, evt *stream.XRPCStreamEvent) error {
		<-release
		return nil
	})

	for seq := int64(1); seq <= 100; seq++ {
		require.NoError(sched.AddWork(t.Context(), "did:example:1", commitEvt(seq)))
	}

	close(release)
	sched.Shutdown()
}
