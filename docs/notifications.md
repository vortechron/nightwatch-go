# Notifications

## Introduction

nightwatch-go does not wrap a notification system, so record each notification yourself with `nightwatch.StartNotification`. This comes from the example app:

```go
_, end := nightwatch.StartNotification(ctx.Context(), nightwatch.NotificationInfo{Channel: "database", Class: "InvoicePaid"})
end(nil)
```

Call `end` when the notification was sent. Pass the error when sending failed.

> [!NOTE]
> Notifications are not recorded automatically yet. `vortechron/notifications-go` has `OnSending`, `OnSent` and `OnFailed` hooks, but nightwatch-go is a public module and cannot import it. Until notifications-go calls `nightwatch.StartNotification` itself, wrap your send calls as shown above.

## Filtering

`RejectNotifications` drops the notifications for which the callback returns `true`, like `Nightwatch::rejectNotifications()`:

```go
client.RejectNotifications(func(notification nightwatch.Notification) bool {
	return notification.Channel == "database"
})
```

`NIGHTWATCH_IGNORE_NOTIFICATIONS=true` drops every notification record.

## Redaction

There are no redaction callbacks for notifications, as in Laravel.
