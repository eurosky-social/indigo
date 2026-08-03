package search

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// externallyManagedProfileFields are populated in palomar_profile by a process
// outside Palomar and are absent from ProfileDoc. They are the fields a
// full-document `index` write silently destroyed.
var externallyManagedProfileFields = []string{
	"followersFuzzy",
	"pagerank",
	"verified",
	"has_custom_domain",
}

// TestProfileBulkUpdatePreservesExternalFields is the regression test for the
// wipe: the bulk body must be an `update` with a partial doc, and must never
// mention the externally-managed ranking signals. With the previous
// `{"index":{...}}` action, every profile reindex from the firehose reset those
// fields to absent -- measured in production as 3 of 300,248 profiles written in
// a 7-day window still carrying followersFuzzy, against 41.8% index-wide.
func TestProfileBulkUpdatePreservesExternalFields(t *testing.T) {
	assert := assert.New(t)

	did := "did:plc:abc123abc123abc123abc123"
	name := "Blacksky"
	desc := "a description"
	doc := ProfileDoc{
		DocIndexTs:  "2026-08-03T00:00:00.000Z",
		DID:         did,
		RecordCID:   "bafyreib2rxk3rh6kzwq6d7dqjqfxvnnkhkfhopvhgxhqfxtnyfxbhjqkeq",
		Handle:      "blacksky.app",
		DisplayName: &name,
		Description: &desc,
		Tag:         []string{"tag"},
		HasAvatar:   true,
	}

	body, err := profileBulkUpdate(did, doc)
	assert.NoError(err)

	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	assert.Len(lines, 2, "bulk body must be exactly an action line and a doc line")

	var action map[string]map[string]string
	assert.NoError(json.Unmarshal([]byte(lines[0]), &action))
	assert.Contains(action, "update", "must be an update, not an index -- index replaces the whole document")
	assert.NotContains(action, "index")
	assert.Equal(did, action["update"]["_id"])

	var payload map[string]any
	assert.NoError(json.Unmarshal([]byte(lines[1]), &payload))
	assert.Equal(true, payload["doc_as_upsert"], "new profiles must still be created")
	inner, ok := payload["doc"].(map[string]any)
	assert.True(ok, "partial update must nest fields under \"doc\"")

	// The core property: Palomar must not assert anything about these fields.
	for _, f := range externallyManagedProfileFields {
		assert.NotContains(inner, f, "Palomar must not write externally-managed field %q", f)
		assert.NotContains(string(body), f)
	}

	// ...while still writing everything it does own.
	for _, f := range []string{
		"doc_index_ts", "did", "record_cid", "handle", "display_name",
		"description", "img_alt_text", "self_label", "url", "domain",
		"tag", "emoji", "has_avatar", "has_banner",
	} {
		assert.Contains(inner, f, "Palomar-owned field %q must be present", f)
	}
}

// TestProfileDocClearedFieldsPropagate guards the regression that switching to a
// partial update could introduce. In an `update`, an omitted key means "keep the
// existing value" -- so if ProfileDoc used `omitempty`, a user clearing their
// description or removing a tag would leave the old value searchable forever.
// Every owned field must therefore marshal explicitly, as null when empty.
func TestProfileDocClearedFieldsPropagate(t *testing.T) {
	assert := assert.New(t)

	// A profile whose display name, description, tags and emoji have all been removed.
	cleared := ProfileDoc{
		DocIndexTs: "2026-08-03T00:00:00.000Z",
		DID:        "did:plc:abc123abc123abc123abc123",
		RecordCID:  "bafyreib2rxk3rh6kzwq6d7dqjqfxvnnkhkfhopvhgxhqfxtnyfxbhjqkeq",
		Handle:     "blacksky.app",
	}

	body, err := profileBulkUpdate(cleared.DID, cleared)
	assert.NoError(err)

	var payload struct {
		Doc map[string]any `json:"doc"`
	}
	assert.NoError(json.Unmarshal([]byte(strings.Split(string(body), "\n")[1]), &payload))

	for _, f := range []string{
		"display_name", "description", "img_alt_text",
		"self_label", "url", "domain", "tag", "emoji",
	} {
		assert.Contains(payload.Doc, f, "cleared field %q must still be sent", f)
		assert.Nil(payload.Doc[f], "cleared field %q must be an explicit null so the merge removes it", f)
	}

	// Booleans must be sent even when false, for the same reason.
	assert.Equal(false, payload.Doc["has_avatar"])
	assert.Equal(false, payload.Doc["has_banner"])
}
