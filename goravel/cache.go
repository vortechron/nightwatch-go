package goravel

import (
	"context"
	"time"

	"github.com/spf13/cast"

	"github.com/goravel/framework/contracts/cache"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// WrapCache returns a cache whose WithContext(ctx) driver records cache
// events. Calls made without a context are not recorded: with no request in
// scope there is no execution to attach them to.
func WrapCache(inner cache.Cache) cache.Cache {
	return &cacheFacade{Cache: inner}
}

type cacheFacade struct{ cache.Cache }

func (c *cacheFacade) WithContext(ctx context.Context) cache.Driver {
	return Cache(ctx, c.Cache.WithContext(ctx), "")
}

func (c *cacheFacade) Store(name string) cache.Driver {
	return &storeFacade{Driver: c.Cache.Store(name), name: name}
}

// storeFacade keeps the store name when WithContext is called on a store.
type storeFacade struct {
	cache.Driver
	name string
}

func (s *storeFacade) WithContext(ctx context.Context) cache.Driver {
	return Cache(ctx, s.Driver.WithContext(ctx), s.name)
}

// Cache wraps a cache driver so its calls become cache-event records of the
// execution in ctx. store is the label shown in Omni.
//
// It records what Laravel's cache repository fires events for, and nothing else:
// hit or miss for reads (Get, GetString ..., Has, and the read inside Pull and
// Remember), write or write-failure for Put and Forever, delete or
// delete-failure for Forget (and the one inside Pull). Add, Increment,
// Decrement and Lock fire nothing in Laravel (add on a store that has add(), as
// every Redis, database or array store does), so they are passed through; in
// particular a set-if-missing on an existing key is not a failed write.
//
// Outside a Nightwatch execution it returns inner itself.
func Cache(ctx context.Context, inner cache.Driver, store string) cache.Driver {
	if !nightwatch.Active(ctx) {
		return inner
	}

	if store == "" {
		store = "default"
	}

	return &tracedCache{Driver: inner, ctx: ctx, store: store, absent: new(int)}
}

type tracedCache struct {
	cache.Driver
	ctx   context.Context //nolint:containedctx // the wrapper is bound to one execution on purpose
	store string
	// absent is handed to the driver as the default of a Get. Getting it back
	// means a miss, so hit or miss is known without a second call (Has).
	absent *int
}

// lookup reads a key once and records exactly one hit or miss event.
func (c *tracedCache) lookup(key string) (value any, hit bool) {
	_, end := nightwatch.StartCache(c.ctx, c.store, key)
	value = c.Driver.Get(key, c.absent)
	hit = value != any(c.absent)
	end(hitOrMiss(hit), 0, nil)

	return value, hit
}

func (c *tracedCache) Get(key string, def ...any) any {
	value, hit := c.lookup(key)
	if hit {
		return value
	}

	if len(def) > 0 {
		if fallback, ok := def[0].(func() any); ok {
			return fallback()
		}

		return def[0]
	}

	return nil
}

func (c *tracedCache) GetString(key string, def ...string) string {
	if value, hit := c.lookup(key); hit {
		return cast.ToString(value)
	}

	if len(def) > 0 {
		return def[0]
	}

	return ""
}

func (c *tracedCache) GetInt(key string, def ...int) int {
	if value, hit := c.lookup(key); hit {
		return cast.ToInt(value)
	}

	if len(def) > 0 {
		return def[0]
	}

	return 0
}

func (c *tracedCache) GetInt64(key string, def ...int64) int64 {
	if value, hit := c.lookup(key); hit {
		return cast.ToInt64(value)
	}

	if len(def) > 0 {
		return def[0]
	}

	return 0
}

func (c *tracedCache) GetBool(key string, def ...bool) bool {
	if value, hit := c.lookup(key); hit {
		return cast.ToBool(value)
	}

	return len(def) > 0 && def[0]
}

func (c *tracedCache) Has(key string) bool {
	_, end := nightwatch.StartCache(c.ctx, c.store, key)
	found := c.Driver.Has(key)
	end(hitOrMiss(found), 0, nil)

	return found
}

func (c *tracedCache) Put(key string, value any, ttl time.Duration) error {
	_, end := nightwatch.StartCache(c.ctx, c.store, key)
	err := c.Driver.Put(key, value, ttl)
	end(writeKind(err == nil), ttl, nil)

	return err //nolint:wrapcheck // passthrough of the cache error
}

func (c *tracedCache) Forever(key string, value any) bool {
	_, end := nightwatch.StartCache(c.ctx, c.store, key)
	saved := c.Driver.Forever(key, value)
	end(writeKind(saved), 0, nil)

	return saved
}

func (c *tracedCache) Forget(key string) bool {
	_, end := nightwatch.StartCache(c.ctx, c.store, key)
	removed := c.Driver.Forget(key)

	kind := nightwatch.CacheDelete
	if !removed {
		kind = nightwatch.CacheDeleteFailure
	}

	end(kind, 0, nil)

	return removed
}

// Pull is Laravel's get() followed by forget(): a hit or miss, then a delete
// event whether or not the key was there (a missing key is a delete-failure,
// as in Laravel).
func (c *tracedCache) Pull(key string, def ...any) any {
	value, hit := c.lookup(key)

	c.Forget(key)

	if hit {
		return value
	}

	if len(def) > 0 {
		return def[0]
	}

	return nil
}

// Remember is the driver's Remember built from Get and Put, so the hit or miss
// is known without a second lookup.
func (c *tracedCache) Remember(key string, ttl time.Duration, callback func() (any, error)) (any, error) {
	if value, hit := c.lookup(key); hit {
		return value, nil
	}

	value, err := callback()
	if err != nil {
		return nil, err
	}

	_ = c.Put(key, value, ttl)

	return value, nil
}

func (c *tracedCache) RememberForever(key string, callback func() (any, error)) (any, error) {
	if value, hit := c.lookup(key); hit {
		return value, nil
	}

	value, err := callback()
	if err != nil {
		return nil, err
	}

	c.Forever(key, value)

	return value, nil
}

func hitOrMiss(found bool) string {
	if found {
		return nightwatch.CacheHit
	}

	return nightwatch.CacheMiss
}

func writeKind(ok bool) string {
	if ok {
		return nightwatch.CacheWrite
	}

	return nightwatch.CacheWriteFailure
}
