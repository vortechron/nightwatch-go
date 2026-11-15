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

## Filtering

`NIGHTWATCH_IGNORE_QUERIES=true` drops every query record. The execution still counts its queries.

## Known gaps

The query `file` and `line` are empty, because Goravel's database spans do not carry the caller. The `nightwatch.query.file` and `nightwatch.query.line` attributes fill them when you set them on a span yourself.
