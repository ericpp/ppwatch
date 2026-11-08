// Package config loads the ppwatch bot configuration from JSON.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// BotConfig is the complete bot configuration.
type BotConfig struct {
	// IRC settings
	IrcHost             string
	IrcPort             int
	IrcNick             string
	IrcUser             string
	IrcRealname         string
	IrcSecure           bool
	IrcNickservPassword string

	// Podcast Index API
	PodcastIndexKey    string
	PodcastIndexSecret string

	// Podping writer (Hive)
	HiveAccount    string
	HivePostingKey string
	HiveNodes      []string
	HiveDryRun     bool

	// Channel subscriptions: channel -> set of URLs
	ChannelSubscriptions map[string]map[string]bool

	// Bot behavior
	CommandName    string
	MessageDelay   float64
	APITimeout     float64
	UserAgentEmail string
	DebugLogging   bool

	// Feed aliases: name -> feed_id (e.g. {"bts": 150842})
	FeedAliases map[string]int64
}

// Load reads and parses the configuration file.
func Load(path string) (*BotConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config file not found: %s", path)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("invalid config JSON: %v", err)
	}
	return FromMap(raw), nil
}

// FromMap builds the config from a parsed JSON map. Only keys present in
// the data override the defaults.
func FromMap(data map[string]any) *BotConfig {
	irc := asSection(data, "irc")
	pi := asSection(data, "podcast_index")
	pw := asSection(data, "podping_writer")

	cfg := &BotConfig{
		IrcHost:              "irc.libera.chat",
		IrcPort:              6667,
		IrcNick:              "podping-bot",
		IrcUser:              "podping",
		IrcRealname:          "Podping RSS Update Bot",
		IrcSecure:            false,
		IrcNickservPassword:  "",
		PodcastIndexKey:      "",
		PodcastIndexSecret:   "",
		HiveAccount:          "",
		HivePostingKey:       "",
		HiveNodes:            []string{},
		HiveDryRun:           true,
		ChannelSubscriptions: map[string]map[string]bool{},
		CommandName:          "ppwatch",
		MessageDelay:         1.0,
		APITimeout:           10.0,
		UserAgentEmail:       "user@email.com",
		DebugLogging:         true,
		FeedAliases:          map[string]int64{},
	}

	setStr(irc, "host", &cfg.IrcHost)
	setInt(irc, "port", &cfg.IrcPort)
	setStr(irc, "nick", &cfg.IrcNick)
	setStr(irc, "user", &cfg.IrcUser)
	setStr(irc, "realname", &cfg.IrcRealname)
	setBool(irc, "secure", &cfg.IrcSecure)
	setStr(irc, "nickserv_password", &cfg.IrcNickservPassword)

	setStr(pi, "api_key", &cfg.PodcastIndexKey)
	setStr(pi, "api_secret", &cfg.PodcastIndexSecret)

	setStr(pw, "hive_account", &cfg.HiveAccount)
	setStr(pw, "hive_posting_key", &cfg.HivePostingKey)
	if nodes, ok := stringSlice(pw["hive_nodes"]); ok {
		cfg.HiveNodes = nodes
	}
	setBool(pw, "dry_run", &cfg.HiveDryRun)

	setStr(data, "command_name", &cfg.CommandName)
	setFloat(data, "message_delay", &cfg.MessageDelay)
	setFloat(data, "api_timeout", &cfg.APITimeout)
	setStr(data, "user_agent_email", &cfg.UserAgentEmail)
	setBool(data, "debug_logging", &cfg.DebugLogging)

	if subs, ok := section(data, "channel_subscriptions"); ok {
		for ch, v := range subs {
			set := map[string]bool{}
			switch urls := v.(type) {
			case []any:
				for _, u := range urls {
					if s, ok := u.(string); ok && s != "" {
						set[s] = true
					}
				}
			case string:
				if urls != "" {
					set[urls] = true
				}
			}
			cfg.ChannelSubscriptions[ch] = set
		}
	}

	if aliases, ok := section(data, "feed_aliases"); ok {
		for name, v := range aliases {
			if id, ok := toInt(v); ok {
				cfg.FeedAliases[strings.ToLower(name)] = id
			}
		}
	}

	return cfg
}

func section(data map[string]any, key string) (map[string]any, bool) {
	v, ok := data[key]
	if !ok {
		return nil, false
	}
	s, ok := v.(map[string]any)
	return s, ok
}

// asSection returns the sub-map for key, or nil when absent/wrong type.
func asSection(data map[string]any, key string) map[string]any {
	if s, ok := section(data, key); ok {
		return s
	}
	return nil
}

func setStr(section map[string]any, key string, dst *string) {
	if section == nil {
		return
	}
	if s, ok := section[key].(string); ok {
		*dst = s
	}
}

func setInt(section map[string]any, key string, dst *int) {
	if section == nil {
		return
	}
	if v, ok := toInt(section[key]); ok {
		*dst = int(v)
	}
}

func setFloat(section map[string]any, key string, dst *float64) {
	if section == nil {
		return
	}
	switch v := section[key].(type) {
	case float64:
		*dst = v
	case int:
		*dst = float64(v)
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			*dst = f
		}
	}
}

func setBool(section map[string]any, key string, dst *bool) {
	if section == nil {
		return
	}
	if b, ok := section[key].(bool); ok {
		*dst = b
	}
}

func stringSlice(v any) ([]string, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out, true
}

func toInt(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}
