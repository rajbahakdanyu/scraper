package fileutil

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// PathAllocator assigns collision-free paths within one download run.
type PathAllocator struct {
	mu      sync.Mutex
	claimed map[string]string
}

func NewPathAllocator() *PathAllocator {
	return &PathAllocator{claimed: make(map[string]string)}
}

func (a *PathAllocator) Claim(path, owner string) string {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.claimed == nil {
		a.claimed = make(map[string]string)
	}
	if currentOwner, ok := a.claimed[pathKey(path)]; !ok || currentOwner == owner {
		a.claimed[pathKey(path)] = owner
		return path
	}

	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, suffix, ext)
		if _, exists := a.claimed[pathKey(candidate)]; !exists {
			a.claimed[pathKey(candidate)] = owner
			return candidate
		}
	}
}

func pathKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}
