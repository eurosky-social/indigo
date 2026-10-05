
`rainbow`: atproto Firehose Fanout Service
==========================================

This is an atproto service which consumes from a firehose (eg, from a relay or PDS) and fans out events to many subscribers.

Features and design points:

- retains "backfill window" on local disk (using [pebble](https://github.com/cockroachdb/pebble))
- serves the `com.atproto.sync.subscribeRepos` endpoint (WebSocket)
- proxies through public and administrative API requests to the backing host
- retains upstream firehose "sequence numbers"
- does not validate events (signatures, repo tree, hashes, etc), just passes through
- does not archive or mirror individual records or entire repositories (or implement related API endpoints)
- somewhat disk I/O intensive: fast NVMe disks are recommended, and RAM is helpful for caching
- single golang binary for easy deployment
- observability: logging, prometheus metrics, and OTEL traces

## Running 

This is a simple, single-binary Go program. You can also build and run it as a docker container (see `./Dockerfile`).

From the top level of this repo, you can build:

```shell
go build ./cmd/rainbow -o rainbow
```

or just run it, and see configuration options:

```shell
go run ./cmd/rainbow --help
```

## Live delivery and cursor playback

A consumer that connects with a cursor is first sent stored events from disk ("playback") until it has caught up, and only then joins the live stream. Playback shares the disk and CPU with intake from the upstream host, so a consumer replaying days of events can make every other consumer lag. These options keep live delivery ahead:

- `--persist-no-sync` (`RAINBOW_PERSIST_NO_SYNC`): don't fsync each event. With the default, intake can't go faster than the disk does fsyncs one after the other (a few hundred per second on network block storage). Events that had not reached the disk when the machine crashed are fetched again from the upstream host on restart; a consumer that was already sent them sees them a second time.
- `--playback-rate-limit` (`RAINBOW_PLAYBACK_RATE_LIMIT`): most events per second sent to one consumer during playback. Set it to several times the live event rate: a consumer catches up at the difference between the two, and never does if the limit is below the live rate.
- `--playback-global-rate-limit` (`RAINBOW_PLAYBACK_GLOBAL_RATE_LIMIT`): the same across all consumers in playback, which is what bounds the load of a client that opens several connections.
- `--playback-exempt` (`RAINBOW_PLAYBACK_EXEMPT`): IP addresses or CIDR ranges of consumers neither limit applies to. The address is the one in `X-Forwarded-For` when there is a reverse proxy in front, so the proxy has to overwrite that header, not append to it.

The limits only apply during playback. Events sent to a consumer that has caught up are never limited.

The `spl_active_playbacks` and `spl_playback_events_sent_total` metrics (labelled `class="throttled"` or `"exempt"`) show how many consumers are in playback and how fast they are being served.
