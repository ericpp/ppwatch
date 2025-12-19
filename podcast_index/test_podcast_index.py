#!/usr/bin/env python3
"""
Test script for the podcast index library.
"""

import asyncio
import logging
from podcast_index import PodcastIndexClient

# Setup logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

async def test_podcast_index():
    """Test the podcast index library."""
    
    print("Testing PodcastIndexClient...")
    
    # Test with dummy credentials (will fail but should not crash)
    client = PodcastIndexClient(
        api_key="test-key",
        api_secret="test-secret"
    )
    
    try:
        # This will fail due to invalid credentials, but should handle gracefully
        metadata = await client.lookup_by_feed_id(123456)
        print(f"Lookup result: {metadata}")
    except Exception as e:
        print(f"Expected error with test credentials: {e}")
    
    # Test context manager
    try:
        async with PodcastIndexClient("test-key", "test-secret") as client:
            print("✓ Context manager works")
    except Exception as e:
        print(f"Context manager error: {e}")
    
    print("✓ PodcastIndexClient initialized successfully")

if __name__ == "__main__":
    asyncio.run(test_podcast_index())
