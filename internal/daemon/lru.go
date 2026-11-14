package daemon

import (
	"container/list"
	"sync"
)

type LRUOptions[V any] struct {
	MaxEntries int
	MaxBytes   int
	SizeFn     func(V) int
}
type lruEntry[K comparable, V any] struct {
	key   K
	value V
	size  int
}

// LRU is a concurrency-safe bounded least-recently-used cache.
type LRU[K comparable, V any] struct {
	mu                          sync.Mutex
	maxEntries, maxBytes, bytes int
	sizeFn                      func(V) int
	ll                          *list.List
	table                       map[K]*list.Element
	hits, misses                uint64
}

func NewLRU[K comparable, V any](opts LRUOptions[V]) *LRU[K, V] {
	return &LRU[K, V]{maxEntries: opts.MaxEntries, maxBytes: opts.MaxBytes, sizeFn: opts.SizeFn, ll: list.New(), table: make(map[K]*list.Element)}
}
func (c *LRU[K, V]) Get(k K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.table[k]; ok {
		c.ll.MoveToFront(e)
		c.hits++
		return e.Value.(lruEntry[K, V]).value, true
	}
	c.misses++
	var z V
	return z, false
}
func (c *LRU[K, V]) Put(k K, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sz := 0
	if c.sizeFn != nil {
		sz = c.sizeFn(v)
	}
	if e, ok := c.table[k]; ok {
		x := e.Value.(lruEntry[K, V])
		c.bytes += sz - x.size
		e.Value = lruEntry[K, V]{k, v, sz}
		c.ll.MoveToFront(e)
	} else {
		c.table[k] = c.ll.PushFront(lruEntry[K, V]{k, v, sz})
		c.bytes += sz
	}
	for (c.maxEntries > 0 && c.ll.Len() > c.maxEntries) || (c.maxBytes > 0 && c.bytes > c.maxBytes) {
		e := c.ll.Back()
		if e == nil {
			break
		}
		x := e.Value.(lruEntry[K, V])
		delete(c.table, x.key)
		c.bytes -= x.size
		c.ll.Remove(e)
	}
}
func (c *LRU[K, V]) Delete(k K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.table[k]; ok {
		x := e.Value.(lruEntry[K, V])
		c.bytes -= x.size
		delete(c.table, k)
		c.ll.Remove(e)
	}
}
func (c *LRU[K, V]) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.table = make(map[K]*list.Element)
	c.bytes = 0
}
func (c *LRU[K, V]) Stats() (hits, misses uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses
}

func (c *LRU[K, V]) Hits() uint64   { h, _ := c.Stats(); return h }
func (c *LRU[K, V]) Misses() uint64 { _, m := c.Stats(); return m }
func (c *LRU[K, V]) Len() int       { c.mu.Lock(); defer c.mu.Unlock(); return c.ll.Len() }

func (c *LRU[K, V]) Bytes() int { c.mu.Lock(); defer c.mu.Unlock(); return c.bytes }
