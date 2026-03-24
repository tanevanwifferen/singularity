package git

import (
	"sync"
	"time"
)

// Cache provides caching for git operations
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*CacheEntry
	maxAge  time.Duration
	maxSize int
	hits    int
	misses  int
}

// CacheEntry holds a cached value with expiration
type CacheEntry struct {
	Value       interface{}
	CreatedAt   time.Time
	AccessedAt  time.Time
	AccessCount int
}

// NewCache creates a new cache with the specified max age and size
func NewCache(maxAge time.Duration, maxSize int) *Cache {
	return &Cache{
		entries: make(map[string]*CacheEntry),
		maxAge:  maxAge,
		maxSize: maxSize,
	}
}

// Get retrieves a value from cache
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.entries[key]
	if !exists {
		c.misses++
		return nil, false
	}

	// Check if expired
	if time.Since(entry.CreatedAt) > c.maxAge {
		delete(c.entries, key)
		c.misses++
		return nil, false
	}

	// Update access info
	entry.AccessedAt = time.Now()
	entry.AccessCount++
	c.hits++

	return entry.Value, true
}

// Set stores a value in cache
func (c *Cache) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict oldest if at capacity
	if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	c.entries[key] = &CacheEntry{
		Value:       value,
		CreatedAt:   time.Now(),
		AccessedAt:  time.Now(),
		AccessCount: 0,
	}
}

// evictOldest removes the least recently accessed entry
func (c *Cache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	first := true

	for key, entry := range c.entries {
		if first || entry.AccessedAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.AccessedAt
			first = false
		}
	}

	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

// Invalidate removes a specific entry from cache
func (c *Cache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// Clear removes all entries from cache
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*CacheEntry)
	c.hits = 0
	c.misses = 0
}

// Stats returns cache statistics
func (c *Cache) Stats() (hits, misses, size int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hits, c.misses, len(c.entries)
}

// HitRate returns the cache hit rate as a percentage
func (c *Cache) HitRate() float64 {
	total := c.hits + c.misses
	if total == 0 {
		return 0
	}
	return float64(c.hits) / float64(total) * 100
}

// Global cache instance for git operations
var (
	globalCache     *Cache
	globalCacheOnce sync.Once
)

// GetGlobalCache returns the global cache instance
func GetGlobalCache() *Cache {
	globalCacheOnce.Do(func() {
		// Default: 5 minute cache, 100 entries
		globalCache = NewCache(5*time.Minute, 100)
	})
	return globalCache
}

// CachedCompareBranches is a cached version of CompareBranches
func CachedCompareBranches(repoPath, branchA, branchB string) (*BranchComparison, error) {
	cache := GetGlobalCache()
	key := cacheKey("compare", repoPath, branchA, branchB)

	if val, ok := cache.Get(key); ok {
		if result, ok := val.(*BranchComparison); ok && result != nil {
			return result, nil
		}
	}

	result, err := CompareBranches(repoPath, branchA, branchB)
	if err != nil {
		return nil, err
	}

	cache.Set(key, result)
	return result, nil
}

// CachedCompareBranchesByTree is a cached version of CompareBranchesByTree
func CachedCompareBranchesByTree(repoPath, branchA, branchB string) (*TreeComparison, error) {
	cache := GetGlobalCache()
	key := cacheKey("tree", repoPath, branchA, branchB)

	if val, ok := cache.Get(key); ok {
		if result, ok := val.(*TreeComparison); ok && result != nil {
			return result, nil
		}
	}

	result, err := CompareBranchesByTree(repoPath, branchA, branchB)
	if err != nil {
		return nil, err
	}

	cache.Set(key, result)
	return result, nil
}

// CachedOpenRepo is a cached version of OpenRepo
func CachedOpenRepo(path string) (*RepoInfo, error) {
	cache := GetGlobalCache()
	key := cacheKey("repo", path)

	if val, ok := cache.Get(key); ok {
		if result, ok := val.(*RepoInfo); ok && result != nil {
			return result, nil
		}
	}

	result, err := OpenRepo(path)
	if err != nil {
		return nil, err
	}

	cache.Set(key, result)
	return result, nil
}

// InvalidateRepoCache invalidates cache for a specific repo
func InvalidateRepoCache(path string) {
	cache := GetGlobalCache()
	cache.Invalidate(cacheKey("repo", path))
	// Also invalidate all branch comparisons for this repo
	// In production, we'd track keys by repo
}

// cacheKey generates a cache key from components
func cacheKey(parts ...string) string {
	result := parts[0]
	for _, p := range parts[1:] {
		result += ":" + p
	}
	return result
}
