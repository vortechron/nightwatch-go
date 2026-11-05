package goravel

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/goravel/framework/contracts/cache"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// Laravel's Cache\Repository fires events only for reads (CacheHit,
// CacheMissed), for put/forever writes (KeyWritten, KeyWriteFailed) and for
// forget (KeyForgotten, KeyForgetFailed). add() on a store that has add()
// (Redis, Memcached, array, database ...), increment() and decrement() fire
// nothing, and pull() is get() plus forget(). A set-if-missing on an existing
// key is a normal "no", not a failed write, so it must never show as one.
func TestCache_EventsMatchLaravelForEveryMethod(t *testing.T) {
	seeded := func() *fakeDriver { return &fakeDriver{data: map[string]any{"have": "v"}} }
	failing := func() *fakeDriver { return &fakeDriver{data: map[string]any{}, failWrites: true} }
	value := func() (any, error) { return "computed", nil }
	broken := func() (any, error) { return nil, errors.New("callback failed") }

	tests := []struct {
		name   string
		driver func() *fakeDriver
		call   func(d cache.Driver)
		want   []string
	}{
		{"Get hit", seeded, func(d cache.Driver) { d.Get("have") }, []string{"hit"}},
		{"Get miss", seeded, func(d cache.Driver) { d.Get("gone") }, []string{"miss"}},
		{"GetString hit", seeded, func(d cache.Driver) { d.GetString("have") }, []string{"hit"}},
		{"GetInt miss", seeded, func(d cache.Driver) { d.GetInt("gone") }, []string{"miss"}},
		{"GetInt64 miss", seeded, func(d cache.Driver) { d.GetInt64("gone") }, []string{"miss"}},
		{"GetBool hit", seeded, func(d cache.Driver) { d.GetBool("have") }, []string{"hit"}},
		{"Has hit", seeded, func(d cache.Driver) { d.Has("have") }, []string{"hit"}},
		{"Has miss", seeded, func(d cache.Driver) { d.Has("gone") }, []string{"miss"}},

		{"Put ok", seeded, func(d cache.Driver) { _ = d.Put("k", 1, time.Minute) }, []string{"write"}},
		{"Put failing", failing, func(d cache.Driver) { _ = d.Put("k", 1, time.Minute) }, []string{"write-failure"}},
		{"Forever ok", seeded, func(d cache.Driver) { d.Forever("k", 1) }, []string{"write"}},
		{"Forever failing", failing, func(d cache.Driver) { d.Forever("k", 1) }, []string{"write-failure"}},

		{"Add new key", seeded, func(d cache.Driver) { d.Add("new", 1, time.Minute) }, nil},
		{"Add existing key", seeded, func(d cache.Driver) { d.Add("have", 1, time.Minute) }, nil},
		{"Increment", seeded, func(d cache.Driver) { _, _ = d.Increment("n") }, nil},
		{"Decrement", seeded, func(d cache.Driver) { _, _ = d.Decrement("n") }, nil},
		{"Lock", seeded, func(d cache.Driver) { d.Lock("l", time.Second) }, nil},

		{"Forget existing", seeded, func(d cache.Driver) { d.Forget("have") }, []string{"delete"}},
		{"Forget missing", seeded, func(d cache.Driver) { d.Forget("gone") }, []string{"delete-failure"}},

		// pull() = get() then forget(), whether or not the key was there.
		{"Pull hit", seeded, func(d cache.Driver) { d.Pull("have") }, []string{"hit", "delete"}},
		{"Pull miss", seeded, func(d cache.Driver) { d.Pull("gone") }, []string{"miss", "delete-failure"}},

		// remember() = get() then put() on a miss.
		{"Remember hit", seeded, func(d cache.Driver) { _, _ = d.Remember("have", time.Minute, value) }, []string{"hit"}},
		{"Remember miss", seeded, func(d cache.Driver) { _, _ = d.Remember("gone", time.Minute, value) }, []string{"miss", "write"}},
		{"Remember callback error", seeded, func(d cache.Driver) { _, _ = d.Remember("gone", time.Minute, broken) }, []string{"miss"}},
		{"RememberForever hit", seeded, func(d cache.Driver) { _, _ = d.RememberForever("have", value) }, []string{"hit"}},
		{"RememberForever miss", seeded, func(d cache.Driver) { _, _ = d.RememberForever("gone", value) }, []string{"miss", "write"}},
		{"RememberForever callback error", seeded, func(d cache.Driver) { _, _ = d.RememberForever("gone", broken) }, []string{"miss"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := setup(t)
			ctx, end := nightwatch.StartCommand(context.Background(), nightwatch.CommandInfo{Name: "c"})
			tt.call(Cache(ctx, tt.driver(), "redis"))
			end(0, nil)

			var got []string
			for _, record := range out.ofType("cache-event") {
				got = append(got, record["type"].(string))
			}
			slices.Sort(got)
			want := slices.Clone(tt.want)
			slices.Sort(want)

			if !slices.Equal(got, want) {
				t.Fatalf("events %v, want %v", got, want)
			}
			if command := out.ofType("command")[0]; command["cache_events"] != len(want) {
				t.Fatalf("the counter must match the events: %v", command["cache_events"])
			}
		})
	}
}

func TestCache_AddAndIncrementStillReachTheStore(t *testing.T) {
	setup(t)
	inner := &fakeDriver{data: map[string]any{"have": 1}}
	ctx, end := nightwatch.StartCommand(context.Background(), nightwatch.CommandInfo{Name: "c"})
	defer end(0, nil)
	d := Cache(ctx, inner, "")

	if !d.Add("new", 7, time.Minute) || d.Add("have", 9, time.Minute) {
		t.Fatal("Add must keep set-if-missing semantics")
	}
	if inner.data["new"] != 7 || inner.data["have"] != 1 {
		t.Fatalf("store: %v", inner.data)
	}
	if n, err := d.Increment("n"); n != 1 || err != nil {
		t.Fatalf("Increment result: %d %v", n, err)
	}
	if _, err := d.Decrement("n"); err == nil {
		t.Fatal("Decrement must pass the store's error through")
	}
}
