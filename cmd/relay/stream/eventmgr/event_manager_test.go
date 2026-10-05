package eventmgr

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/cmd/relay/stream"
	"github.com/bluesky-social/indigo/cmd/relay/stream/persist/diskpersist"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/bluesky-social/indigo/util"
	"github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fixedUidSource struct{}

func (fixedUidSource) DidToUid(ctx context.Context, did string) (uint64, error) {
	return 123, nil
}

// A subscriber that resumes from a cursor and reads no faster than the relay
// writes (rainbow at peak) must still receive every event: playback has to
// follow the log to its end before crossing over to the live stream.
func TestSubscribeWithCursorSlowConsumerGetsEveryEvent(t *testing.T) {
	const eventsPerFile = 10

	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "relay.sqlite")))
	require.NoError(t, err)

	opts := diskpersist.DefaultDiskPersistOptions()
	opts.EventsPerFile = eventsPerFile
	dp, err := diskpersist.NewDiskPersistence(dir, "", db, opts)
	require.NoError(t, err)
	dp.SetUidSource(fixedUidSource{})

	em := NewEventManager(dp)
	// hand events over one at a time so the reader paces the playback
	em.crossoverBufferSize = 0

	c, err := cid.NewPrefixV1(cid.Raw, mh.SHA2_256).Sum(make([]byte, 32))
	require.NoError(t, err)
	written := 0
	write := func(n int) {
		for range n {
			require.NoError(t, em.AddEvent(t.Context(), &stream.XRPCStreamEvent{
				RepoCommit: &atproto.SyncSubscribeRepos_Commit{
					Repo:   "did:example:123",
					Commit: lexutil.LexLink(c),
					Time:   time.Now().Format(util.ISO8601),
				},
			}))
		}
		written += n
	}

	// playback phase: the writer stays ahead of the reader up to this many
	// events, then stops so the reader can reach the end of the log
	const backlog = 600
	// live phase: a few more events once the reader has stalled
	const live = 5

	write(3 * eventsPerFile)

	since := int64(12)
	evts, cleanup, err := em.Subscribe(t.Context(), "test", nil, &since)
	require.NoError(t, err)
	defer cleanup()

	next := since + 1
	wroteLive := false
	for !wroteLive || next <= int64(written) {
		select {
		case evt, ok := <-evts:
			require.True(t, ok, "stream closed at seq %d", next)
			require.Equal(t, next, evt.Sequence(), "gap in the stream")
			next++
			// just before the reader finishes a file, the writer finishes another
			if evt.Sequence()%eventsPerFile == eventsPerFile-2 && written < backlog {
				write(eventsPerFile)
			}
		case <-time.After(time.Second):
			// the reader has everything the stream will give it for now:
			// write the live events
			require.False(t, wroteLive, "stream stalled at seq %d", next)
			write(live)
			wroteLive = true
		}
	}
	require.Equal(t, backlog+live, written)
}
