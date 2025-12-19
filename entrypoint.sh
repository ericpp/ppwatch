#!/bin/bash
set -e

# Check if CONFIG_FILE environment variable is set
if [ -z "$CONFIG_FILE" ]; then
    echo "Error: CONFIG_FILE environment variable is required"
    echo "Usage: CONFIG_FILE=/path/to/config.json $0"
    echo "Or: $0 --config /path/to/config.json"
    exit 1
fi

# Check if config file exists
if [ ! -f "$CONFIG_FILE" ]; then
    echo "Error: Configuration file not found: $CONFIG_FILE"
    exit 1
fi

# Run the IRC bot
echo "Starting IRC bot with config: $CONFIG_FILE"
exec uv run src/ppwatch.py --config "$CONFIG_FILE"
