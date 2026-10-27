package search

import (
	"encoding/json"
	"io"
	"os"
	"testing"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	appbsky "github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"

	"github.com/stretchr/testify/assert"
)

func TestParseEmojis(t *testing.T) {
	assert := assert.New(t)

	assert.Equal(parseEmojis("bunch 🎅 of 🏡 emoji 🤰and 🫄 some 👩‍👩‍👧‍👧 compound"), []string{"🎅", "🏡", "🤰", "🫄", "👩‍👩‍👧‍👧"})

	assert.Equal(parseEmojis("more ⛄ from ☠ lower ⛴ range"), []string{"⛄", "☠", "⛴"})
	assert.True(parseEmojis("blah") == nil)
}

type profileFixture struct {
	DID           string `json:"did"`
	Handle        string `json:"handle"`
	Rkey          string `json:"rkey"`
	Cid           string `json:"cid"`
	DocId         string `json:"doc_id"`
	ProfileRecord *appbsky.ActorProfile
	ProfileDoc    ProfileDoc
}

func TestTransformProfileFixtures(t *testing.T) {
	f, err := os.Open("testdata/transform-profile-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	fixBytes, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}

	var fixtures []profileFixture
	if err := json.Unmarshal(fixBytes, &fixtures); err != nil {
		t.Fatal(err)
	}

	for _, row := range fixtures {
		_ = row
		testProfileFixture(t, row)
	}
}

func testProfileFixture(t *testing.T, row profileFixture) {
	assert := assert.New(t)

	repo := identity.Identity{
		Handle: syntax.Handle(row.Handle),
		DID:    syntax.DID(row.DID),
	}
	doc := TransformProfile(row.ProfileRecord, &repo, row.Cid)
	doc.DocIndexTs = "2006-01-02T15:04:05.000Z"
	assert.Equal(row.ProfileDoc, doc)
	assert.Equal(row.DocId, doc.DocId())
}

type postFixture struct {
	DID        string `json:"did"`
	Handle     string `json:"handle"`
	Rkey       string `json:"rkey"`
	Cid        string `json:"cid"`
	DocId      string `json:"doc_id"`
	PostRecord *appbsky.FeedPost
	PostDoc    PostDoc
}

func TestTransformPostFixtures(t *testing.T) {
	f, err := os.Open("testdata/transform-post-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	fixBytes, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}

	var fixtures []postFixture
	if err := json.Unmarshal(fixBytes, &fixtures); err != nil {
		t.Fatal(err)
	}

	for _, row := range fixtures {
		_ = row
		testPostFixture(t, row)
	}
}

func testPostFixture(t *testing.T, row postFixture) {
	assert := assert.New(t)

	repo := identity.Identity{
		Handle: syntax.Handle(row.Handle),
		DID:    syntax.DID(row.DID),
	}
	doc := TransformPost(row.PostRecord, repo.DID, row.Rkey, row.Cid)
	doc.DocIndexTs = "2006-01-02T15:04:05.000Z"
	assert.Equal(row.PostDoc, doc)
	assert.Equal(row.DocId, doc.DocId())
}

// TestTransformPostMalformedRecords covers records that decoded with an outer
// struct present but an inner pointer nil. These reach the indexer from the
// firehose, where records are not strictly validated, and each of the cases
// below dereferenced a nil pointer before the guards in TransformPost were
// added. The nil-root case is the one observed panicking in production:
//
//	panic: runtime error: invalid memory address or nil pointer dereference
//	  search.TransformPost  search/transform.go:136
//	  search.(*Indexer).indexPosts  search/indexing.go:393
func TestTransformPostMalformedRecords(t *testing.T) {
	did := syntax.DID("did:plc:abc123abc123abc123abc123")
	rkey := "3l4yzevkwz4x2"
	cid := "bafyreib2rxk3rh6kzwq6d7dqjqfxvnnkhkfhopvhgxhqfxtnyfxbhjqkeq"

	for _, tc := range []struct {
		name string
		post *appbsky.FeedPost
	}{
		{
			// The production panic: reply present, root absent.
			name: "reply with nil root",
			post: &appbsky.FeedPost{
				Text:  "reply with no root",
				Reply: &appbsky.FeedPost_ReplyRef{},
			},
		},
		{
			name: "reply with nil root but non-nil parent",
			post: &appbsky.FeedPost{
				Text: "reply with parent only",
				Reply: &appbsky.FeedPost_ReplyRef{
					Parent: &comatproto.RepoStrongRef{Uri: "at://did:plc:x/app.bsky.feed.post/y", Cid: cid},
				},
			},
		},
		{
			// Matches the observed decode failure:
			//   cbor decode: unmarshaling t.Embed pointer:
			//   unmarshaling t.External pointer: got tag 7 while reading string value
			name: "embed external with nil external",
			post: &appbsky.FeedPost{
				Text:  "external embed that failed to decode",
				Embed: &appbsky.FeedPost_Embed{EmbedExternal: &appbsky.EmbedExternal{}},
			},
		},
		{
			name: "embed record with nil record",
			post: &appbsky.FeedPost{
				Text:  "quote post with no subject",
				Embed: &appbsky.FeedPost_Embed{EmbedRecord: &appbsky.EmbedRecord{}},
			},
		},
		{
			name: "embed record with media, nil record",
			post: &appbsky.FeedPost{
				Text:  "quote with media, no subject",
				Embed: &appbsky.FeedPost_Embed{EmbedRecordWithMedia: &appbsky.EmbedRecordWithMedia{}},
			},
		},
		{
			name: "embed record with media, nil inner record",
			post: &appbsky.FeedPost{
				Text: "quote with media, empty subject",
				Embed: &appbsky.FeedPost_Embed{
					EmbedRecordWithMedia: &appbsky.EmbedRecordWithMedia{
						Record: &appbsky.EmbedRecord{},
					},
				},
			},
		},
		{
			name: "empty embed union",
			post: &appbsky.FeedPost{
				Text:  "embed present but every variant nil",
				Embed: &appbsky.FeedPost_Embed{},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			// Must not panic, and must still produce a usable document.
			assert.NotPanics(func() {
				doc := TransformPost(tc.post, did, rkey, cid)
				assert.Equal(did.String(), doc.DID)
				assert.Equal(rkey, doc.RecordRkey)
			})

			// The wrapper reports success for these now-handled records.
			doc, err := SafeTransformPost(tc.post, did, rkey, cid)
			assert.NoError(err)
			assert.Equal(did.String(), doc.DID)
		})
	}
}

// TestSafeTransformPostRecovers checks the defence-in-depth wrapper: a panic
// from any future unguarded field is converted into an error so the indexer can
// skip one record instead of the process dying and dropping the whole batch.
func TestSafeTransformPostRecovers(t *testing.T) {
	assert := assert.New(t)

	doc, err := SafeTransformPost(nil, syntax.DID("did:plc:abc123abc123abc123abc123"), "rkey", "cid")
	assert.Error(err)
	assert.Equal(PostDoc{}, doc)
}
