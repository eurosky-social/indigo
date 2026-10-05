package diskpersist

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/cmd/relay/stream"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/bluesky-social/indigo/util"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const (
	playbackTestDid           = "did:example:123"
	playbackTestEventsPerFile = 10
)

func openPlaybackTestPersist(t *testing.T, dir string, db *gorm.DB) *DiskPersistence {
	t.Helper()
	opts := DefaultDiskPersistOptions()
	opts.EventsPerFile = playbackTestEventsPerFile
	dp, err := NewDiskPersistence(dir, "", db, opts)
	require.NoError(t, err)
	dp.didCache.Add(playbackTestDid, 123)
	return dp
}

func newPlaybackTestPersist(t *testing.T) *DiskPersistence {
	t.Helper()
	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "relay.sqlite")))
	require.NoError(t, err)
	return openPlaybackTestPersist(t, dir, db)
}

func persistTestEvents(t *testing.T, dp *DiskPersistence, n int) {
	t.Helper()
	for range n {
		require.NoError(t, dp.Persist(t.Context(), &stream.XRPCStreamEvent{
			RepoCommit: &atproto.SyncSubscribeRepos_Commit{
				Repo:   playbackTestDid,
				Commit: lexutil.LexLink(testCid()),
				Time:   time.Now().Format(util.ISO8601),
			},
		}))
	}
}

// requirePlaybackComplete plays back from every possible cursor and requires
// each playback to return exactly the events after the cursor, in order.
func requirePlaybackComplete(t *testing.T, dp *DiskPersistence, lastSeq int64) {
	t.Helper()
	for since := int64(1); since < lastSeq; since++ {
		next := since + 1
		err := dp.Playback(t.Context(), since, func(evt *stream.XRPCStreamEvent) error {
			require.Equal(t, next, evt.Sequence(), "playback since %d out of sequence", since)
			next++
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, lastSeq, next-1, "playback since %d stopped early", since)
	}
}

// A cursor equal to the last seq of a full log file must continue into the
// next file rather than play back nothing.
func TestPlaybackFromFileBoundary(t *testing.T) {
	dp := newPlaybackTestPersist(t)

	// seqs 1..35: three full files and a partial one
	persistTestEvents(t, dp, 35)
	require.NoError(t, dp.Flush(t.Context()))

	requirePlaybackComplete(t, dp, 35)
}

// A restart resumes the last log file and writes another eventsPerFile events
// to it, so that file ends up longer than the others. Playback must not take
// its end for the end of the log.
func TestPlaybackAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "relay.sqlite")))
	require.NoError(t, err)

	dp := openPlaybackTestPersist(t, dir, db)
	persistTestEvents(t, dp, 15)
	require.NoError(t, dp.Shutdown(t.Context()))

	dp = openPlaybackTestPersist(t, dir, db)
	persistTestEvents(t, dp, 30)
	require.NoError(t, dp.Flush(t.Context()))

	requirePlaybackComplete(t, dp, 45)
}

// A consumer that reads no faster than the relay writes keeps finding new log
// files at the end of every pass. Playback must keep going until it reaches
// the end of the log, however many passes that takes.
func TestPlaybackKeepsUpWithWriter(t *testing.T) {
	dp := newPlaybackTestPersist(t)

	const total = 400
	written := 20
	persistTestEvents(t, dp, written)

	next := int64(13)
	err := dp.Playback(t.Context(), 12, func(evt *stream.XRPCStreamEvent) error {
		require.Equal(t, next, evt.Sequence(), "playback out of sequence")
		next++
		// each time the reader finishes a file, the writer finishes another
		if evt.Sequence()%playbackTestEventsPerFile == 0 && written < total {
			persistTestEvents(t, dp, playbackTestEventsPerFile)
			written += playbackTestEventsPerFile
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, int64(total), next-1, "playback stopped before the end of the log")
}
