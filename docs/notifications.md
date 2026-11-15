# Notifications

## Introduction

nightwatch-go does not wrap a notification system, so record each notification yourself with `nightwatch.StartNotification`. This comes from the example app:

```go
_, end := nightwatch.StartNotification(ctx.Context(), nightwatch.NotificationInfo{Channel: "database", Class: "InvoicePaid"})
end(nil)
```

Call `end` when the notification was sent. Pass the error when sending failed.

## Filtering

`NIGHTWATCH_IGNORE_NOTIFICATIONS=true` drops every notification record.
