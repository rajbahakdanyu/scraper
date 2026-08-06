package fileutil

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

func SafeJoin(root, component string) (string, error) {
	if component == "" || component == "." || component == ".." || filepath.IsAbs(component) ||
		strings.ContainsAny(component, `/\\`) {
		return "", fmt.Errorf("unsafe path component %q", component)
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(filepath.Join(rootAbs, component))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes output directory", component)
	}
	return targetAbs, nil
}

// PathAllocator assigns collision-free paths within one download run.
type PathAllocator struct {
	mu      sync.Mutex
	claimed map[string]string
	owners  map[string]string
}

func NewPathAllocator() *PathAllocator {
	return &PathAllocator{claimed: make(map[string]string), owners: make(map[string]string)}
}

func (a *PathAllocator) Claim(path, owner string) string {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.claimed == nil {
		a.claimed = make(map[string]string)
	}
	if a.owners == nil {
		a.owners = make(map[string]string)
	}
	if currentOwner, ok := a.claimed[pathKey(path)]; !ok || currentOwner == owner {
		a.claimed[pathKey(path)] = owner
		a.owners[owner] = path
		return path
	}

	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, suffix, ext)
		if _, exists := a.claimed[pathKey(candidate)]; !exists {
			a.claimed[pathKey(candidate)] = owner
			a.owners[owner] = candidate
			return candidate
		}
	}
}

func (a *PathAllocator) Load(owner string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.owners[owner]
}

func pathKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}
