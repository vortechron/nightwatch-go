# Mail

## Introduction

Goravel's mail API takes no context, so wrap the mailer per request with `nwgoravel.Mail`. `Send` and `Queue` then become `mail` records of the execution. This comes from the example app:

```go
sender := nwgoravel.Mail(ctx.Context(), facades.Mail())
err := sender.To([]string{"person@example.test"}).Subject("Hello from the example").
	Content(mail.Content{Html: "<p>Hello</p>"}).Send()
```

Outside an execution, `nwgoravel.Mail` returns the mailer you gave it.

## Mailer name

The mailer shown in Omni is `smtp`, the only transport Goravel's mailer has. Pass a name to override it: `nwgoravel.Mail(ctx, facades.Mail(), "ses")`.

## Filtering

`NIGHTWATCH_IGNORE_MAIL=true` drops every mail record.
