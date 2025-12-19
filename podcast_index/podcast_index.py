"""
Podcast Index API client for fetching podcast metadata.
"""

import asyncio
import hashlib
import logging
import time
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional
from datetime import datetime

import aiohttp

from .errors import PodcastIndexError, PodcastIndexAuthError, PodcastIndexNotFound

logger = logging.getLogger(__name__)


@dataclass
class PodcastMetadata:
    """Podcast metadata from Podcast Index API."""
    
    id: int
    title: str
    url: str
    original_url: Optional[str] = None
    link: Optional[str] = None
    description: Optional[str] = None
    author: Optional[str] = None
    image: Optional[str] = None
    last_update_time: Optional[int] = None
    last_crawl_time: Optional[int] = None
    itunes_id: Optional[int] = None
    language: Optional[str] = None
    categories: Optional[Dict[str, str]] = None
    extra: Dict[str, Any] = field(default_factory=dict)
    
    def display_name(self) -> str:
        """Get the podcast's display name (title or URL if no title)."""
        return self.title if self.title else self.url
    
    def last_update_datetime(self) -> Optional[datetime]:
        """Get the last update time as a DateTime."""
        if self.last_update_time:
            return datetime.fromtimestamp(self.last_update_time)
        return None
    
    def categories_string(self) -> str:
        """Get formatted categories as a string."""
        if self.categories:
            return ", ".join(self.categories.values())
        return "Unknown"


