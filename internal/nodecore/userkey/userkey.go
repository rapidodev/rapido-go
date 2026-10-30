// Package userkey is the identity the forked inbounds hand to their wire
// services in place of upstream sing-box's "index into the inbound's user
// slice".
//
// An index is only meaningful against the user list it was resolved from, and
// a handshake can take arbitrarily long (it waits on the client's next
// packet) - so if UpdateUsers swaps the list in between, the connection would
// index the new list: out of range (a panic that takes the whole node down)
// or, when the list merely reordered, bytes billed to the wrong user. A Key
// carries the name itself, so there is no second lookup to race.
package userkey

import (
	"crypto/sha256"
	"encoding/json"
	"slices"
	"strconv"
	"sync"
)

// Key identifies one user of one UpdateUsers generation. Every generation
// mints fresh Keys, so a Key is never equal to one from another generation.
type Key struct {
	Fingerprint [32]byte
	// Name is what sing-box calls metadata.User: empty for an unnamed user.
	Name string
	// Label is what logs and traffic accounting call the user: Name, or the
	// user's position in its list when it has no name.
	Label string
}

// OptionKey binds the authenticated identity to its actual credential set.
// Canonical JSON also makes raw options and typed options hash identically.
func OptionKey(value any, index int) *Key {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		panic(err)
	}
	name := ""
	for _, field := range []string{"name", "username", "Name", "Username"} {
		if raw, ok := fields[field]; ok {
			_ = json.Unmarshal(raw, &name)
			if name != "" {
				break
			}
		}
	}
	label := name
	if label == "" {
		label = strconv.Itoa(index)
	}
	canonical, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	return &Key{Name: name, Label: label, Fingerprint: sha256.Sum256(canonical)}
}

func BuildOptions[T any](users []T) []*Key {
	keys := make([]*Key, len(users))
	for i, value := range users {
		keys[i] = OptionKey(value, i)
	}
	return keys
}

// List is what one user list builds to: the Keys its service looks users up by
// and the credential fingerprints its connections are checked against. Nothing
// in it changes once built, so every listener serving the list can share it.
type List struct {
	Keys   []*Key
	Policy map[string][32]byte
}

func NewList[T any](users []T) *List {
	keys := BuildOptions(users)
	policy := make(map[string][32]byte, len(keys))
	for _, key := range keys {
		policy[key.Label] = key.Fingerprint
	}
	return &List{Keys: keys, Policy: policy}
}

// maxRecentLists is how many lists Lists remembers: the current one, the same
// one with flows cleared for inbounds that can't carry one, and one more. More
// distinct lists than that still work; they just get built per listener.
const maxRecentLists = 3

// Lists remembers what was built from the user lists seen most recently. A
// multi-port inbound hands every one of its listeners the same users, and with
// 50,000 of them on 51 ports, building each listener's own copy was most of what
// a user change cost: over ten seconds of CPU and a gigabyte of memory.
type Lists[T comparable, V any] struct {
	mu     sync.Mutex
	recent []listEntry[T, V]
}

type listEntry[T comparable, V any] struct {
	users []T
	value V
}

// Get returns the value built for a list equal to users, building it if needed.
// The comparison is by content, since each listener's caller may have its own
// slice. Comparing strings that share their bytes is fast, and they do here.
func (l *Lists[T, V]) Get(users []T, build func() V) V {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, entry := range l.recent {
		if slices.Equal(entry.users, users) {
			copy(l.recent[1:i+1], l.recent[:i])
			l.recent[0] = entry
			return entry.value
		}
	}
	value := build()
	l.recent = slices.Insert(l.recent, 0, listEntry[T, V]{slices.Clone(users), value})
	if len(l.recent) > maxRecentLists {
		l.recent = l.recent[:maxRecentLists]
	}
	return value
}

// String exists because sing's services format the key into their own error
// messages (trojan does, on a duplicate password) through a formatter that
// panics on a type it does not know.
func (k *Key) String() string { return k.Label }

// Build returns one fresh Key per name, in order.
func Build(names []string) []*Key {
	keys := make([]*Key, len(names))
	for i, name := range names {
		label := name
		if label == "" {
			label = strconv.Itoa(i)
		}
		keys[i] = &Key{Name: name, Label: label}
	}
	return keys
}
