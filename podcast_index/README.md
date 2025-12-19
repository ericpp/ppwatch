# Podcast Index API Client

A Python client library for the Podcast Index API, providing easy access to podcast metadata and information.

## Features

- **Async/Await Support**: Full async/await support for non-blocking operations
- **Feed Lookup**: Look up podcasts by feed URL or feed ID
- **Batch Operations**: Look up multiple podcasts concurrently with rate limiting
- **Rich Metadata**: Access to comprehensive podcast information including title, description, categories, and more
- **Error Handling**: Comprehensive error handling with specific exception types

## Installation

```bash
pip install podcast-index
```

## Quick Start

```python
import asyncio
from podcast_index import PodcastIndexClient

async def main():
    # Initialize client with your API credentials
    client = PodcastIndexClient(
        api_key="your-api-key",
        api_secret="your-api-secret"
    )
    
    # Look up podcast by feed URL
    metadata = await client.lookup_by_feed_url("https://example.com/feed.xml")
    if metadata:
        print(f"Found: {metadata.title}")
        print(f"Description: {metadata.description}")
    
    # Look up podcast by feed ID
    metadata = await client.lookup_by_feed_id(123456)
    if metadata:
        print(f"Found: {metadata.title}")
    
    # Look up multiple podcasts
    urls = [
        "https://example.com/feed1.xml",
        "https://example.com/feed2.xml"
    ]
    results = await client.lookup_multiple(urls)
    for url, metadata in results.items():
        if metadata:
            print(f"{url}: {metadata.title}")
    
    # Close the client
    await client.close()

# Run the example
asyncio.run(main())
```

## API Reference

### PodcastIndexClient

The main client class for interacting with the Podcast Index API.

#### Constructor

```python
PodcastIndexClient(api_key: str, api_secret: str, base_url: str = "https://api.podcastindex.org/api/1.0")
```

- `api_key`: Your Podcast Index API key
- `api_secret`: Your Podcast Index API secret
- `base_url`: Base URL for the API (defaults to production)

#### Methods

##### `lookup_by_feed_url(feed_url: str) -> Optional[PodcastMetadata]`

Look up podcast information by feed URL.

- `feed_url`: The podcast feed URL
- Returns: `PodcastMetadata` if found, `None` if not found
- Raises: `PodcastIndexError` if the API request fails

##### `lookup_by_feed_id(feed_id: int) -> Optional[PodcastMetadata]`

Look up podcast information by feed ID.

- `feed_id`: The podcast feed ID
- Returns: `PodcastMetadata` if found, `None` if not found
- Raises: `PodcastIndexError` if the API request fails

##### `lookup_multiple(feed_urls: List[str]) -> Dict[str, Optional[PodcastMetadata]]`

Look up multiple podcasts by feed URLs with concurrent processing and rate limiting.

- `feed_urls`: List of podcast feed URLs
- Returns: Dictionary mapping feed URLs to their metadata (if found)

##### `close()`

Close the HTTP session. Should be called when done with the client.

### PodcastMetadata

Data class containing podcast metadata from the Podcast Index API.

#### Attributes

- `id`: Podcast feed ID
- `title`: Podcast title
- `url`: Feed URL
- `original_url`: Original feed URL (if different)
- `link`: Website link
- `description`: Podcast description
- `author`: Podcast author
- `image`: Image URL
- `last_update_time`: Last update timestamp
- `last_crawl_time`: Last crawl timestamp
- `itunes_id`: iTunes ID
- `language`: Language code
- `categories`: Categories dictionary
- `extra`: Additional fields

#### Methods

##### `display_name() -> str`

Get the podcast's display name (title or URL if no title).

##### `last_update_datetime() -> Optional[datetime]`

Get the last update time as a DateTime object.

##### `categories_string() -> str`

Get formatted categories as a string.

## Error Handling

The library provides specific exception types for different error conditions:

- `PodcastIndexError`: Base exception for all Podcast Index errors
- `PodcastIndexAuthError`: Authentication errors
- `PodcastIndexNotFound`: Podcast not found (though this is handled gracefully by returning `None`)

## Context Manager Support

The client supports async context manager usage:

```python
async with PodcastIndexClient(api_key, api_secret) as client:
    metadata = await client.lookup_by_feed_url("https://example.com/feed.xml")
    # Client is automatically closed
```

## License

This library is part of the PodPing ecosystem and follows the same licensing terms.
