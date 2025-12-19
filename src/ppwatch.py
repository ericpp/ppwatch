#!/usr/bin/env python3
"""
IRC Podping Bot - Posts podping updates to IRC channels
"""

import argparse
import asyncio
import json
import logging
import re
from urllib.parse import quote, urlsplit, urlunsplit
from collections import defaultdict
from dataclasses import dataclass, field
from pathlib import Path
from typing import Dict, Set, Optional, Tuple
from xml.etree import ElementTree as ET

import httpx
from asif.bot import Client, Channel
from pypodping import PodpingWatcher, PodpingWriter, PodpingError
from podcast_index import PodcastIndexClient

# Compatibility for Python < 3.11
try:
    from asyncio import timeout as asyncio_timeout
except ImportError:
    from async_timeout import timeout as asyncio_timeout

# Setup logging
logging.basicConfig(
    level=logging.DEBUG,
    format="[%(asctime)s] %(levelname)s: %(filename)s:%(funcName)s(%(lineno)s): %(message)s"
)
logger = logging.getLogger(__name__)


@dataclass
class BotConfig:
    """Complete bot configuration."""
    # IRC settings
    irc_host: str = "irc.libera.chat"
    irc_port: int = 6667
    irc_nick: str = "podping-bot"
    irc_user: str = "podping"
    irc_realname: str = "Podping RSS Update Bot"
    irc_secure: bool = False
    irc_nickserv_password: Optional[str] = None
    
    # Podcast Index API
    podcast_index_key: str = ""
    podcast_index_secret: str = ""
    
    # Podping writer (Hive)
    hive_account: str = ""
    hive_posting_key: str = ""
    hive_dry_run: bool = True
    
    # Channel subscriptions: channel -> set of URLs
    channel_subscriptions: Dict[str, Set[str]] = field(default_factory=dict)
    
    # Bot behavior
    command_name: str = "ppwatch"
    allow_runtime_subscriptions: bool = False  # Default to secure
    authorized_users: Set[str] = field(default_factory=set)
    message_delay: float = 1.0
    
    # Timeouts for external services
    api_timeout: float = 10.0  # Timeout for API calls
    command_timeout: float = 30.0  # Max time for command execution


