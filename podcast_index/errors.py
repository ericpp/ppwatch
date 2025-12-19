"""
Error types for the Podcast Index library.
"""

from typing import Optional


class PodcastIndexError(Exception):
    """Base exception for Podcast Index-related errors."""
    
    def __init__(self, message: str, cause: Optional[Exception] = None) -> None:
        super().__init__(message)
        self.message = message
        self.cause = cause
    
    def __str__(self) -> str:
        if self.cause:
            return f"{self.message}: {self.cause}"
        return self.message


class PodcastIndexAuthError(PodcastIndexError):
    """Authentication errors with Podcast Index API."""
    pass


class PodcastIndexNotFound(PodcastIndexError):
    """Podcast not found in Podcast Index."""
    pass
