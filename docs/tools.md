# Tools

The repository has two command-line tools for checking what an app sends.

## omni-capture

`omni-capture` is a fake Omni. It answers the agent-auth and ingest endpoints, applies the same decoder rules as Omni, and writes every record it receives to a folder:

```shell
go run ./cmd/omni-capture -addr 127.0.0.1:2601 -dir ./capture
```

Point an app at it with `NIGHTWATCH_BASE_URL=http://127.0.0.1:2601`. It works for the PHP package too. `-token` accepts only that bearer token. By default any token is accepted.

## nwdiff

`nwdiff` compares two capture folders, for example one from a Laravel app and one from the Go port of it:

```shell
go run ./cmd/nwdiff -golden ./golden -go ./capture -allow allow.txt
```

It exits with status 1 when the Go side lacks a record type or a field, has a JSON type that differs from Laravel's, has a `_group` of the wrong length, or has a child whose `execution_id` has no execution record. The `-allow` file is optional. Its lines look like `type:log` or `field:request.route_name`.
