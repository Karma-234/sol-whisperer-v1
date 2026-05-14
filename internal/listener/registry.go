package listener

import "sync"

// Registry tracks active mint watches across users.
// This enables low-latency routing decisions (Tier A and higher WS priority)
// without querying the database on every incoming market event.
type Registry struct {
	mu             sync.RWMutex
	watchByUserKey map[string]struct{}
	mintCounts     map[string]int
	mintUsers      map[string]map[string]struct{}
}

func NewRegistry() *Registry {
	return &Registry{
		watchByUserKey: make(map[string]struct{}),
		mintCounts:     make(map[string]int),
		mintUsers:      make(map[string]map[string]struct{}),
	}
}

func (r *Registry) AddWatch(userID string, mint string) {
	key := userID + ":" + mint
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.watchByUserKey[key]; ok {
		return
	}
	r.watchByUserKey[key] = struct{}{}
	r.mintCounts[mint]++
	if _, ok := r.mintUsers[mint]; !ok {
		r.mintUsers[mint] = make(map[string]struct{})
	}
	r.mintUsers[mint][userID] = struct{}{}
}

func (r *Registry) RemoveWatch(userID string, mint string) {
	key := userID + ":" + mint
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.watchByUserKey[key]; !ok {
		return
	}
	delete(r.watchByUserKey, key)
	r.mintCounts[mint]--
	if r.mintCounts[mint] <= 0 {
		delete(r.mintCounts, mint)
	}
	if users, ok := r.mintUsers[mint]; ok {
		delete(users, userID)
		if len(users) == 0 {
			delete(r.mintUsers, mint)
		}
	}
}

func (r *Registry) HasWatchers(mint string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.mintCounts[mint] > 0
}

func (r *Registry) ActiveMints() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.mintCounts))
	for mint := range r.mintCounts {
		out = append(out, mint)
	}
	return out
}

func (r *Registry) UsersForMint(mint string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	set, ok := r.mintUsers[mint]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(set))
	for userID := range set {
		out = append(out, userID)
	}
	return out
}
