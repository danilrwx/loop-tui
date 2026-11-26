// Package contrib contains the notification service distributed with Loop TUI.
package contrib

import _ "embed"

// NotificationService is embedded in the verified release binary for installation and updates.
//
//go:embed loop-tui-notifications.service
var NotificationService string