class PodcastIndexClient:
    """Client for the Podcast Index API."""
    
    def __init__(
        self,
        api_key: str,
        api_secret: str,
        base_url: str = "https://api.podcastindex.org/api/1.0",
    ) -> None:
        """Initialize the Podcast Index client.
        
        Args:
            api_key: Podcast Index API key
            api_secret: Podcast Index API secret
            base_url: Base URL for the API
        """
        self.api_key = api_key
        self.api_secret = api_secret
        self.base_url = base_url
        self._session: Optional[aiohttp.ClientSession] = None
    
    async def __aenter__(self) -> "PodcastIndexClient":
        """Async context manager entry."""
        await self._ensure_session()
        return self
    
    async def __aexit__(self, exc_type: Any, exc_val: Any, exc_tb: Any) -> None:
        """Async context manager exit."""
        await self.close()
    
    async def _ensure_session(self) -> None:
        """Ensure aiohttp session is created."""
        if self._session is None or self._session.closed:
            self._session = aiohttp.ClientSession()
    
    async def close(self) -> None:
        """Close the HTTP session."""
        if self._session and not self._session.closed:
            await self._session.close()
    
    def _generate_auth_headers(self) -> Dict[str, str]:
        """Generate authentication headers for Podcast Index API."""
        unix_time = str(int(time.time()))
        
        # Create authorization hash: SHA1(api_key + api_secret + unix_time)
        auth_string = self.api_key + self.api_secret + unix_time
        auth_hash = hashlib.sha1(auth_string.encode()).hexdigest()
        
        return {
            "User-Agent": "PodPing-Podcast-Index-Python/1.0.0",
            "X-Auth-Date": unix_time,
            "X-Auth-Key": self.api_key,
            "Authorization": auth_hash,
        }
    
    async def lookup_by_feed_url(self, feed_url: str) -> Optional[PodcastMetadata]:
        """Look up podcast information by feed URL.
        
        Args:
            feed_url: The podcast feed URL
            
        Returns:
            PodcastMetadata if found, None if not found
            
        Raises:
            PodcastIndexError: If the API request fails
        """
        await self._ensure_session()
        
        url = f"{self.base_url}/podcasts/byfeedurl"
        headers = self._generate_auth_headers()
        params = {"url": feed_url}
        
        try:
            assert self._session is not None
            async with self._session.get(url, headers=headers, params=params) as response:
                if response.status == 401:
                    raise PodcastIndexAuthError("Authentication failed - check API credentials")
                
                if response.status != 200:
                    raise PodcastIndexError(f"HTTP {response.status}: {response.reason}")
                
                data = await response.json()
                
                if data.get("status") == "false":
                    description = data.get("description", "")
                    if "not found" in description.lower():
                        return None
                    else:
                        raise PodcastIndexError(description)
                
                feed_data = data.get("feed")
                if not feed_data:
                    return None
                
                return self._parse_podcast_metadata(feed_data)
        
        except aiohttp.ClientError as e:
            raise PodcastIndexError(f"Request failed: {e}")
        except Exception as e:
            if isinstance(e, PodcastIndexError):
                raise
            raise PodcastIndexError(f"Unexpected error: {e}")
    
    async def lookup_by_feed_id(self, feed_id: int) -> Optional[PodcastMetadata]:
        """Look up podcast information by feed ID.
        
        Args:
            feed_id: The podcast feed ID
            
        Returns:
            PodcastMetadata if found, None if not found
            
        Raises:
            PodcastIndexError: If the API request fails
        """
        await self._ensure_session()
        
        url = f"{self.base_url}/podcasts/byfeedid"
        headers = self._generate_auth_headers()
        params = {"id": feed_id}
        
        try:
            assert self._session is not None
            async with self._session.get(url, headers=headers, params=params) as response:
                if response.status == 401:
                    raise PodcastIndexAuthError("Authentication failed - check API credentials")
                
                if response.status != 200:
                    raise PodcastIndexError(f"HTTP {response.status}: {response.reason}")
                
                data = await response.json()
                
                if data.get("status") == "false":
                    description = data.get("description", "")
                    if "not found" in description.lower():
                        return None
                    else:
                        raise PodcastIndexError(description)
                
                feed_data = data.get("feed")
                if not feed_data:
                    return None
                
                return self._parse_podcast_metadata(feed_data)
        
        except aiohttp.ClientError as e:
            raise PodcastIndexError(f"Request failed: {e}")
        except Exception as e:
            if isinstance(e, PodcastIndexError):
                raise
            raise PodcastIndexError(f"Unexpected error: {e}")
    
    def _parse_podcast_metadata(self, data: Dict[str, Any]) -> PodcastMetadata:
        """Parse podcast metadata from API response."""
        # Handle categories
        categories = None
        if "categories" in data and isinstance(data["categories"], dict):
            categories = data["categories"]
        
        return PodcastMetadata(
            id=data["id"],
            title=data.get("title", ""),
            url=data.get("url", ""),
            original_url=data.get("originalUrl"),
            link=data.get("link"),
            description=data.get("description"),
            author=data.get("author"),
            image=data.get("image"),
            last_update_time=data.get("lastUpdateTime"),
            last_crawl_time=data.get("lastCrawlTime"),
            itunes_id=data.get("itunesId"),
            language=data.get("language"),
            categories=categories,
            extra={k: v for k, v in data.items() 
                  if k not in ["id", "title", "url", "originalUrl", "link", 
                              "description", "author", "image", "lastUpdateTime", 
                              "lastCrawlTime", "itunesId", "language", "categories"]},
        )
    
    async def lookup_multiple(self, feed_urls: List[str]) -> Dict[str, Optional[PodcastMetadata]]:
        """Look up multiple podcasts by feed URLs.
        
        Args:
            feed_urls: List of podcast feed URLs
            
        Returns:
            Dictionary mapping feed URLs to their metadata (if found)
        """
        if not feed_urls:
            return {}
        
        # Process URLs concurrently but with rate limiting
        semaphore = asyncio.Semaphore(5)  # Max 5 concurrent requests
        
        async def lookup_with_rate_limit(url: str) -> tuple[str, Optional[PodcastMetadata]]:
            async with semaphore:
                # Small delay to avoid hitting rate limits
                await asyncio.sleep(0.1)
                
                try:
                    metadata = await self.lookup_by_feed_url(url)
                    return url, metadata
                except Exception as e:
                    logger.warning(f"Failed to lookup podcast {url}: {e}")
                    return url, None
        
        # Execute all lookups concurrently
        tasks = [lookup_with_rate_limit(url) for url in feed_urls]
        results = await asyncio.gather(*tasks)
        
        return dict(results)
