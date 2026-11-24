package main

import "slices"

// chatStatusCounts counts each unread channel once, including channels whose
// only unread activity is in followed threads. The value records whether any
// unread mention was found for that channel.
func chatStatusCounts(channels []channel) (unread, mentions int) {
	byID := make(map[string]bool)
	for _, ch := range channels {
		if ch.ID == "" || !ch.hasUnread() {
			continue
		}
		byID[ch.ID] = byID[ch.ID] || ch.Mentions > 0 || slices.ContainsFunc(ch.Threads, func(signal threadSignal) bool {
			return signal.mentions > 0
		})
	}
	for _, mentioned := range byID {
		if mentioned {
			mentions++
		}
	}
	return len(byID), mentions
}
