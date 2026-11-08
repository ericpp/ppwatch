package config

import (
	"encoding/json"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := FromMap(map[string]any{})
	if cfg.IrcHost != "irc.libera.chat" || cfg.IrcPort != 6667 || cfg.IrcNick != "podping-bot" {
		t.Errorf("bad defaults: %+v", cfg)
	}
	if cfg.IrcUser != "podping" || cfg.IrcRealname != "Podping RSS Update Bot" {
		t.Errorf("bad irc defaults: %+v", cfg)
	}
	if !cfg.HiveDryRun || !cfg.DebugLogging {
		t.Errorf("dry_run/debug defaults wrong: %v/%v", cfg.HiveDryRun, cfg.DebugLogging)
	}
	if cfg.MessageDelay != 1.0 || cfg.APITimeout != 10.0 || cfg.CommandName != "ppwatch" {
		t.Errorf("behavior defaults wrong: %+v", cfg)
	}
	if cfg.UserAgentEmail != "user@email.com" {
		t.Errorf("user agent default wrong: %s", cfg.UserAgentEmail)
	}
}

func TestFromMapFullMapping(t *testing.T) {
	raw := `{
	  "irc": {"host": "irc.zeronode.net", "port": 6697, "nick": "ppwatch2", "user": "ppwatch2",
	          "realname": "Podping Watcher", "secure": true, "nickserv_password": "pw"},
	  "podcast_index": {"api_key": "K", "api_secret": "S"},
	  "podping_writer": {"hive_account": "acct", "hive_posting_key": "key",
	                     "hive_nodes": ["https://a", "https://b"], "dry_run": false},
	  "channel_subscriptions": {"#ppwatch": ["https://ericpp.github.io/rss/test.xml"],
	                            "#solo": "https://single.example/rss.xml"},
	  "command_name": "ppwatch",
	  "message_delay": 2.5,
	  "api_timeout": 20,
	  "debug_logging": false,
	  "feed_aliases": {"BTS": 150842, "na": 41504}
	}`
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}
	cfg := FromMap(data)

	if cfg.IrcHost != "irc.zeronode.net" || cfg.IrcPort != 6697 || !cfg.IrcSecure || cfg.IrcNickservPassword != "pw" {
		t.Errorf("irc mapping wrong: %+v", cfg)
	}
	if cfg.PodcastIndexKey != "K" || cfg.PodcastIndexSecret != "S" {
		t.Errorf("pi mapping wrong")
	}
	if cfg.HiveAccount != "acct" || cfg.HivePostingKey != "key" || cfg.HiveDryRun {
		t.Errorf("writer mapping wrong: %+v", cfg)
	}
	if len(cfg.HiveNodes) != 2 || cfg.HiveNodes[0] != "https://a" {
		t.Errorf("hive nodes wrong: %v", cfg.HiveNodes)
	}
	if len(cfg.ChannelSubscriptions) != 2 {
		t.Fatalf("subscriptions wrong: %v", cfg.ChannelSubscriptions)
	}
	if !cfg.ChannelSubscriptions["#ppwatch"]["https://ericpp.github.io/rss/test.xml"] {
		t.Errorf("list subscription not mapped")
	}
	if !cfg.ChannelSubscriptions["#solo"]["https://single.example/rss.xml"] {
		t.Errorf("single-string subscription not mapped")
	}
	if cfg.MessageDelay != 2.5 || cfg.APITimeout != 20 {
		t.Errorf("delay/timeout wrong: %v %v", cfg.MessageDelay, cfg.APITimeout)
	}
	if cfg.FeedAliases["bts"] != 150842 || cfg.FeedAliases["na"] != 41504 {
		t.Errorf("aliases wrong: %v (keys must be lowercased)", cfg.FeedAliases)
	}
	if cfg.DebugLogging {
		t.Errorf("debug_logging should be false")
	}
}
