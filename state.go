package nightwatch

import (
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ExecutionState tracks the state of a single request or command execution.
// It is stored in context.Context and is safe for concurrent access.
type ExecutionState struct {
	mu sync.Mutex

	// Identity
	TraceID   string
	Deploy    string
	Server    string
	Source    string // "request" or "command"
	Timestamp float64 // Unix timestamp with microsecond precision

	// Route info (populated by middleware adapter)
	RoutePath    string
	RouteName    string
	RouteMethods []string
	RouteDomain  string
	RouteAction  string

	// Request info
	Method     string
	URL        string
	IP         string
	StatusCode int
	RequestSize  int
	ResponseSize int

	// Stage tracking
	Stage      ExecutionStage
	stageStart time.Time
	StageDurations map[ExecutionStage]int // microseconds

	// User
	UserID string

	// Counters
	Exceptions       int
	Logs             int
	Queries          int
	OutgoingRequests int
	JobsQueued       int
	Mail             int
	Notifications    int
	CacheEvents      int
	LazyLoads        int
	FilesRead        int
	FilesWritten     int
	HydratedModels   int

	// Exception preview (last unhandled exception message)
	ExceptionPreview string

	// Peak memory (approximation via runtime.ReadMemStats)
	peakMemory uint64
}

// NewExecutionState creates a new state for a request execution.
func NewExecutionState(deploy, server string) *ExecutionState {
	now := time.Now()
	state := &ExecutionState{
		TraceID:   uuid.New().String(),
		Deploy:    deploy,
		Server:    server,
		Source:    "request",
		Timestamp: float64(now.UnixMicro()) / 1_000_000,
		Stage:     StageBeforeMiddleware,
		stageStart: now,
		StageDurations: make(map[ExecutionStage]int),
	}

	// Initialize all stage durations to 0
	for _, stage := range AllStages() {
		state.StageDurations[stage] = 0
	}

	return state
}

// TransitionStage records the duration of the current stage and transitions to the next.
func (s *ExecutionState) TransitionStage(next ExecutionStage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	elapsed := int(now.Sub(s.stageStart).Microseconds())
	s.StageDurations[s.Stage] = elapsed
	s.Stage = next
	s.stageStart = now
}

// TotalDuration returns the sum of all stage durations in microseconds.
func (s *ExecutionState) TotalDuration() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	total := 0
	for _, d := range s.StageDurations {
		total += d
	}
	return total
}

// IncrExceptions atomically increments the exception counter.
func (s *ExecutionState) IncrExceptions() {
	s.mu.Lock()
	s.Exceptions++
	s.mu.Unlock()
}

// IncrQueries atomically increments the query counter.
func (s *ExecutionState) IncrQueries() {
	s.mu.Lock()
	s.Queries++
	s.mu.Unlock()
}

// IncrOutgoingRequests atomically increments the outgoing requests counter.
func (s *ExecutionState) IncrOutgoingRequests() {
	s.mu.Lock()
	s.OutgoingRequests++
	s.mu.Unlock()
}

// IncrLogs atomically increments the log counter.
func (s *ExecutionState) IncrLogs() {
	s.mu.Lock()
	s.Logs++
	s.mu.Unlock()
}

// PeakMemory returns the peak memory usage in bytes (approximation).
func (s *ExecutionState) PeakMemory() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if m.Alloc > s.peakMemory {
		s.peakMemory = m.Alloc
	}
	return s.peakMemory
}

// ExecutionID returns the execution identifier. For requests, this is the trace ID.
func (s *ExecutionState) ExecutionID() string {
	return s.TraceID
}

// ExecutionPreview returns a human-readable preview of the execution.
func (s *ExecutionState) ExecutionPreview() string {
	if s.Source == "request" {
		return s.Method + " " + s.RoutePath
	}
	return ""
}

// CurrentStage returns the current stage as a string value.
func (s *ExecutionState) CurrentStage() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.Stage)
}
