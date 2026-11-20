package fcmassist

import (
	"encoding/json"
	"strings"
)

// ChannelRule is one entry in a tenant's apps.fcm_channel_rules array — maps a notification to an
// Android notification channel + priority based on its url/title. Rules are evaluated in order,
// first match wins (like firewall rules), via ResolveChannel. Within one rule, every condition
// that's set must match (AND); a rule with no conditions at all never matches (it would otherwise
// swallow everything before it, almost certainly a config mistake).
//
// A rule's own small app (icon, sound, vibration) is never configured here — that's the Android
// app's own notification channel definition, keyed by ChannelID. PushHub only decides which
// channel a given notification belongs to.
type ChannelRule struct {
	// URLPrefix matches when data.url equals this value, or starts with this value + "/" (so
	// "/driver" matches both "/driver" and "/driver/123"). Mirrors the riwariwi contract's
	// "url = /driver atau diawali /driver/" phrasing.
	URLPrefix string `json:"url_prefix,omitempty"`
	// URLContains matches when data.url contains this substring anywhere.
	URLContains string `json:"url_contains,omitempty"`
	// TitlePrefix matches when the notification title starts with this value.
	TitlePrefix string `json:"title_prefix,omitempty"`

	ChannelID string `json:"channel_id"`
	// Priority is "high" or "normal" (case-insensitive); empty means "normal".
	Priority string `json:"priority,omitempty"`
}

// defaultChannelID/defaultPriority are what a notification gets when no tenant rule matches —
// including for a tenant with no rules configured at all (fcm_channel_rules = '[]').
const (
	defaultChannelID = "info"
	defaultPriority  = PriorityNormal
)

// ParseChannelRules decodes a tenant's raw apps.fcm_channel_rules JSON. Empty/nil input and a
// malformed value both come back as "no rules" rather than an error — a tenant that hasn't
// configured rules yet (or whose rules got corrupted some other way) should just fall back to the
// default channel, not break sending entirely.
func ParseChannelRules(raw []byte) []ChannelRule {
	if len(raw) == 0 {
		return nil
	}
	var rules []ChannelRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil
	}
	return rules
}

// ResolveChannel evaluates a tenant's rules against one notification's url/title and returns the
// Android channel_id + priority to send it with, falling back to "info"/normal when nothing
// matches. Callers should only call this when the /send request didn't already specify an
// explicit "channel" — per the riwariwi contract, an explicit channel always wins outright and
// skips rule evaluation entirely.
func ResolveChannel(rules []ChannelRule, notifURL, title string) (channelID string, priority Priority) {
	for _, r := range rules {
		if r.URLPrefix == "" && r.URLContains == "" && r.TitlePrefix == "" {
			continue // a rule with no conditions would match everything — treat as a no-op, not a catch-all
		}
		if r.URLPrefix != "" && !(notifURL == r.URLPrefix || strings.HasPrefix(notifURL, r.URLPrefix+"/")) {
			continue
		}
		if r.URLContains != "" && !strings.Contains(notifURL, r.URLContains) {
			continue
		}
		if r.TitlePrefix != "" && !strings.HasPrefix(title, r.TitlePrefix) {
			continue
		}
		p := defaultPriority
		if strings.EqualFold(r.Priority, "high") {
			p = PriorityHigh
		}
		return r.ChannelID, p
	}
	return defaultChannelID, defaultPriority
}
