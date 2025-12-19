# PodPing IRC Bot

IRC bot that monitors the Hive blockchain for podping notifications and posts updates to IRC channels that are interested in specific URLs.

## Quick Start

### Installation

From the project root:

```bash
# Install with uv
uv sync
```

### Configuration

Copy and edit the configuration file:

```bash
cp irc_bot_config.json my_config.json
# Edit my_config.json with your settings
```

### Running the Bot

From the project root, use `uv run`:

```bash
# Run with config file
uv run src/ppwatch.py --config irc_bot_config.json

# Or with custom config
uv run src/ppwatch.py --config my_config.json
```

## Features

- Monitor Hive blockchain for podping notifications
- Filter by URL patterns (exact match, wildcard, regex)
- Post updates to multiple IRC channels
- Support for SSL/TLS connections
- NickServ authentication
- Configurable message formats
- Rate limiting and flood protection

## Dependencies

This bot depends on:

- `podping-hivewatcher-py` (core library, installed from workspace)
- `asif` (modern async IRC bot framework)
- `httpx>=0.24.0` (HTTP client)

All dependencies are managed by `uv` and defined in `pyproject.toml`.

