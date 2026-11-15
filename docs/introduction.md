# Introduction

nightwatch-go is a Nightwatch client for Go and Goravel. A Go app that uses it reports requests, queries, jobs, commands, scheduled tasks, cache events, mail, notifications, outgoing requests, exceptions, logs and users to [Omni](https://omni.vortechron.com), a Nightwatch-compatible receiver. The records have the same shape as those of `laravel/nightwatch` **v1.30.3**, so Omni's dashboards work unchanged for Go and PHP apps.

nightwatch-go is an independent project. It is not made by, affiliated with or endorsed by Laravel. "Laravel" and "Laravel Nightwatch" are trademarks of Laravel Holdings Inc.

## How it works

The client is built on OpenTelemetry. Goravel already makes spans and log records for requests, database queries and logs. nightwatch-go turns them into Nightwatch records and ships them in the background:

1. Goravel makes OpenTelemetry spans and log records.
2. The translator turns them into Nightwatch records.
3. A background sender batches, gzips and retries them, and posts them to `/api/omni/ingest`.

The translator holds a trace until its root span ends, so the execution record (a request, a job attempt, a command or a scheduled task) carries correct counters and every child names its execution. For the parts of Goravel that make no spans (the cache, the scheduler, console commands, queued jobs, mail, outgoing HTTP), the Goravel adapter wraps the framework services.

There is no separate agent process, as `php artisan nightwatch:agent` is in PHP. The Go client sends batches to Omni over HTTPS itself.

## Status

nightwatch-go is in the v0.2 series. Every record type that `laravel/nightwatch` sends is produced. [Differences from Laravel](differences) lists what works differently, and why.

The repository is public: https://github.com/vortechron/nightwatch-go.
