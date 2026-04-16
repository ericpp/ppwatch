#!/usr/bin/env python3
"""
IRC Podping Bot - Posts podping updates to IRC channels
"""

import argparse
import asyncio
import json
import logging
import re
from collections import defaultdict
from dataclasses import dataclass, field
from pathlib import Path
from typing import Dict, List, Optional, Set, Tuple
from xml.etree import ElementTree as ET

import httpx
from asif.bot import Client, Channel
from pypodping import PodpingWatcher, PodpingWriter, PodpingError
from pypodping.errors import PodpingNetworkError
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

VALID_PP_REASONS = {"live", "liveEnd", "update"}


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
    hive_nodes: List[str] = field(default_factory=list)
    hive_dry_run: bool = True

    # Channel subscriptions: channel -> set of URLs
    channel_subscriptions: Dict[str, Set[str]] = field(default_factory=dict)

    # Bot behavior
    command_name: str = "ppwatch"
    message_delay: float = 1.0
    api_timeout: float = 10.0
    user_agent_email: str = "user@email.com"

    # Feed aliases: name -> feed_id (e.g. {"bts": 150842})
    feed_aliases: Dict[str, int] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, data: dict) -> 'BotConfig':
        """Build config from a parsed JSON dict.

        Only keys present in the data override the dataclass defaults.
        """
        irc = data.get("irc", {})
        pi = data.get("podcast_index", {})
        pw = data.get("podping_writer", {})

        kwargs = {}
        for field_name, section, key in [
            ("irc_host", irc, "host"),
            ("irc_port", irc, "port"),
            ("irc_nick", irc, "nick"),
            ("irc_user", irc, "user"),
            ("irc_realname", irc, "realname"),
            ("irc_secure", irc, "secure"),
            ("irc_nickserv_password", irc, "nickserv_password"),
            ("podcast_index_key", pi, "api_key"),
            ("podcast_index_secret", pi, "api_secret"),
            ("hive_account", pw, "hive_account"),
            ("hive_posting_key", pw, "hive_posting_key"),
            ("hive_nodes", pw, "hive_nodes"),
            ("hive_dry_run", pw, "dry_run"),
            ("command_name", data, "command_name"),
            ("message_delay", data, "message_delay"),
            ("api_timeout", data, "api_timeout"),
            ("user_agent_email", data, "user_agent_email"),
        ]:
            if key in section:
                kwargs[field_name] = section[key]

        subs = data.get("channel_subscriptions", {})
        if subs:
            kwargs["channel_subscriptions"] = {
                ch: set(urls if isinstance(urls, list) else [urls])
                for ch, urls in subs.items()
            }

        raw_aliases = data.get("feed_aliases", {})
        if raw_aliases:
            kwargs["feed_aliases"] = {k.lower(): int(v) for k, v in raw_aliases.items()}

        return cls(**kwargs)


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

        self._normalized_subscriptions = self._normalize_subscriptions()
        self._joined_channels: Set[str] = set()
        self._http_client: Optional[httpx.AsyncClient] = None

        self.podcast_index: Optional[PodcastIndexClient] = None
        self.podping_writer: Optional[PodpingWriter] = None
        self.watcher: Optional[PodpingWatcher] = None

        self._setup_handlers()

    def _normalize_subscriptions(self) -> Dict[str, Set[str]]:
        """Normalize all subscription URLs for faster matching."""
        return {
            channel: {self._normalize_url(url) for url in urls}
            for channel, urls in self.config.channel_subscriptions.items()
        }

    @staticmethod
    def _normalize_url(url: str) -> str:
        """Normalize URL for comparison."""
        url = url.lower().rstrip('/')
        if url.startswith('http://'):
            url = 'https://' + url[7:]
        return url

    async def _get_http_client(self) -> httpx.AsyncClient:
        if self._http_client is None:
            self._http_client = httpx.AsyncClient(
                follow_redirects=True,
                headers={"User-Agent": f"PodPing Watch/{self.config.user_agent_email}"}
            )
        return self._http_client

    async def _close_http_client(self) -> None:
        if self._http_client:
            await self._http_client.aclose()
            self._http_client = None

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

    async def _check_live_item_status(self, feed_url: str) -> Tuple[Optional[bool], Optional[str]]:
        """Fetch feed and check if any liveItem tags have status="live".
        Returns (is_live, error_message)."""
        try:
            client = await self._get_http_client()
            async with asyncio_timeout(self.config.api_timeout):
                response = await client.get(feed_url)
                response.raise_for_status()

                root = ET.fromstring(response.text)

                # Match all xmlns variants for liveItem
                for live_item in root.findall('.//{*}liveItem'):
                    if live_item.get('status', '').lower() == 'live':
                        logger.debug(f"Found liveItem with status='live' in {feed_url}")
                        return True, None

                logger.debug(f"No live items found in {feed_url}")
                return False, None

        except asyncio.TimeoutError:
            error_msg = f"Timeout fetching feed {feed_url}"
            logger.warning(error_msg)
            return None, error_msg
        except httpx.HTTPStatusError as e:
            error_msg = f"HTTP {e.response.status_code} {e.response.reason_phrase} fetching feed {feed_url}"
            logger.warning(error_msg)
            return None, error_msg
        except httpx.RequestError as e:
            error_msg = f"Request error fetching feed {feed_url}: {e}"
            logger.warning(error_msg)
            return None, error_msg
        except Exception as e:
            error_msg = f"Error checking live item status for {feed_url}: {e}"
            logger.warning(error_msg)
            return None, error_msg

    async def _verify_live_status(self, url: str, reason: str) -> Tuple[bool, Optional[str]]:
        """Verify that the feed's liveItem status matches the podping reason.
        Returns (is_valid, message)."""
        if reason not in ('live', 'liveEnd'):
            return True, None

        is_live, error_msg = await self._check_live_item_status(url)

        if is_live is None:
            warning = "Warning: Could not verify liveItem status"
            if error_msg:
                warning += f" - {error_msg}"
            logger.warning(f"{warning} for {url} (reason: {reason})")
            return True, warning

        if reason == 'live' and not is_live:
            error = "Error: Feed has no liveItem with status='live' but reason is 'live'"
            logger.error(f"{error} for {url}")
            return False, error

        if reason == 'liveEnd' and is_live:
            error = "Error: Feed has liveItem with status='live' but reason is 'liveEnd'"
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

        channel_urls: Dict[str, List[str]] = defaultdict(list)
        for url in podping_data.urls:
            normalized = self._normalize_url(url)
            for channel, urls in self._normalized_subscriptions.items():
                if normalized in urls:
                    channel_urls[channel].append(url)

        for channel, urls in channel_urls.items():
            if channel not in self._joined_channels:
                logger.warning(f"Not in channel {channel}, skipping notifications")
                continue

            for url in urls:
                msg = await self._format_podping_message(
                    url, podping_data.reason, podping_data.trx_id
                )
                await self._send_message(channel, msg)
                await asyncio.sleep(self.config.message_delay)

                if podping_data.reason in ('live', 'liveEnd'):
                    is_valid, message = await self._verify_live_status(url, podping_data.reason)
                    if not is_valid or message:
                        await self._send_message(channel, f"  → {message}")
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
        await self._send_message(nick, f"  pp <feed_id_or_alias> [reason] - Write podping to Hive")
        await self._send_message(nick, f"    Valid reasons: live, liveEnd, update (default: update)")
        if self.config.feed_aliases:
            aliases = ", ".join(f"{name}={fid}" for name, fid in sorted(self.config.feed_aliases.items()))
            await self._send_message(nick, f"    Aliases: {aliases}")

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

    def _resolve_feed_alias(self, feed_id_str: str) -> Tuple[str, Optional[str]]:
        """Resolve a feed alias to its numeric ID.
        Returns (resolved_id_str, alias_name_or_None)."""
        alias_key = feed_id_str.lower()
        if alias_key in self.config.feed_aliases:
            return str(self.config.feed_aliases[alias_key]), feed_id_str
        return feed_id_str, None

    async def _write_podping(self, feed_id: int, reason: str, nick: str) -> Tuple[str, str, str]:
        """Look up feed and write podping to Hive.
        Returns (result_message, feed_url, reason).
        Raises ValueError if the feed is not found."""
        async with asyncio_timeout(self.config.api_timeout):
            metadata = await self.podcast_index.lookup_by_feed_id(feed_id)

        if not metadata:
            raise ValueError(f"Feed ID {feed_id} not found in Podcast Index")

        logger.info(f"Writing podping for feed {feed_id}: {metadata.url} (reason: {reason})")
        result = await self.podping_writer.post(metadata.url, reason=reason)
        rc_percent = await self.podping_writer.get_credits()
        rc_used = 100 - rc_percent if rc_percent is not None else None

        tx_id = result["tx_id"]
        tx_url = f"https://hive.ausbit.dev/tx/{tx_id}"
        rc_info = f" rc used: {rc_used:.1f}%" if rc_used is not None else ""

        msg = f"Podping sent: {metadata.title} {metadata.url} ({reason}) (tx: {tx_url}{rc_info})"
        logger.info(f"Podping sent by {nick} for feed {feed_id}: tx {tx_id} (reason: {reason}){rc_info}")

        return msg, metadata.url, reason

    async def _handle_pp(self, nick: str, feed_id_str: str, reason: Optional[str] = None, channel: Optional[str] = None) -> None:
        """Handle !pp command."""
        target = channel or nick

        feed_id_str, alias_used = self._resolve_feed_alias(feed_id_str)
        if alias_used:
            await self._send_message(target, f"Sending podping for {alias_used} (feed ID {feed_id_str})...")
        else:
            await self._send_message(target, f"Sending podping for feed ID {feed_id_str}...")

        if not self.podcast_index:
            await self._send_message(target, "Error: Podcast Index not configured")
            return

        if not self.podping_writer:
            await self._send_message(target, "Error: Podping writer not configured")
            return

        try:
            feed_id = int(feed_id_str)
        except ValueError:
            await self._send_message(target, f"Error: Invalid feed ID '{feed_id_str}' (must be a number or alias)")
            return

        if reason and reason not in VALID_PP_REASONS:
            await self._send_message(target, f"Error: Invalid reason '{reason}'. Valid reasons are: {', '.join(sorted(VALID_PP_REASONS))}")
            return

        try:
            msg, feed_url, reason = await self._write_podping(feed_id, reason or "update", nick)
            await self._send_message(target, msg)

            if reason in ('live', 'liveEnd'):
                is_valid, message = await self._verify_live_status(feed_url, reason)
                if not is_valid or message:
                    await self._send_message(target, f"  → {message}")

        except ValueError as e:
            await self._send_message(target, f"Error: {e}")

        except asyncio.TimeoutError:
            logger.error(f"Timeout in pp command by {nick} for feed {feed_id}")
            await self._send_message(target, f"Error: Timeout writing podping for feed {feed_id} (try again later)")

        except PodpingNetworkError as e:
            logger.warning(f"Network error in pp command by {nick} for feed {feed_id}: {e}")
            await self._send_message(target, f"Error: Hive network error for feed {feed_id} - please try again in a moment")

        except PodpingError as e:
            logger.warning(f"Podping error in pp command by {nick} for feed {feed_id}: {e}")
            await self._send_message(target, f"Error: Failed to write podping for feed {feed_id}: {e}")

        except Exception as e:
            logger.error(f"Unexpected error in pp command by {nick}: {e}", exc_info=True)
            await self._send_message(target, f"Error: Unexpected error writing podping for feed {feed_id}: {e}")

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

            for channel in self.config.channel_subscriptions:
                logger.info(f"Joining {channel}")
                await self.bot.join(channel)
                self._joined_channels.add(channel)

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
                await self._send_message(channel, "Usage: !pp <feed_id_or_alias> [reason] (valid reasons: live, liveEnd, update)")

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
        parts: List[str],
        channel: Optional[str]
    ) -> None:
        """Route ppwatch command to appropriate handler."""
        if not parts or parts[0] == "help":
            await self._handle_help(nick)

        elif parts[0] == "list":
            await self._handle_list(nick, channel)

        elif parts[0] == "pp":
            if len(parts) < 2:
                await self._send_message(nick, "Usage: pp <feed_id_or_alias> [reason] (valid reasons: live, liveEnd, update)")
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
                    nodes=self.config.hive_nodes,
                    dry_run=self.config.hive_dry_run
                )
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
        watcher_task = asyncio.create_task(self._start_watcher())

        try:
            await self.bot.run()
        finally:
            watcher_task.cancel()
            try:
                await watcher_task
            except asyncio.CancelledError:
                pass
            await self._close_http_client()


def load_config(config_path: Path) -> BotConfig:
    """Load configuration from JSON file."""
    if not config_path.exists():
        raise FileNotFoundError(f"Config file not found: {config_path}")

    with open(config_path) as f:
        return BotConfig.from_dict(json.load(f))


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
