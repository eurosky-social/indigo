package relay

import (
	"context"
	"testing"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/cmd/relay/relay/models"
	"github.com/bluesky-social/indigo/cmd/relay/stream/eventmgr"
	"github.com/bluesky-social/indigo/cmd/relay/stream/persist/diskpersist"
	"github.com/bluesky-social/indigo/util/cliutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testThrottleRelay(t *testing.T, dir identity.Directory) *Relay {
	t.Helper()
	db, err := cliutil.SetupDatabase("sqlite://:memory:", 1)
	require.NoError(t, err)
	persister, err := diskpersist.NewDiskPersistence(t.TempDir(), "", db, diskpersist.DefaultDiskPersistOptions())
	require.NoError(t, err)
	r, err := NewRelay(db, eventmgr.NewEventManager(persister), dir, DefaultRelayConfig())
	require.NoError(t, err)
	persister.SetUidSource(r)
	return r
}

func testThrottleHost(t *testing.T, r *Relay, hostname string, count, limit int64) *models.Host {
	t.Helper()
	h := models.Host{Hostname: hostname, AccountCount: count, AccountLimit: limit, LastSeq: -1}
	require.NoError(t, r.db.Create(&h).Error)
	return &h
}

func testThrottleIdentity(did syntax.DID, pdsHostname string) identity.Identity {
	return identity.Identity{
		DID: did,
		Services: map[string]identity.ServiceEndpoint{
			"atproto_pds": {Type: "AtprotoPersonalDataServer", URL: "https://" + pdsHostname},
		},
	}
}

func testThrottledAccount(t *testing.T, r *Relay, did syntax.DID, hostID uint64, upstream models.AccountStatus) *models.Account {
	t.Helper()
	acc := models.Account{DID: did.String(), HostID: hostID, Status: models.AccountStatusHostThrottled, UpstreamStatus: upstream}
	require.NoError(t, r.db.Create(&acc).Error)
	return &acc
}

func testAccountRow(t *testing.T, r *Relay, uid uint64) models.Account {
	t.Helper()
	var acc models.Account
	require.NoError(t, r.db.First(&acc, uid).Error)
	return acc
}

// An account throttled on a capped host keeps no throttle once it moves to a host with room for it.
func TestEnsureAccountHostUnthrottlesOnMove(t *testing.T) {
	ctx := context.Background()
	did := syntax.DID("did:plc:movedintoroom")
	dir := identity.NewMockDirectory()
	dir.Insert(testThrottleIdentity(did, "roomy.example.com"))
	r := testThrottleRelay(t, dir)

	capped := testThrottleHost(t, r, "capped.example.com", 150, 100)
	roomy := testThrottleHost(t, r, "roomy.example.com", 10, 1000)
	acc := testThrottledAccount(t, r, did, capped.ID, models.AccountStatusActive)

	require.NoError(t, r.EnsureAccountHost(ctx, acc, roomy.ID, roomy.Hostname))

	assert.Equal(t, models.AccountStatusActive, acc.Status)
	row := testAccountRow(t, r, acc.UID)
	assert.Equal(t, roomy.ID, row.HostID)
	assert.Equal(t, models.AccountStatusActive, row.Status)
	assert.True(t, row.IsActive())
}

// Moving to a host that is itself over its limit keeps the account throttled.
func TestEnsureAccountHostKeepsThrottleOnFullHost(t *testing.T) {
	ctx := context.Background()
	did := syntax.DID("did:plc:movedintofull")
	dir := identity.NewMockDirectory()
	dir.Insert(testThrottleIdentity(did, "full.example.com"))
	r := testThrottleRelay(t, dir)

	capped := testThrottleHost(t, r, "capped.example.com", 150, 100)
	// at its limit before the move; over it once this account is counted
	full := testThrottleHost(t, r, "full.example.com", 5, 5)
	acc := testThrottledAccount(t, r, did, capped.ID, models.AccountStatusActive)

	require.NoError(t, r.EnsureAccountHost(ctx, acc, full.ID, full.Hostname))

	assert.Equal(t, models.AccountStatusHostThrottled, acc.Status)
	row := testAccountRow(t, r, acc.UID)
	assert.Equal(t, full.ID, row.HostID)
	assert.Equal(t, models.AccountStatusHostThrottled, row.Status)
}

// An account skipped by an account limit increase (inactive upstream at the time) is unthrottled when it becomes active upstream again.
func TestAccountEventUnthrottlesOnReactivation(t *testing.T) {
	ctx := context.Background()
	did := syntax.DID("did:plc:reactivated")
	dir := identity.NewMockDirectory()
	dir.Insert(testThrottleIdentity(did, "roomy.example.com"))
	r := testThrottleRelay(t, dir)

	roomy := testThrottleHost(t, r, "roomy.example.com", 10, 1000)
	acc := testThrottledAccount(t, r, did, roomy.ID, models.AccountStatusDeactivated)

	evt := &comatproto.SyncSubscribeRepos_Account{Did: did.String(), Active: true, Seq: 1, Time: syntax.DatetimeNow().String()}
	require.NoError(t, r.processAccountEvent(ctx, evt, roomy.Hostname, roomy.ID))

	row := testAccountRow(t, r, acc.UID)
	assert.Equal(t, models.AccountStatusActive, row.UpstreamStatus)
	assert.Equal(t, models.AccountStatusActive, row.Status)
	assert.True(t, row.IsActive())
}

// Other local statuses (eg, takedowns) are never touched.
func TestUnthrottleLeavesOtherStatusesAlone(t *testing.T) {
	ctx := context.Background()
	r := testThrottleRelay(t, identity.NewMockDirectory())

	roomy := testThrottleHost(t, r, "roomy.example.com", 10, 1000)
	acc := models.Account{DID: "did:plc:takendown", HostID: roomy.ID, Status: models.AccountStatusTakendown, UpstreamStatus: models.AccountStatusActive}
	require.NoError(t, r.db.Create(&acc).Error)

	require.NoError(t, r.UnthrottleAccountIfHostHasRoom(ctx, &acc, true))

	assert.Equal(t, models.AccountStatusTakendown, testAccountRow(t, r, acc.UID).Status)
}
