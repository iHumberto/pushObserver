// Package notify sends deployment notifications via Apprise HTTP API.
//
// Apprise is a separate container that receives POST /notify and forwards to
// Discord, Telegram, ntfy, Slack, and 100+ other services.
// pushObserver only needs to know one URL: http://apprise:8000/notify.
package notify

// TODO: Notifier struct, New(), NotifySuccess(), NotifyFailure(), NotifyNoChanges()