class PodpingIRCBot:
    """IRC bot that monitors podping and posts updates to channels."""
    
    def __init__(self, config: BotConfig):
        self.config = config
        self.bot = Client(
            host=config.irc_host,
            port=config.irc_port,
            secure=config.irc_secure,
            user=config.irc_user,
            realname=config.irc_realname,
            nick=config.irc_nick,
        )
        
        # Normalize all subscribed URLs once at startup
        self._normalized_subscriptions = self._normalize_subscriptions()
        
        # Optional clients
        self.podcast_index: Optional[PodcastIndexClient] = None
        self.podping_writer: Optional[PodpingWriter] = None
        self.watcher: Optional[PodpingWatcher] = None
        
        
        self._setup_handlers()
    
    def _normalize_subscriptions(self) -> Dict[str, Set[str]]:
        """Normalize all subscription URLs for faster matching."""
        normalized = {}
        for channel, urls in self.config.channel_subscriptions.items():
            normalized[channel] = {self._normalize_url(url) for url in urls}
        return normalized
    
    @staticmethod
    def _normalize_url(url: str) -> str:
        """Normalize URL for comparison."""
        url = url.lower().rstrip('/')
        if url.startswith('http://'):
            url = 'https://' + url[7:]
        return url

    @staticmethod
    def _encode_feed_url(url: str) -> str:
        """Percent-encode feed URL so it is a valid IRI for podping writes."""
        try:
            parts = urlsplit(url)
        except Exception:
            # If parsing fails, fall back to the original URL
            return url

        path = quote(parts.path, safe="/%")
        query = quote(parts.query, safe="=&%")
        fragment = quote(parts.fragment, safe="=%")

        return urlunsplit((parts.scheme, parts.netloc, path, query, fragment))
    
    
    def _is_authorized(self, nick: str) -> bool:
        """Check if user is authorized to manage subscriptions."""
        # Require explicit authorization if runtime subscriptions enabled
        return (self.config.allow_runtime_subscriptions 
                and nick in self.config.authorized_users)
    
    
    
    async def _get_podcast_info(self, url: str) -> Tuple[Optional[str], Optional[int]]:
        """Get podcast title and ID from Podcast Index with timeout."""
        if not self.podcast_index:
            return None, None
        
        try:
            # Wrap in timeout to prevent hanging
            async with asyncio_timeout(self.config.api_timeout):
                metadata = await self.podcast_index.lookup_by_feed_url(url)
                return metadata.title, metadata.id
        except asyncio.TimeoutError:
            logger.warning(f"Timeout fetching metadata for {url}")
            return None, None
        except Exception as e:
            logger.debug(f"Failed to fetch metadata for {url}: {e}")
            return None, None
    
    async def _check_live_item_status(self, feed_url: str) -> Optional[bool]:
        """
        Fetch feed and check if any liveItem tags have status="live".
        Returns True if live, False if not live, None if error/not found.
        """
        try:
            async with asyncio_timeout(self.config.api_timeout):
                async with httpx.AsyncClient(follow_redirects=True) as client:
                    response = await client.get(feed_url)
                    response.raise_for_status()

                    # Parse XML
                    root = ET.fromstring(response.text)

                    # Look for any liveItem tag with status="live"
                    # people seem to use different podcast xmlns urls so im just matching everything
                    live_items = root.findall('.//{*}liveItem')

                    for live_item in live_items:
                        status = live_item.get('status', '').lower()
                        if status == 'live':
                            logger.debug(f"Found liveItem with status='live' in {feed_url}")
                            return True
                    
                    logger.debug(f"No live items found in {feed_url}")
                    return False
                    
        except asyncio.TimeoutError:
            logger.warning(f"Timeout fetching feed {feed_url}")
            return None
        except Exception as e:
            logger.warning(f"Error checking live item status for {feed_url}: {e}")
            return None
    
    async def _verify_live_status(self, url: str, reason: str) -> Tuple[bool, str]:
        """
        Verify that the feed's liveItem status matches the podping reason.
        Returns (is_valid, message).
        """
        if reason not in ('live', 'liveEnd'):
            # No verification needed for other reasons
            return True, ""
        
        is_live = await self._check_live_item_status(url)
        error = None

        if is_live is None:
            warning = f"Warning: Could not verify liveItem status"
            logger.warning(f"{warning} for {url} (reason: {reason})")
            return True, warning  # Allow it but warn

        elif reason == 'live' and not is_live:
            error = f"Error: Feed has no liveItem with status='live' but reason is 'live'"
        
        elif reason == 'liveEnd' and is_live:
            error = f"Error: Feed has liveItem with status='live' but reason is 'liveEnd'"

        if error:
            logger.error(f"{error} for {url}")
            return False, error
        
        return True, None
    
    async def _format_podping_message(
        self, 
        url: str, 
        reason: str, 
        trx_id: Optional[str] = None
    ) -> str:
        """Format podping notification message."""
        title, _ = await self._get_podcast_info(url)
        podcast_name = title or "Unknown Podcast"
        
        msg = f"Podping received: {podcast_name} {url} ({reason}) (tx: {trx_id})"
        
        return msg
    
    async def _handle_podping(self, podping_data) -> None:
        """Handle incoming podping data."""
        logger.debug(
            f"Received podping with {len(podping_data.urls)} URL(s): {'; '.join(podping_data.urls)}"
        )
        
        # Group URLs by interested channel
        channel_urls: Dict[str, list[str]] = defaultdict(list)
        for url in podping_data.urls:
            normalized = self._normalize_url(url)
            for channel, urls in self._normalized_subscriptions.items():
                if normalized in urls:
                    channel_urls[channel].append(url)
        
        # Send messages to interested channels
        for channel, urls in channel_urls.items():
            # Find actual joined channel name (case-insensitive)
            channel_lower = channel.lower()
            target = None
            for ch in self.bot._channels:
                if ch.lower() == channel_lower:
                    target = ch
                    break
            
            if not target:
                logger.warning(f"Not in channel {channel}, skipping notifications")
                continue
            
            for url in urls:
                # First, announce the podping
                msg = await self._format_podping_message(
                    url, 
                    podping_data.reason, 
                    podping_data.trx_id
                )
                await self.bot.message(target, msg)
                await asyncio.sleep(self.config.message_delay)
                
                # Then verify live/liveEnd status if applicable
                if podping_data.reason in ('live', 'liveEnd'):
                    is_valid, message = await self._verify_live_status(url, podping_data.reason)
                    if not is_valid or message:
                        # Send follow-up message with status check result
                        await self.bot.message(target, f"  → {message}")
                        await asyncio.sleep(self.config.message_delay)
    
    async def _send_message(self, nick: str, text: str) -> None:
        """Send message with error handling."""
        try:
            await self.bot.message(nick, text)
        except Exception as e:
            logger.error(f"Failed to send message to {nick}: {e}")
    
    async def _handle_help(self, nick: str) -> None:
        """Send help message."""
        cmd = self.config.command_name
        await self._send_message(nick, f"=== {cmd.upper()} Bot Commands ===")
        await self._send_message(nick, f"  help - Show this help")
        await self._send_message(nick, f"  list - Show all subscriptions")
        await self._send_message(nick, f"  subscribe <channel> <url> - Subscribe to updates")
        await self._send_message(nick, f"  unsubscribe <channel> <url> - Unsubscribe")
        await self._send_message(nick, f"  pp <feed_id> [reason] - Write podping to Hive")
        await self._send_message(nick, f"    Valid reasons: live, liveEnd, update (default: update)")
    
    async def _handle_list(self, nick: str, channel: Optional[str] = None) -> None:
        """List subscriptions (specific channel or all)."""
        if channel:
            # List for specific channel
            subs = self.config.channel_subscriptions.get(channel, set())
            if not subs:
                await self._send_message(nick, f"No subscriptions for {channel}")
            else:
                await self._send_message(nick, f"Monitoring {len(subs)} feed(s) for {channel}:")
                for url in sorted(subs):
                    await self._send_message(nick, f"  {url}")
        else:
            # List all channels
            if not self.config.channel_subscriptions:
                await self._send_message(nick, "No subscriptions configured")
            else:
                total = len(self.config.channel_subscriptions)
                await self._send_message(nick, f"Subscriptions ({total} channels):")
                for ch, subs in self.config.channel_subscriptions.items():
                    await self._send_message(nick, f"  {ch}: {len(subs)} feed(s)")
                    for url in sorted(subs):
                        await self._send_message(nick, f"    {url}")
    
    async def _handle_subscribe(
        self, 
        nick: str, 
        channel: str, 
        url: str
    ) -> None:
        """Subscribe channel to URL."""
        if not self._is_authorized(nick):
            await self._send_message(nick, "Unauthorized: subscriptions disabled or user not authorized")
            logger.warning(f"Unauthorized subscribe attempt by {nick}")
            return
        
        # Add to subscriptions
        if channel not in self.config.channel_subscriptions:
            self.config.channel_subscriptions[channel] = set()
        
        if url in self.config.channel_subscriptions[channel]:
            await self._send_message(nick, f"Already monitoring {url} in {channel}")
        else:
            self.config.channel_subscriptions[channel].add(url)
            # Update normalized cache
            if channel not in self._normalized_subscriptions:
                self._normalized_subscriptions[channel] = set()
            self._normalized_subscriptions[channel].add(self._normalize_url(url))
            
            await self._send_message(nick, f"Now monitoring {url} in {channel}")
            logger.info(f"{channel} subscribed to {url} (by {nick})")
    
    async def _handle_unsubscribe(
        self, 
        nick: str, 
        channel: str, 
        url: str
    ) -> None:
        """Unsubscribe channel from URL."""
        if not self._is_authorized(nick):
            await self._send_message(nick, "Unauthorized: subscriptions disabled or user not authorized")
            logger.warning(f"Unauthorized unsubscribe attempt by {nick}")
            return
        
        if channel not in self.config.channel_subscriptions:
            await self._send_message(nick, f"No subscriptions for {channel}")
            return
        
        if url not in self.config.channel_subscriptions[channel]:
            await self._send_message(nick, f"Not monitoring {url} in {channel}")
            return
        
        # Remove from subscriptions
        self.config.channel_subscriptions[channel].remove(url)
        # Update normalized cache
        if channel in self._normalized_subscriptions:
            self._normalized_subscriptions[channel].discard(self._normalize_url(url))
        
        await self._send_message(nick, f"Stopped monitoring {url} in {channel}")
        logger.info(f"{channel} unsubscribed from {url} (by {nick})")
    
    async def _handle_pp(self, nick: str, feed_id_str: str, reason: Optional[str] = None, channel: Optional[str] = None) -> None:
        """Handle !pp command with timeout and error handling."""
        # Determine target for response (channel or user)
        target = channel if channel else nick
        
        # Send immediate feedback that we're processing the command
        await self._send_message(target, f"Sending podping for feed ID {feed_id_str}...")
        
        # Validate prerequisites
        if not self.podcast_index:
            await self._send_message(target, "Error: Podcast Index not configured")
            return
        
        if not self.podping_writer:
            await self._send_message(target, "Error: Podping writer not configured")
            return
        
        # Validate feed ID
        try:
            feed_id = int(feed_id_str)
        except ValueError:
            await self._send_message(target, f"Error: Invalid feed ID '{feed_id_str}' (must be a number)")
            return
        
        # Validate reason if provided
        valid_reasons = {"live", "liveEnd", "update"}
        if reason and reason not in valid_reasons:
            await self._send_message(target, f"Error: Invalid reason '{reason}'. Valid reasons are: {', '.join(sorted(valid_reasons))}")
            return
        
        try:
            # Look up feed with timeout
            logger.info(f"Looking up feed {feed_id} for {nick}")
            async with asyncio_timeout(self.config.api_timeout):
                metadata = await self.podcast_index.lookup_by_feed_id(feed_id)
            
            if not metadata:
                await self._send_message(target, f"Error: Feed ID {feed_id} not found in Podcast Index")
                return
            
            # Write podping with timeout
            reason = reason or "update"  # Ensure reason is not None
            safe_url = self._encode_feed_url(metadata.url)
            if safe_url != metadata.url:
                logger.debug(f"Percent-encoding feed URL for podping: {metadata.url} -> {safe_url}")

            logger.info(f"Writing podping for feed {feed_id}: {safe_url} (reason: {reason})")
            result = await self.podping_writer.post(safe_url, reason=reason)
            rc_percent = await self.podping_writer.get_credits()
            rc_used = 100 - rc_percent if rc_percent is not None else None

            # Format success response
            reason_display = reason or "update"
            tx_id = result["tx_id"]
            tx_url = f"https://hive.ausbit.dev/tx/{tx_id}"
            rc_info = f" rc used: {rc_used:.1f}%" if rc_used is not None else ""
            
            msg = f"Podping sent: {metadata.title} {safe_url} ({reason_display}) (tx: {tx_url}{rc_info})"
            logger.info(f"Podping sent by {nick} for feed {feed_id}: tx {tx_id} (reason: {reason_display}){rc_info}")
        
            await self._send_message(target, msg)
            
            # Verify live/liveEnd status after sending (if applicable)
            if reason in ('live', 'liveEnd'):
                is_valid, message = await self._verify_live_status(safe_url, reason)
                if not is_valid or message:
                    await self._send_message(target, f"  → {message}")
            
        except asyncio.TimeoutError:
            error_msg = f"Error: Timeout writing podping for feed {feed_id} (try again later)"
            logger.error(f"Timeout in pp command by {nick} for feed {feed_id}")
            await self._send_message(target, error_msg)
            
        except Exception as e:
            error_msg = f"Error: Failed to write podping for feed {feed_id}: {str(e)}"
            logger.error(f"Error in pp command by {nick}: {e}", exc_info=True)
            await self._send_message(target, error_msg)
    
    def _setup_handlers(self) -> None:
        """Setup IRC event handlers."""
        
        @self.bot.on_connected()
        async def on_connected():
            logger.info(f"Connected to {self.config.irc_host}")
            
            # Identify with NickServ if configured
            if self.config.irc_nickserv_password:
                logger.info("Identifying with NickServ...")
                nickserv_ok = self.bot.await_message(
                    sender="NickServ", 
                    message=re.compile("Password accepted|identified")
                )
                await self.bot.message(
                    "NickServ", 
                    f"IDENTIFY {self.config.irc_nickserv_password}"
                )
                await nickserv_ok
            
            # Join subscribed channels
            for channel in self.config.channel_subscriptions:
                logger.info(f"Joining {channel}")
                await self.bot.join(channel)
        
        # Channel commands: !ppwatch and !pp
        @self.bot.on_message(
            matcher=lambda msg: (
                isinstance(msg.recipient, Channel) 
                and (msg.text.startswith(f"!{self.config.command_name}") 
                     or msg.text.startswith("!pp"))
            )
        )
        async def on_channel_command(message):
            # Parse command (remove ! prefix for channel commands)
            text = message.text[1:] if message.text.startswith('!') else message.text
            parts = text.split()
            if not parts:
                return
            
            nick = message.sender.name
            channel = message.recipient.name
            cmd = parts[0]
            
            # Route to appropriate handler
            if cmd == self.config.command_name:
                await self._route_ppwatch_command(nick, parts[1:], channel)
            elif cmd == "pp" and len(parts) >= 2:
                reason = parts[2] if len(parts) >= 3 else None
                await self._handle_pp(nick, parts[1], reason, channel)
            elif cmd == "pp":
                await self._send_message(channel, "Usage: !pp <feed_id> [reason] (valid reasons: live, liveEnd, update)")
        
        # Private message commands (no ! prefix)
        @self.bot.on_message(
            matcher=lambda msg: not isinstance(msg.recipient, Channel)
        )
        async def on_private_command(message):
            parts = message.text.split()
            if not parts:
                return
            
            nick = message.sender.name
            await self._route_ppwatch_command(nick, parts, channel=None)
    
    async def _route_ppwatch_command(
        self, 
        nick: str, 
        parts: list[str], 
        channel: Optional[str]
    ) -> None:
        """Route ppwatch command to appropriate handler."""
        if not parts or parts[0] == "help":
            await self._handle_help(nick)
        
        elif parts[0] == "list":
            await self._handle_list(nick, channel)
        
        elif parts[0] == "subscribe":
            if len(parts) < 2:
                await self._send_message(nick, "Usage: subscribe <channel> <url>")
            elif len(parts) < 3:
                # Channel context or missing URL
                if channel:
                    await self._send_message(nick, "Usage: subscribe <url>")
                else:
                    await self._send_message(nick, "Usage: subscribe <channel> <url>")
            else:
                target_channel = channel or parts[1]
                url = parts[2] if channel else parts[2]
                url_arg = parts[1] if channel else url
                await self._handle_subscribe(nick, target_channel, url_arg)
        
        elif parts[0] == "unsubscribe":
            if len(parts) < 3 and not channel:
                await self._send_message(nick, "Usage: unsubscribe <channel> <url>")
            elif len(parts) < 2:
                await self._send_message(nick, "Usage: unsubscribe <url>")
            else:
                target_channel = channel or parts[1]
                url_arg = parts[1] if channel else parts[2]
                await self._handle_unsubscribe(nick, target_channel, url_arg)
        
        elif parts[0] == "pp":
            if len(parts) < 2:
                await self._send_message(nick, "Usage: pp <feed_id> [reason] (valid reasons: live, liveEnd, update)")
            else:
                reason = parts[2] if len(parts) >= 3 else None
                await self._handle_pp(nick, parts[1], reason, None)
    
    async def _start_watcher(self) -> None:
        """Start the podping watcher."""
        # Initialize Podcast Index
        if self.config.podcast_index_key and self.config.podcast_index_secret:
            self.podcast_index = PodcastIndexClient(
                api_key=self.config.podcast_index_key,
                api_secret=self.config.podcast_index_secret
            )
            logger.info("Podcast Index client initialized")
        else:
            logger.warning("Podcast Index not configured - metadata unavailable")
        
        # Initialize Podping writer
        if self.config.hive_account and self.config.hive_posting_key:
            try:
                self.podping_writer = PodpingWriter(
                    account=self.config.hive_account,
                    posting_key=self.config.hive_posting_key,
                    dry_run=self.config.hive_dry_run
                )
                print(repr(self.podping_writer))
                logger.info(f"Podping writer initialized (dry_run={self.config.hive_dry_run})")
            except Exception as e:
                logger.error(f"Error initializing podping writer: {e}")
        else:
            logger.warning("Podping writer not configured - !pp command unavailable")
        
        # Start watcher
        self.watcher = PodpingWatcher()
        
        @self.watcher.on_update
        async def handle_update(podping_data):
            await self._handle_podping(podping_data)
        
        logger.info("Starting podping watcher...")
        await self.watcher.start()
    
    async def run(self) -> None:
        """Run the bot."""
        # Start watcher as background task
        watcher_task = asyncio.create_task(self._start_watcher())
        
        try:
            await self.bot.run()
        finally:
            # Cancel watcher
            watcher_task.cancel()
            try:
                await watcher_task
            except asyncio.CancelledError:
                pass
            


