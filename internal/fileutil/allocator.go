package fileutil

import (
	"fmt"
	"os"
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

// EnsureDir creates dir (and parents) and refuses a symlink in its place,
// so a planted link cannot redirect an album's downloads elsewhere.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create album directory: %w", err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("stat album directory: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to use symlinked output directory: %s", dir)
	}
	return nil
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
	claimed, _ := a.ClaimAvailable(path, owner, nil)
	return claimed
}

// ClaimAvailable assigns a collision-free path to owner, considering an
// existing filesystem entry reusable only when available reports true.
func (a *PathAllocator) ClaimAvailable(path, owner string, available func(string) (bool, error)) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.claimed == nil {
		a.claimed = make(map[string]string)
	}
	if a.owners == nil {
		a.owners = make(map[string]string)
	}
	if existing, ok := a.owners[owner]; ok {
		return existing, nil
	}

	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for suffix := 1; ; suffix++ {
		candidate := path
		if suffix > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, suffix, ext)
		}
		key := pathKey(candidate)
		if currentOwner, exists := a.claimed[key]; exists && currentOwner != owner {
			continue
		}
		if available != nil {
			ok, err := available(candidate)
			if err != nil {
				return "", err
			}
			if !ok {
				continue
			}
		}
		a.claimed[key] = owner
		a.owners[owner] = candidate
		return candidate, nil
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
