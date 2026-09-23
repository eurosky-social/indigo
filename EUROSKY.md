# Eurosky fork

This is Eurosky's fork of [bluesky-social/indigo](https://github.com/bluesky-social/indigo). We track upstream closely and keep our own changes small.

## Branches

- **`eurosky`** (default): upstream plus our changes. Work here: branch off it and open PRs into it.
- **`main`**: an exact mirror of `upstream/main`. Never commit to it; [sync-upstream.yaml](.github/workflows/sync-upstream.yaml) fast-forwards it.

Merge methods:

- **Our PRs:** squash, so each change lands as a single commit that can be reverted on its own.
- **Sync PRs** (`sync/upstream-*`): **Create a merge commit**. Never squash or rebase them: that drops the upstream ancestry, and the next sync conflicts on everything.

The branch was rebuilt on 2026-09-23 as upstream `main` plus the commits listed below; the previous `main` and `eurosky` are kept as the `archive/main-2026-09-23` and `archive/eurosky-2026-09-23` tags.

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

`go.mod` / `go.sum` conflicts: take upstream's version (`git checkout --theirs go.mod go.sum`) and run `go mod tidy`, which re-adds the one dependency our code needs (`goimagehash`). If upstream regenerated code (`make lexgen`, `make cborgen`), rerun the generator after resolving instead of merging generated files by hand.

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

| Change                                                                                                                                                  | Commit      | Files                                                                                                                                                      | Status                       |
| ------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------- |
| Relay: `--allow-private-networks` (`RELAY_ALLOW_PRIVATE_NETWORKS`) lets admin `requestCrawl` and the slurper reach private-network hosts (no SSRF guard) | `36ed2535`  | `cmd/relay/{handlers,main,service}.go`, `cmd/relay/relay/{host_checker,relay,slurper}.go`                                                                  | Ours only                    |
| Relay: `lexicon_type_counter` metric (commit ops per PDS and collection)                                                                                | `b7a3fc4c`  | `cmd/relay/relay/{ingest,metrics}.go`                                                                                                                      | Ours only; could go upstream |
| Flashes lexicon types (`api/flashes`, `cmd/lexgen/flashes.json`, `make lexgen`) and hepa `--collection-filter` (`HEPA_COLLECTION_FILTER`)                | `a5febc27`  | `api/flashes/*`, `cmd/lexgen/flashes.json`, `gen/main.go`, `Makefile`, `automod/consumer/firehose.go`, `cmd/hepa/main.go`                                  | Ours only                    |
| hepa: CSAM detection via an external service (`HEPA_CSAM_HOST`, `HEPA_CSAM_API_TOKEN`), `mock-csam` stand-in service and image, docker-compose            | `eb635a04`  | `automod/visual/{csam_client,csam_rule,metrics}.go`, `cmd/hepa/{main,server}.go`, `cmd/hepa/mock-csam/*`, `cmd/hepa/docker-compose.yml`                    | Ours only                    |
| hepa: 60s HTTP timeout for the Ozone client (serverless cold starts)                                                                                    | `6b431bcd`  | `cmd/hepa/server.go`                                                                                                                                       | Ours only                    |
| Docker: hepa and relay images cache Go modules and build without `.git`                                                                                 | `dc9fa5e4`  | `cmd/hepa/Dockerfile`, `cmd/relay/Dockerfile`                                                                                                              | Ours only                    |
| hepa: spam image detection by perceptual hash (`HEPA_SPAM_IMAGE_PATH`, `HEPA_SPAM_HASH_THRESHOLD`), reference image shipped in the hepa image             | `f0e4ed76`  | `automod/visual/{spam_hash_client,spam_hash_rule,metrics}.go`, `cmd/hepa/{main,server}.go`, `cmd/hepa/{Dockerfile,spam.jpg}`, `go.mod`, `go.sum`            | Ours only; demo              |
| Fork CI: upstream sync workflow, multi-arch GHCR image builds from `eurosky` for relay, hepa and mock-csam, golang.yml on `eurosky`                      | `275a927c`  | `sync-upstream.yaml`, `container-{hepa,relay,mock-csam}-ghcr.yaml`, `golang.yml`                                                                           | Ours only                    |
| This file                                                                                                                                               | see git log | `EUROSKY.md`                                                                                                                                               | Ours only                    |

### Not carried over from the archive

Changes on `archive/eurosky-2026-09-23` that were left behind when the branch was rebuilt:

- **Relay warning counter for non-active accounts** (`1ee5fcb5b`): upstream took it as-is (`f54de4ed`).
- **hepa test harness** (`testing/hepa_*`, `testing/utils.go` story helper, `testing/*.jpg`, edits to `pds/` and `plc/fakedid.go`): upstream removed the `pds/` and `plc/` packages it relied on.
- **`automod/engine/engine.go`**: identity and account event processing commented out. A workaround, not a feature; upstream's code is kept.
- **`automod/cachestore` moved to `automod/consumer/cachestore`**: no functional change.
- **Downgrades** of `versioninfo` and `urfave/cli` (kw-cli) and the `replace github.com/bluesky-social/indigo => ./` in `go.mod`: rebase leftovers.
- **`api/bsky/unspeccedcheckHandleAvailability.go`**: stray generated file, unused.
- Whitespace-only edits to `README.md` and `automod/rules/blobs.go`.
