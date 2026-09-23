# Eurosky fork

This is Eurosky's fork of [bluesky-social/indigo](https://github.com/bluesky-social/indigo). We track upstream closely and keep our own changes small.

## Branches

- **`eurosky`** (default): upstream plus our changes. Work here: branch off it and open PRs into it.
- **`main`**: an exact mirror of `upstream/main`. Never commit to it; [sync-upstream.yaml](.github/workflows/sync-upstream.yaml) fast-forwards it.

Merge methods:

- **Our PRs:** squash, so each change lands as a single commit that can be reverted on its own.
- **Sync PRs** (`sync/upstream-*`): **Create a merge commit**. Never squash or rebase them: that drops the upstream ancestry, and the next sync conflicts on everything.

## CI and images

- [golang.yml](.github/workflows/golang.yml) (upstream's build, test and lint) runs on PRs and on pushes to `eurosky`.
- Container images are built by the `container-*-ghcr.yaml` workflows on pushes to `eurosky` and pushed to `ghcr.io/eurosky-social/indigo`. Each build also pushes a git tag named after the image tag.

| Image     | Workflow                                                                             | Tags                                                                                                       |
| --------- | ------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------- |
| relay     | [container-relay-ghcr.yaml](.github/workflows/container-relay-ghcr.yaml)             | `relay-<sha>`, `relay-<YYYY-MM-DDTHH-mm-ssZ>`, `relay-latest`                                              |
| hepa      | [container-hepa-ghcr.yaml](.github/workflows/container-hepa-ghcr.yaml)               | `hepa-eurosky-<sha>`, `hepa-eurosky-<YYYY-MM-DDTHH-mm-ssZ>`, `hepa-eurosky-latest`                         |
| mock-csam | [container-mock-csam-ghcr.yaml](.github/workflows/container-mock-csam-ghcr.yaml)     | `mock-csam-eurosky-<sha>`, `mock-csam-eurosky-<YYYY-MM-DDTHH-mm-ssZ>`, `mock-csam-eurosky-latest`          |

Upstream's other `container-*` workflows are guarded with `if: github.repository == 'bluesky-social/indigo'` and never run here. Neither does `sync-internal.yaml`.

## Syncing with upstream

Every Monday the sync workflow fast-forwards `main`, merges it into a `sync/upstream-<date>` branch cut from `eurosky`, and opens a PR. You can also run it by hand from the Actions tab.

If the merge conflicts, the workflow fails and lists the files. Resolve it locally:

```bash
git fetch upstream origin
git switch -c sync/upstream-$(date +%F) origin/eurosky
git merge upstream/main
# resolve, then: go build ./... && make test && make lint
git push -u origin HEAD   # open a PR into eurosky, merge with a merge commit
```

`go.mod` / `go.sum` conflicts: take upstream's version (`git checkout --theirs go.mod go.sum`) and run `go mod tidy`, which re-adds the few dependencies our code needs. If upstream regenerated code (`make lexgen`, `make cborgen`), rerun the generator after resolving instead of merging generated files by hand.

Turn on `git config rerere.enabled true` so git reuses your previous conflict resolutions.

## When upstream covers one of our changes

Revert ours on the sync branch **before** merging upstream, and name what replaced it:

```bash
git switch -c sync/upstream-$(date +%F) origin/eurosky
git revert <commit>   # "Drop <change>: superseded by bluesky-social/indigo#NNNN"
git merge upstream/main
```

Then update the table below. If upstream took our change as-is (we sent it upstream), the merge absorbs it and there's nothing to do.

To see what still differs from upstream:

```bash
git diff --stat main...eurosky                                              # what's different
git log --oneline --no-merges --right-only --cherry-mark main...eurosky     # = already upstream, + ours only
git log --first-parent --oneline eurosky                                    # our PRs and syncs only
```

## Our changes

Commits are as of the branch before the first upstream sync; they predate the merge model, so they are plain commits on `eurosky` rather than squashed PRs.

| Change                                                                                                                                                                                  | Commit(s)                                          | Files                                                                                                                                                                                                                                                                                | Status                                                            |
| --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------- |
| Relay: `--allow-private-networks` (`RELAY_ALLOW_PRIVATE_NETWORKS`) lets admin `requestCrawl` reach private-network hosts (bypasses SSRF protection); Dockerfile tweaks                  | `8d68152d2`                                        | `cmd/relay/{Dockerfile,handlers.go,main.go,service.go}`, `cmd/relay/relay/{host_checker,relay,slurper}.go`                                                                                                                                                                           | Ours only                                                         |
| Relay: `events_warnings_counter` metric for events from non-active accounts                                                                                                             | `1ee5fcb5b`                                        | `cmd/relay/relay/{ingest,metrics}.go`                                                                                                                                                                                                                                                | Sent upstream, taken as-is (`f54de4ed`); absorbed by the sync     |
| Relay: `lexicon_type_counter` metric (records per `$type` and PDS)                                                                                                                      | `fd3c1fda5`                                        | `cmd/relay/relay/{ingest,metrics}.go`                                                                                                                                                                                                                                                | Ours only; could go upstream                                      |
| Flashes lexicon (`api/flashes`, `cmd/lexgen/flashes.json`, `make lexgen`) and automod/hepa support for it: collection filter (`HEPA_COLLECTION_FILTER`), engine and consumer changes     | `c63a37d7`, `9cebabce`, `b906e2c4`, `7bb09a3e`     | `api/flashes/*`, `cmd/lexgen/flashes.json`, `gen/main.go`, `Makefile`, `automod/consumer/*`, `automod/engine/*`, `automod/rules/*`, `cmd/hepa/{main,server}.go`, `api/bsky/unspeccedcheckHandleAvailability.go`                                                                    | Ours only                                                         |
| CSAM scanning for hepa (`HEPA_CSAM_HOST`, `HEPA_CSAM_API_TOKEN`) with a `mock-csam` test service and image                                                                              | `c63a37d7`, `b906e2c4`                             | `automod/visual/{csam_client,csam_rule,metrics}.go`, `cmd/hepa/mock-csam/*`, `cmd/hepa/{Dockerfile,docker-compose.yml}`, `container-mock-csam-ghcr.yaml`                                                                                                                             | Ours only                                                         |
| Spam image-hash scanning demo for hepa (`HEPA_SPAM_IMAGE_PATH`, `HEPA_SPAM_HASH_THRESHOLD`)                                                                                             | `314249960`                                        | `automod/visual/{spam_hash_client,spam_hash_rule}.go`, `cmd/hepa/spam.jpg`, `testing/spam-*.jpg`, `testing/hepa_mocks.go`                                                                                                                                                            | Ours only; demo                                                   |
| Test harness for hepa: in-memory blob store in the test PDS, `getBlob` without auth, fake DID service type, external/integration tests                                                  | `c63a37d7`, `314249960`                            | `pds/{handlers,server}.go`, `plc/fakedid.go`, `testing/{utils,hepa_mocks}.go`, `testing/hepa_*_test.go`, `testing/*.jpg`                                                                                                                                                             | Ours only; upstream has since removed `pds/` and `plc/` entirely  |
| Fork CI: multi-arch GHCR image builds with branch-prefixed tags and git tags for hepa, relay and mock-csam; upstream sync workflow; builds triggered from `eurosky`; `gen/main.go` import fix | `8d68152d2`, `608b490c`, `b906e2c4`, #2            | `sync-upstream.yaml`, `container-{hepa,relay,mock-csam}-ghcr.yaml`, `golang.yml`, `gen/main.go`                                                                                                                                                                                      | Ours only                                                         |

