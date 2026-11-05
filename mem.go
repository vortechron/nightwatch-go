package nightwatch

import (
	"runtime"
	"sync"
	"time"
)

const memoryCacheFor = time.Second

// memSampler reports the Go heap in bytes. It is a label only: PHP reports
// peak memory, Go has no per-request figure. The value is cached for a second
// because reading memory stats stops the world briefly. Each Translator owns
// one, so clients never share state.
type memSampler struct {
	mu      sync.Mutex
	checked time.Time
	value   int64
}

func (m *memSampler) read(now time.Time) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	if now.Sub(m.checked) < memoryCacheFor && m.value > 0 {
		return m.value
	}

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	m.value = int64(stats.Alloc) //nolint:gosec // heap size always fits in int64
	m.checked = now

	return m.value
}