def load_config(config_path: Path) -> BotConfig:
    """Load configuration from JSON file."""
    if not config_path.exists():
        raise FileNotFoundError(f"Config file not found: {config_path}")
    
    with open(config_path) as f:
        data = json.load(f)
    
    # IRC settings
    irc = data.get("irc", {})
    
    # Podcast Index settings
    pi = data.get("podcast_index", {})
    
    # Podping writer settings
    pw = data.get("podping_writer", {})
    
    # Channel subscriptions (convert lists to sets)
    subs = data.get("channel_subscriptions", {})
    channel_subs = {
        ch: set(urls if isinstance(urls, list) else [urls])
        for ch, urls in subs.items()
    }
    
    # Authorized users (convert to set)
    auth_users = set(data.get("authorized_users", []))
    
    return BotConfig(
        # IRC
        irc_host=irc.get("host", "irc.libera.chat"),
        irc_port=irc.get("port", 6667),
        irc_nick=irc.get("nick", "podping-bot"),
        irc_user=irc.get("user", "podping"),
        irc_realname=irc.get("realname", "Podping RSS Update Bot"),
        irc_secure=irc.get("secure", False),
        irc_nickserv_password=irc.get("nickserv_password"),
        # Podcast Index
        podcast_index_key=pi.get("api_key", ""),
        podcast_index_secret=pi.get("api_secret", ""),
        # Podping writer
        hive_account=pw.get("hive_account", ""),
        hive_posting_key=pw.get("hive_posting_key", ""),
        hive_dry_run=pw.get("dry_run", True),
        # Subscriptions
        channel_subscriptions=channel_subs,
        # Bot behavior
        command_name=data.get("command_name", "ppwatch"),
        allow_runtime_subscriptions=data.get("allow_runtime_subscriptions", False),
        authorized_users=auth_users,
        message_delay=data.get("message_delay", 1.0),
        api_timeout=data.get("api_timeout", 10.0),
        command_timeout=data.get("command_timeout", 30.0),
    )


async def main() -> None:
    """Main entry point."""
    parser = argparse.ArgumentParser(
        description="IRC Podping Bot - Posts podping updates to IRC channels"
    )
    parser.add_argument(
        "--config", "-c",
        type=Path,
        required=True,
        help="Path to configuration file"
    )
    args = parser.parse_args()
    
    try:
        config = load_config(args.config)
        bot = PodpingIRCBot(config)
        await bot.run()
    except KeyboardInterrupt:
        logger.info("Shutting down...")
    except Exception as e:
        logger.error(f"Fatal error: {e}", exc_info=True)
        raise


if __name__ == "__main__":
    asyncio.run(main())
