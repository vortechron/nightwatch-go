package nightwatch

import (
	"context"
	"fmt"
	"hash/crc32"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// JobEnvelope is what travels with a queued job so the worker continues the
// trace of the code that queued it, and reports the same user.
type JobEnvelope struct {
	TraceParent string
	JobID       string
	Connection  string
	Queue       string
	User        string
	// Sync is set when the job runs inline in the dispatching process. Such a
	// job is not a job attempt: its work stays part of the dispatching execution.
	Sync bool
}

const (
	envelopeTag   = "nwtp2"
	envelopeParts = 8
)

var traceparentPattern = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)

// EncodeJobEnvelope returns the carrier string for the dispatch span in ctx:
//
//	nwtp2|<traceparent>|<job id>|<connection>|<queue>|<user>|<sync 0/1>|<crc32>
//
// The text fields are URL escaped. It returns "" when ctx has no dispatch span
// (see StartJobDispatch). The strict shape (tag, valid W3C traceparent, UUID
// job id, checksum) lets a worker tell its own envelope from a job argument.
func EncodeJobEnvelope(ctx context.Context, connection, queue string, sync bool) string {
	jobID, _ := ctx.Value(jobIDKey{}).(string)
	if !trace.SpanFromContext(ctx).SpanContext().IsValid() || jobID == "" {
		return ""
	}

	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)

	flag := "0"
	if sync {
		flag = "1"
	}

	body := strings.Join([]string{
		envelopeTag, carrier.Get("traceparent"), jobID,
		url.QueryEscape(connection), url.QueryEscape(queue), url.QueryEscape(UserFromContext(ctx)), flag,
	}, "|")

	return fmt.Sprintf("%s|%08x", body, crc32.ChecksumIEEE([]byte(body)))
}

// DecodeJobEnvelope parses EncodeJobEnvelope output. Anything that does not
// match the strict shape, including a wrong checksum, is not an envelope.
func DecodeJobEnvelope(value string) (JobEnvelope, bool) {
	parts := strings.Split(value, "|")
	if len(parts) != envelopeParts || parts[0] != envelopeTag {
		return JobEnvelope{}, false
	}

	body := strings.Join(parts[:envelopeParts-1], "|")
	if parts[envelopeParts-1] != fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(body))) {
		return JobEnvelope{}, false
	}

	if !traceparentPattern.MatchString(parts[1]) || len(parts[2]) != 36 {
		return JobEnvelope{}, false
	}

	if _, err := uuid.Parse(parts[2]); err != nil || (parts[6] != "0" && parts[6] != "1") {
		return JobEnvelope{}, false
	}

	connection, errA := url.QueryUnescape(parts[3])
	queue, errB := url.QueryUnescape(parts[4])
	user, errC := url.QueryUnescape(parts[5])

	if errA != nil || errB != nil || errC != nil {
		return JobEnvelope{}, false
	}

	return JobEnvelope{
		TraceParent: parts[1], JobID: parts[2], Connection: connection, Queue: queue, User: user, Sync: parts[6] == "1",
	}, true
}

// IsJobEnvelope reports whether value is a valid envelope.
func IsJobEnvelope(value string) bool {
	_, ok := DecodeJobEnvelope(value)

	return ok
}
