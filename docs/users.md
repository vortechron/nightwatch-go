# Users

## Introduction

Nightwatch shows the signed-in user on every record of an execution, and lists users on its user page. In a Goravel app the middleware asks Goravel's auth for the user id of each request. A request without a signed-in user has no user.

## Customizing user fields

The id alone is often not enough to recognise a user. `nwgoravel.WithUserResolver` turns the id into a name and a username, like `Nightwatch::user()`. This comes from the example app:

```go
// resolveUser gives the signed-in user a name for Omni's user page. A real app
// would load the user from the database here.
func resolveUser(_ contractshttp.Context, id string) (nightwatch.User, bool) {
	return nightwatch.User{ID: id, Name: "Example User", Username: "user" + id}, true
}
```

Pass it to the middleware: `nwgoravel.LazyMiddleware(facades.App, nwgoravel.WithUserResolver(resolveUser))`. Return false to keep only the id. Each user record is sent once per id.

## Setting the user yourself

Outside the middleware, or for an app without Goravel's auth, call `nightwatch.SetUser`. It stores the user on the current span and returns a context that remembers the id, so jobs dispatched from it carry the same user:

```go
reqCtx := nightwatch.SetUser(ctx.Context(), nightwatch.User{ID: "7", Name: "Jane Doe"})
```

`nightwatch.UserFromContext(ctx)` returns the stored id.
