"""
Podcast Index API client library.

This library provides a client for interacting with the Podcast Index API
to fetch podcast metadata and information.
"""

from .podcast_index import PodcastIndexClient, PodcastMetadata
from .errors import PodcastIndexError, PodcastIndexAuthError, PodcastIndexNotFound

__all__ = [
    "PodcastIndexClient",
    "PodcastMetadata", 
    "PodcastIndexError",
    "PodcastIndexAuthError",
    "PodcastIndexNotFound",
]
