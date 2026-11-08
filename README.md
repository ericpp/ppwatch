# ppwatch

IRC bot that monitors the Hive blockchain for podping notifications and posts
updates to IRC channels that subscribe to specific feeds.

## Features

- Monitor the Hive blockchain for podping notifications (block polling with
  automatic node failover)
- Post updates to multiple IRC channels with configurable message pacing
- Filter by subscribed feed URLs (normalized matching)
- `!pp` / `!pplive` / `!ppend` commands for manual podpings, with feed aliases
- liveItem status verification for `live` / `liveEnd` pings
- Signed Hive transactions (posting authority) with dry-run mode
- SSL/TLS connections, NickServ authentication, automatic reconnection

## Quick start

Copy and edit the configuration file:

```
cp irc_bot_config.json my_config.json
```

Run the bot:

```
ppwatch --config my_config.json
```

The configuration path can also be provided through the `CONFIG_FILE`
environment variable (the `--config` flag takes precedence).

## Configuration

The JSON configuration file has these sections:

| Section | Keys |
|---|---|
| `irc` | `host`, `port`, `nick`, `user`, `realname`, `secure`, `nickserv_password` |
| `podcast_index` | `api_key`, `api_secret` |
| `podping_writer` | `hive_account`, `hive_posting_key`, `hive_nodes`, `dry_run` |
| top level | `channel_subscriptions`, `feed_aliases`, `command_name`, `message_delay`, `api_timeout`, `user_agent_email`, `debug_logging` |

`channel_subscriptions` maps a channel to a feed URL or list of URLs.
`feed_aliases` maps short names to Podcast Index feed ids, e.g.
`{"bts": 150842}`.

## Build

Requires Go 1.23+:

```
make            # test + build
make test       # run the test suite
make linux      # static linux/amd64 binary
```

Or directly: `go build -o ppwatch ./cmd/ppwatch`.

## Docker

```
make docker           # builds ericpp/ppwatch
docker compose up -d  # uses docker-compose.yml in this directory
```

The image is a small multi-stage build (static binary, non-root user). Mount
the config read-only:

```
docker run -v ./irc_bot_config.json:/app/config.json:ro ericpp/ppwatch
```

## Building notes

The Go toolchain needs a filesystem that supports file locking. On network or
9P-style mounts (e.g. some Windows mapped drives, or WSL `/mnt/*` paths) builds
can fail or be very slow - copy the tree to a local disk first.

## Hive transaction signing

Transactions are serialized by the node (`condenser_api.get_transaction_hex`),
then signed locally:

- digest = `sha256(chain_id + serialized tx without the trailing signature
  count)` with the Hive mainnet chain id `beeab0de...`
- RFC6979 deterministic ECDSA over secp256k1, retried with extra entropy until
  the signature satisfies graphene's canonicality check (low-R and low-S
  32-byte encodings)
- compact recoverable signature `[27+4+recovery_id, r(32), s(32)]`, hex
  encoded; every signature is verified via public-key recovery before broadcast

Curve arithmetic uses `github.com/btcsuite/btcd/btcec/v2`; WIF decoding
(double-SHA256 base58check) and base58 are implemented locally.
