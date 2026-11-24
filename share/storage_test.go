package share

import (
	"sort"
	"sync"
	"testing"
	"time"
)

// memBackend is a minimal in-memory StorageBackend for testing the
// expiry-purge list methods.
type memBackend struct {
	mu    sync.Mutex
	links map[string]*Link
}

func newMemBackend(links ...*Link) *memBackend {
	b := &memBackend{links: make(map[string]*Link)}
	for _, l := range links {
		cp := *l
		b.links[l.Hash] = &cp
	}
	return b
}

func (b *memBackend) All() ([]*Link, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*Link, 0, len(b.links))
	for _, l := range b.links {
		cp := *l
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hash < out[j].Hash })
	return out, nil
}

func (b *memBackend) FindByUserID(id uint) ([]*Link, error) {
	all, _ := b.All()
	out := all[:0:0]
	for _, l := range all {
		if l.UserID == id {
			out = append(out, l)
		}
	}
	return out, nil
}

func (b *memBackend) GetByHash(hash string) (*Link, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	l, ok := b.links[hash]
	if !ok {
		return nil, nil
	}
	cp := *l
	return &cp, nil
}

func (b *memBackend) GetPermanent(string, uint) (*Link, error) { return nil, nil }
func (b *memBackend) Gets(string, uint) ([]*Link, error)       { return b.All() }
func (b *memBackend) Save(l *Link) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := *l
	b.links[l.Hash] = &cp
	return nil
}
func (b *memBackend) Delete(hash string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.links, hash)
	return nil
}
func (b *memBackend) DeleteWithPathPrefix(string, uint) error { return nil }
func (b *memBackend) DeleteByPathPrefix(string) error         { return nil }

// Consecutive expired links must all be purged from list output: removing
// elements while ranging would skip the entry after every deletion and
// over-report expired links as valid.
func TestListPurgesConsecutiveExpiredLinks(t *testing.T) {
	now := time.Now().Unix()
	back := newMemBackend(
		&Link{Hash: "expired-1", Path: "/a", Expire: now - 10},
		&Link{Hash: "expired-2", Path: "/b", Expire: now - 10},
		&Link{Hash: "live", Path: "/c", Expire: now + 3600},
		&Link{Hash: "expired-3", Path: "/d", Expire: now - 10},
		&Link{Hash: "permanent", Path: "/e"},
	)
	s := NewStorage(back)

	for name, list := range map[string]func() ([]*Link, error){
		"All":          s.All,
		"FindByUserID": func() ([]*Link, error) { return s.FindByUserID(0) },
		"Gets":         func() ([]*Link, error) { return s.Gets("/e", 0) },
	} {
		t.Run(name, func(t *testing.T) {
			links, err := list()
			if err != nil {
				t.Fatal(err)
			}
			if len(links) != 2 {
				hashes := make([]string, 0, len(links))
				for _, l := range links {
					hashes = append(hashes, l.Hash)
				}
				t.Fatalf("got %v, want [live permanent]", hashes)
			}
		})
	}

	// The expired records must be gone from the backend, not just hidden.
	remaining, _ := back.All()
	if len(remaining) != 2 {
		t.Fatalf("backend still holds %d links, want 2", len(remaining))
	}
}
