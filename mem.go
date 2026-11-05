package nightwatch

import (
	"runtime"
	"time"
)

// peakMemory reports the Go heap in bytes. It is a label only: PHP reports
// peak memory, Go has no per-request figure. The value is cached for a
// second because reading memory stats stops the world briefly.
func peakMemory() int64 {
	memoryMu.Lock()
	defer memoryMu.Unlock()

	if time.Since(memoryChecked) < time.Second && memoryValue > 0 {
		return memoryValue
	}

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	memoryValue = int64(stats.Alloc) //nolint:gosec // heap size always fits in int64
	memoryChecked = time.Now()

	return memoryValue
}
