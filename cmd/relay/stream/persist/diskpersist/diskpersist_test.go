package diskpersist

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/cmd/relay/stream"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/bluesky-social/indigo/util"
	"github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testCid() cid.Cid {
	buf := make([]byte, 32)
	c, err := cid.NewPrefixV1(cid.Raw, mh.SHA2_256).Sum(buf)
	if err != nil {
		panic(err)
	}
	return c
}

func TestInitialSequenceNumber(t *testing.T) {
	require := require.New(t)

	dir := t.TempDir()

	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "relay.sqlite")))
	require.NoError(err)

	// Open the persistence, the current sequence should be 1 (the default):
	persist, err := NewDiskPersistence(dir, "", db, DefaultDiskPersistOptions())
	require.NoError(err)
	require.Equal(int64(1), persist.curSeq)

	// Shutdown the persister:
	err = persist.Shutdown(t.Context())
	require.NoError(err)

	// Reopen the persistence, the current sequence should still be 1:
	persist, err = NewDiskPersistence(dir, "", db, DefaultDiskPersistOptions())
	require.NoError(err)
	require.Equal(int64(1), persist.curSeq)

	// Fake the DID to UID mapping:
	did := "did:example:123"
	persist.didCache.Add(did, 123)

	// Insert a dummy event:
	event := &stream.XRPCStreamEvent{
		RepoCommit: &atproto.SyncSubscribeRepos_Commit{
			Repo:   did,
			Commit: lexutil.LexLink(testCid()),
			Time:   time.Now().Format(util.ISO8601),
		},
	}
	err = persist.Persist(t.Context(), event)
	require.NoError(err)

	// Sequence number should now be 2:
	require.Equal(int64(2), persist.curSeq)

	// Shutdown the persister:
	err = persist.Shutdown(t.Context())
	require.NoError(err)

	// Reopen the persistence, the current sequence should still be 2:
	persist, err = NewDiskPersistence(dir, "", db, DefaultDiskPersistOptions())
	require.NoError(err)
	require.Equal(int64(2), persist.curSeq)
}

// persistCommits opens a persister in dir, writes n commit events and shuts it down.
func persistCommits(t *testing.T, dir string, db *gorm.DB, n int) {
	t.Helper()

	persist, err := NewDiskPersistence(dir, "", db, DefaultDiskPersistOptions())
	require.NoError(t, err)

	did := "did:example:123"
	persist.didCache.Add(did, 123)

	for range n {
		err := persist.Persist(t.Context(), &stream.XRPCStreamEvent{
			RepoCommit: &atproto.SyncSubscribeRepos_Commit{
				Repo:   did,
				Commit: lexutil.LexLink(testCid()),
				Time:   time.Now().Format(util.ISO8601),
			},
		})
		require.NoError(t, err)
	}

	require.NoError(t, persist.Shutdown(t.Context()))
}

// recordOffsets returns the start offset of each record in a log file, plus the end offset of the last one.
func recordOffsets(t *testing.T, path string) []int64 {
	t.Helper()

	b, err := os.ReadFile(path)
	require.NoError(t, err)

	r := bytes.NewReader(b)
	scratch := make([]byte, headerSize)
	offsets := []int64{0}
	var off int64
	for off < int64(len(b)) {
		h, err := readHeader(r, scratch)
		require.NoError(t, err)
		off += headerSize + h.Len64()
		_, err = r.Seek(off, io.SeekStart)
		require.NoError(t, err)
		offsets = append(offsets, off)
	}
	require.Equal(t, int64(len(b)), off)
	return offsets
}

func playbackSeqs(t *testing.T, persist *DiskPersistence) []int64 {
	t.Helper()

	var seqs []int64
	err := persist.Playback(t.Context(), 0, func(evt *stream.XRPCStreamEvent) error {
		seqs = append(seqs, evt.Sequence())
		return nil
	})
	require.NoError(t, err)
	return seqs
}

func TestResumeTruncatedRecord(t *testing.T) {
	for _, tc := range []struct {
		name string
		// how many bytes of the third record survive the crash
		keep func(recordLen int64) int64
	}{
		{name: "partial body", keep: func(l int64) int64 { return l - 5 }},
		{name: "partial header", keep: func(l int64) int64 { return headerSize - 3 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require := require.New(t)

			dir := t.TempDir()
			db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "relay.sqlite")))
			require.NoError(err)

			persistCommits(t, dir, db, 3)

			// Simulate the process dying partway through writing the third record.
			logPath := filepath.Join(dir, "evts-0")
			offs := recordOffsets(t, logPath)
			require.Len(offs, 4)
			require.NoError(os.Truncate(logPath, offs[2]+tc.keep(offs[3]-offs[2])))

			// On resume the incomplete record is cut off and its seq reused.
			persist, err := NewDiskPersistence(dir, "", db, DefaultDiskPersistOptions())
			require.NoError(err)
			require.Equal(int64(3), persist.curSeq)

			st, err := os.Stat(logPath)
			require.NoError(err)
			require.Equal(offs[2], st.Size())
			require.NoError(persist.Shutdown(t.Context()))

			// New writes continue right after the last complete record, and
			// playback sees every event.
			persistCommits(t, dir, db, 2)
			require.Len(recordOffsets(t, logPath), 5)

			persist, err = NewDiskPersistence(dir, "", db, DefaultDiskPersistOptions())
			require.NoError(err)
			require.Equal([]int64{1, 2, 3, 4}, playbackSeqs(t, persist))
			require.NoError(persist.Shutdown(t.Context()))
		})
	}
}

func TestPlaybackSkipsUndecodableRecord(t *testing.T) {
	require := require.New(t)

	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "relay.sqlite")))
	require.NoError(err)

	persistCommits(t, dir, db, 3)

	// Zero the body of the second record, leaving its header intact. This is
	// what a hole in the file looks like.
	logPath := filepath.Join(dir, "evts-0")
	offs := recordOffsets(t, logPath)
	fi, err := os.OpenFile(logPath, os.O_RDWR, 0)
	require.NoError(err)
	_, err = fi.WriteAt(make([]byte, offs[2]-offs[1]-headerSize), offs[1]+headerSize)
	require.NoError(err)
	require.NoError(fi.Close())

	persist, err := NewDiskPersistence(dir, "", db, DefaultDiskPersistOptions())
	require.NoError(err)
	require.Equal(int64(4), persist.curSeq)
	require.Equal([]int64{1, 3}, playbackSeqs(t, persist))

	// a cursor pointing before the bad record still gets past it
	var seqs []int64
	err = persist.Playback(t.Context(), 1, func(evt *stream.XRPCStreamEvent) error {
		seqs = append(seqs, evt.Sequence())
		return nil
	})
	require.NoError(err)
	require.Equal([]int64{3}, seqs)
	require.NoError(persist.Shutdown(t.Context()))
}
