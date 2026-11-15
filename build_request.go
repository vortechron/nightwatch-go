package nightwatch

import (
	"encoding/json"
	"net/url"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// preparedRequest is a request after the RedactRequests callbacks ran: the
// fields to send and the preview its children carry. ok is false when a
// callback panicked; the execution is then dropped.
type preparedRequest struct {
	request          Request
	headers, payload string
	preview          string
	ok               bool
}

// prepareRequests runs the request redaction callbacks for the request roots
// among spans. It returns nil when no callback is registered, so the default
// path stays as it was.
func (t *Translator) prepareRequests(spans []sdktrace.ReadOnlySpan) map[trace.SpanID]*preparedRequest {
	hooks := t.load()
	if len(hooks.redactRequests) == 0 {
		return nil
	}

	prepared := map[trace.SpanID]*preparedRequest{}

	for _, span := range spans {
		if execKind(span) == RecordRequest && !newAttrSet(span.Attributes()).bool(AttrIgnore) {
			prepared[span.SpanContext().SpanID()] = t.prepareRequest(hooks, span)
		}
	}

	return prepared
}

// prepareRequest reads a request from its root span and runs the callbacks.
func (t *Translator) prepareRequest(hooks *hookSet, root sdktrace.ReadOnlySpan) *preparedRequest {
	attrs := newAttrSet(root.Attributes())
	request, headers, payload := t.requestFields(root, attrs)
	out := &preparedRequest{request: request, headers: headers, payload: payload, preview: requestPreview(attrs), ok: true}

	if len(hooks.redactRequests) == 0 {
		return out
	}

	request.Headers, request.Payload = decodeHeaders(headers), decodePayload(payload)

	redacted, keep := filtered(hooks, nil, hooks.redactRequests, request)
	if !keep {
		return &preparedRequest{}
	}

	out.request.URL = RedactURL(redacted.URL, t.cfg.RedactQuery)
	out.request.IP = redacted.IP
	out.headers = t.reRedactHeaders(redacted.Headers)
	out.payload = t.reRedactPayload(payload, redacted.Payload)
	out.preview = request.Method + " " + previewPath(out.request.URL, request.RoutePath)

	return out
}

// previewPath is the path of a redacted URL, or the route pattern when the
// callback left something that is not a URL: never the original path.
func previewPath(target, route string) string {
	if parsed, err := url.Parse(target); err == nil && parsed.Path != "" {
		return parsed.Path
	}

	return route
}

// requestRecord builds the request execution record from the prepared
// request (callbacks already ran), or prepares it now. It reports false when
// a RedactRequests callback panicked.
func (t *Translator) requestRecord(root sdktrace.ReadOnlySpan, x execInfo, c counters, preview string, prepared *preparedRequest) (Record, bool) {
	if prepared == nil {
		prepared = t.prepareRequest(t.load(), root)
	}

	if !prepared.ok {
		return nil, false
	}

	attrs := newAttrSet(root.Attributes())
	request, headers, payload := prepared.request, prepared.headers, prepared.payload

	record := t.headRecord(RecordRequest, root.StartTime(), RequestGroup(request.RouteMethods, request.RouteDomain, request.RoutePath), x.traceID)

	duration := micros(request.Duration)
	before, after := attrs.int(AttrStageBefore), attrs.int(AttrStageAfter)

	record["user"] = x.user
	record["method"] = request.Method
	record["url"] = TinyText(request.URL)
	record["route_name"] = request.RouteName
	record["route_methods"] = request.RouteMethods
	record["route_domain"] = request.RouteDomain
	record["route_path"] = request.RoutePath
	record["route_action"] = request.RouteAction
	record["ip"] = request.IP
	record["duration"] = duration
	record["status_code"] = int64(request.StatusCode)
	record["request_size"] = request.RequestSize
	record["response_size"] = request.ResponseSize
	record["bootstrap"] = int64(0)
	record["before_middleware"] = before
	record["action"] = max(duration-before-after, 0)
	record["render"] = int64(0)
	record["after_middleware"] = after
	record["sending"] = int64(0)
	record["terminating"] = int64(0)
	executionCounters(record, c)
	record["peak_memory_usage"] = t.peakBytes(attrs)
	record["exception_preview"] = trimPreview(preview)
	record["context"] = "{}"
	record["headers"] = headers
	record["payload"] = payload

	return record, true
}

// requestFields reads the request from its span, with the headers and the
// payload as the redacted JSON text they are sent as.
func (t *Translator) requestFields(root sdktrace.ReadOnlySpan, attrs attrSet) (request Request, headers, payload string) {
	method := attrs.str(attrHTTPMethod)

	return Request{
		Method:       method,
		URL:          t.requestURL(attrs),
		RouteName:    attrs.str(AttrRouteName),
		RouteMethods: []string{method},
		RoutePath:    attrs.str(attrHTTPRoute),
		RouteAction:  attrs.str(AttrRouteAction),
		IP:           attrs.str(attrClientAddress),
		Duration:     spanDuration(root),
		StatusCode:   int(attrs.int(attrHTTPStatus)),
		RequestSize:  attrs.int(attrHTTPReqSize),
		ResponseSize: attrs.int(attrHTTPRespSize),
	}, t.headersJSON(attrs), t.payloadFor(attrs)
}

// headersJSON re-redacts even though capture helpers already did: a span
// attribute set by hand must not be able to leak a secret.
func (t *Translator) headersJSON(attrs attrSet) string {
	return Text(RedactHeadersJSON(attrs.str(AttrHeaders), t.cfg.RedactHeaders))
}

// payloadFor returns the redacted body of a failed request. It stays empty
// unless CaptureRequestPayload is on and the status is 500.
func (t *Translator) payloadFor(attrs attrSet) string {
	if !t.cfg.CaptureRequestPayload || attrs.int(attrHTTPStatus) != 500 {
		return ""
	}

	return Text(RedactPayloadJSON(attrs.str(AttrPayload), t.cfg.RedactPayloadFields))
}

// requestURL builds the request URL. Secrets in the query are redacted here
// even though the middleware already did it, so a span attribute set by hand
// cannot leak one.
func (t *Translator) requestURL(attrs attrSet) string {
	if full := attrs.str(attrURLFull); full != "" {
		return RedactURL(full, t.cfg.RedactQuery)
	}

	target := attrs.str(attrURLScheme) + "://" + attrs.str(attrServerAddress) + attrs.str(attrURLPath)
	if query := RedactQuery(attrs.str(attrURLQuery), t.cfg.RedactQuery); query != "" {
		target += "?" + query
	}

	return target
}

// reRedactHeaders encodes the headers a callback left and applies the header
// redaction again, so a callback cannot put a secret back in.
func (t *Translator) reRedactHeaders(headers map[string][]string) string {
	return Text(RedactHeadersJSON(jsonString(headers), t.cfg.RedactHeaders))
}

// reRedactPayload encodes the payload a callback left and redacts it again. A
// request that had no captured payload never gets one; a nil payload is none.
func (t *Translator) reRedactPayload(original string, payload map[string]any) string {
	if original == "" || payload == nil {
		return ""
	}

	return Text(RedactPayloadJSON(jsonString(payload), t.cfg.RedactPayloadFields))
}

// decodeHeaders reads the headers JSON (name to a value or a list of values).
func decodeHeaders(raw string) map[string][]string {
	var parsed map[string]any
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return map[string][]string{}
	}

	out := make(map[string][]string, len(parsed))
	for name, value := range parsed {
		out[name] = headerValues(value)
	}

	return out
}

func headerValues(value any) []string {
	list, isList := value.([]any)
	if !isList {
		list = []any{value}
	}

	out := make([]string, 0, len(list))
	for _, item := range list {
		if text, ok := item.(string); ok {
			out = append(out, text)
		} else {
			out = append(out, jsonString(item))
		}
	}

	return out
}

// decodePayload reads a JSON object payload; anything else is nil.
func decodePayload(raw string) map[string]any {
	var parsed map[string]any
	if raw == "" || json.Unmarshal([]byte(raw), &parsed) != nil {
		return nil
	}

	return parsed
}
