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

func testAccount(t *testing.T, r *Relay, did string, hostID uint64, status, upstream models.AccountStatus) *models.Account {
	t.Helper()
	acc := models.Account{DID: did, HostID: hostID, Status: status, UpstreamStatus: upstream}
	require.NoError(t, r.db.Create(&acc).Error)
	return &acc
}

// Re-applying the same limit resumes an earlier limit increase which was interrupted before promoting every account it had room for.
func TestUpdateHostAccountLimitResumesInterruptedRun(t *testing.T) {
	ctx := context.Background()
	r := testThrottleRelay(t, identity.NewMockDirectory())

	host := testThrottleHost(t, r, "resumed.example.com", 3, 3)
	testAccount(t, r, "did:plc:resumed1", host.ID, models.AccountStatusActive, models.AccountStatusActive)
	a2 := testAccount(t, r, "did:plc:resumed2", host.ID, models.AccountStatusHostThrottled, models.AccountStatusActive)
	a3 := testAccount(t, r, "did:plc:resumed3", host.ID, models.AccountStatusHostThrottled, models.AccountStatusActive)

	require.NoError(t, r.UpdateHostAccountLimit(ctx, host.ID, 3))

	assert.Equal(t, models.AccountStatusActive, testAccountRow(t, r, a2.UID).Status)
	assert.Equal(t, models.AccountStatusActive, testAccountRow(t, r, a3.UID).Status)
}

// Only as many accounts as the limit has room for are promoted, lowest UID first, and accounts inactive upstream are left for later.
func TestUpdateHostAccountLimitPromotesUpToRoom(t *testing.T) {
	ctx := context.Background()
	r := testThrottleRelay(t, identity.NewMockDirectory())

	host := testThrottleHost(t, r, "partial.example.com", 5, 1)
	testAccount(t, r, "did:plc:partial1", host.ID, models.AccountStatusActive, models.AccountStatusActive)
	inactive := testAccount(t, r, "did:plc:partial2", host.ID, models.AccountStatusHostThrottled, models.AccountStatusDeactivated)
	first := testAccount(t, r, "did:plc:partial3", host.ID, models.AccountStatusHostThrottled, models.AccountStatusActive)
	second := testAccount(t, r, "did:plc:partial4", host.ID, models.AccountStatusHostThrottled, models.AccountStatusActive)
	third := testAccount(t, r, "did:plc:partial5", host.ID, models.AccountStatusHostThrottled, models.AccountStatusActive)

	require.NoError(t, r.UpdateHostAccountLimit(ctx, host.ID, 3))

	assert.Equal(t, models.AccountStatusHostThrottled, testAccountRow(t, r, inactive.UID).Status)
	assert.Equal(t, models.AccountStatusActive, testAccountRow(t, r, first.UID).Status)
	assert.Equal(t, models.AccountStatusActive, testAccountRow(t, r, second.UID).Status)
	assert.Equal(t, models.AccountStatusHostThrottled, testAccountRow(t, r, third.UID).Status)
	assert.Equal(t, int64(3), testHostRow(t, r, host.ID).AccountLimit)
}

func testHostRow(t *testing.T, r *Relay, id uint64) models.Host {
	t.Helper()
	var h models.Host
	require.NoError(t, r.db.First(&h, id).Error)
	return h
}

// An account left throttled on a host which has since come back under its limit is promoted the next time it has an event checked.
func TestEnsureAccountActiveUnthrottlesWhenHostHasRoom(t *testing.T) {
	ctx := context.Background()
	r := testThrottleRelay(t, identity.NewMockDirectory())

	host := testThrottleHost(t, r, "roomy.example.com", 90, 100)
	acc := testThrottledAccount(t, r, syntax.DID("did:plc:droppedwithroom"), host.ID, models.AccountStatusActive)

	require.NoError(t, r.EnsureAccountActive(ctx, acc))

	assert.Equal(t, models.AccountStatusActive, acc.Status)
	assert.Equal(t, models.AccountStatusActive, testAccountRow(t, r, acc.UID).Status)
}

// On a host still over its limit, the account stays throttled and its events are still dropped.
func TestEnsureAccountActiveKeepsThrottleOnFullHost(t *testing.T) {
	ctx := context.Background()
	r := testThrottleRelay(t, identity.NewMockDirectory())

	host := testThrottleHost(t, r, "full.example.com", 150, 100)
	acc := testThrottledAccount(t, r, syntax.DID("did:plc:droppedwhenfull"), host.ID, models.AccountStatusActive)

	require.Error(t, r.EnsureAccountActive(ctx, acc))

	assert.Equal(t, models.AccountStatusHostThrottled, acc.Status)
	assert.Equal(t, models.AccountStatusHostThrottled, testAccountRow(t, r, acc.UID).Status)
}

func TestInactiveAccountWarning(t *testing.T) {
	assert.Equal(t, "host-throttled", inactiveAccountWarning(&models.Account{Status: models.AccountStatusHostThrottled, UpstreamStatus: models.AccountStatusActive}))
	assert.Equal(t, "inactive-account", inactiveAccountWarning(&models.Account{Status: models.AccountStatusTakendown, UpstreamStatus: models.AccountStatusActive}))
	assert.Equal(t, "inactive-account", inactiveAccountWarning(&models.Account{Status: models.AccountStatusActive, UpstreamStatus: models.AccountStatusDeactivated}))
}
