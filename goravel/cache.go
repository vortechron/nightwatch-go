package goravel

import (
	"context"
	"time"

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
func Cache(ctx context.Context, inner cache.Driver, store string) cache.Driver {
	if store == "" {
		store = "default"
	}

	return &tracedCache{Driver: inner, ctx: ctx, store: store}
}

type tracedCache struct {
	cache.Driver
	ctx   context.Context //nolint:containedctx // the wrapper is bound to one execution on purpose
	store string
}

func (c *tracedCache) Get(key string, def ...any) any {
	_, end := nightwatch.StartCache(c.ctx, c.store, key)
	value := c.Driver.Get(key, def...)
	end(hitOrMiss(c.Driver.Has(key)), 0, nil)

	return value
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

func (c *tracedCache) Add(key string, value any, ttl time.Duration) bool {
	_, end := nightwatch.StartCache(c.ctx, c.store, key)
	added := c.Driver.Add(key, value, ttl)
	end(writeKind(added), ttl, nil)

	return added
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

func (c *tracedCache) Remember(key string, ttl time.Duration, callback func() (any, error)) (any, error) {
	_, end := nightwatch.StartCache(c.ctx, c.store, key)
	hit := c.Driver.Has(key)
	value, err := c.Driver.Remember(key, ttl, callback)
	end(hitOrMiss(hit), ttl, nil)

	return value, err //nolint:wrapcheck // passthrough of the cache error
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
