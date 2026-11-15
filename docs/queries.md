# Queries

## Introduction

Goravel's database instrumentation makes a client span with `db.query.text` for every query. A query joins its execution when it runs with that execution's context:

```go
var rows []map[string]any
if err := facades.Orm().WithContext(reqCtx).Query().Raw("select * from tasks where id = ?", id).Scan(&rows); err != nil {
	return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()})
}
```

A query made without the context has no execution to attach to.

## Grouping

Queries are grouped like the PHP package: for known SQL drivers the statement is normalised first, so lists of values group together. `nightwatch.QueryGroup(driver, connection, sql)` returns the group hash.

## File and line

Like Laravel, each query record shows the file and line of the code that ran it. The provider adds `nightwatch.QueryCallerProcessor` to Goravel's tracer provider: when a database span starts, it reads the call stack and keeps the first application frame (not the Go installation, not the module cache or `vendor`). When there is none, it keeps the first dependency frame outside the database layers, as Laravel falls back to the first vendor file outside the framework. Paths are absolute, like exception frames.

The `nightwatch.query.file` and `nightwatch.query.line` attributes (`nightwatch.AttrQueryFile`, `nightwatch.AttrQueryLine`) set by hand on a span win over the processor's.

## Filtering

`RejectQueries` drops the queries for which the callback returns `true`, like `Nightwatch::rejectQueries()`. A rejected query is not sent and not counted on its execution.

```go
client.RejectQueries(func(query nightwatch.Query) bool {
	return strings.Contains(query.SQL, "into `jobs`")
})
```

`NIGHTWATCH_IGNORE_QUERIES=true` drops every query record. Those are not counted either.

## Redaction

`RedactQueries` lets you change the SQL before it is sent, like `Nightwatch::redactQueries()`. The query's group is hashed from the redacted SQL, as in Laravel.

```go
client.RedactQueries(func(query *nightwatch.Query) {
	query.SQL = strings.ReplaceAll(query.SQL, "secret", "***")
})
```
