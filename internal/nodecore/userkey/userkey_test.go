package userkey

import (
	"fmt"
	"testing"
)

type testUser struct{ Name, Password string }

func TestListsReuseWhatEqualUsersBuiltEvenFromAnotherSlice(t *testing.T) {
	var lists Lists[testUser, *List]
	builds := 0
	get := func(users []testUser) *List {
		return lists.Get(users, func() *List {
			builds++
			return NewList(users)
		})
	}
	first := get([]testUser{{"a", "1"}, {"b", "2"}})
	second := get([]testUser{{"a", "1"}, {"b", "2"}})
	if first != second || builds != 1 {
		t.Errorf("equal lists built %d times", builds)
	}
	if third := get([]testUser{{"a", "1"}, {"b", "changed"}}); third == first || builds != 2 {
		t.Errorf("a changed credential was served from the old list (%d builds)", builds)
	}
}

// A caller reusing its slice must not change what was remembered.
func TestListsKeepTheirOwnCopyOfTheUsers(t *testing.T) {
	var lists Lists[testUser, *List]
	users := []testUser{{"a", "1"}}
	first := lists.Get(users, func() *List { return NewList(users) })
	users[0].Password = "2"
	second := lists.Get(users, func() *List { return NewList(users) })
	if first == second {
		t.Error("a list changed in place came back as the old one")
	}
	if first.Keys[0].Fingerprint == second.Keys[0].Fingerprint {
		t.Error("two different credentials have one fingerprint")
	}
}

func TestListsForgetTheOldestButNotWhatWasJustUsed(t *testing.T) {
	var lists Lists[testUser, int]
	builds := 0
	get := func(name string) {
		lists.Get([]testUser{{name, ""}}, func() int { builds++; return builds })
	}
	for i := 0; i < maxRecentLists; i++ {
		get(fmt.Sprint(i))
	}
	get("0") // now the most recent
	get("new")
	builds = 0
	get("0")
	if builds != 0 {
		t.Error("the list used last was forgotten")
	}
	get("1")
	if builds != 1 {
		t.Error("the list used longest ago was remembered past the limit")
	}
	if len(lists.recent) > maxRecentLists {
		t.Errorf("remembers %d lists", len(lists.recent))
	}
}

func TestNewListPolicyMatchesItsKeys(t *testing.T) {
	list := NewList([]testUser{{"alice", "pw"}, {"", "nameless"}})
	if len(list.Keys) != 2 || len(list.Policy) != 2 {
		t.Fatalf("%d keys, %d policy entries", len(list.Keys), len(list.Policy))
	}
	for _, key := range list.Keys {
		if list.Policy[key.Label] != key.Fingerprint {
			t.Errorf("policy for %q does not match its key", key.Label)
		}
	}
}

// BuildOptions remembers what it derived for the last lists, so a list that
// differs by one user re-derives only that user - and must still give exactly
// what deriving every user afresh gives, with Keys of its own.
func TestBuildOptionsReusesDerivedFieldsButMintsFreshKeys(t *testing.T) {
	type memoUser struct{ Name, Password string }
	users := make([]memoUser, 500)
	for i := range users {
		users[i] = memoUser{fmt.Sprintf("u%d", i), fmt.Sprintf("p%d", i)}
	}
	users[7].Name = "" // unnamed: labelled by position
	first := BuildOptions(users)

	changed := append([]memoUser(nil), users...)
	changed[3].Password = "new"
	changed = append(changed[:10], changed[11:]...) // u10 removed: later users move up
	changed = append(changed, memoUser{"late", "x"})
	second := BuildOptions(changed)

	for i, u := range changed {
		want := OptionKey(u, i)
		got := second[i]
		if got.Fingerprint != want.Fingerprint || got.Name != want.Name || got.Label != want.Label {
			t.Fatalf("user %d %+v: got %+v, want %+v", i, u, *got, *want)
		}
	}
	if second[3].Fingerprint == first[3].Fingerprint {
		t.Error("a changed password must change the fingerprint")
	}
	if second[0] == first[0] {
		t.Error("each build must mint its own Keys")
	}
	if second[7].Label != "7" {
		t.Errorf("an unnamed user is labelled by its position, got %q", second[7].Label)
	}
}

// A type that can't be a map key still works, without the memo.
func TestBuildOptionsHandlesUncomparableUsers(t *testing.T) {
	type listUser struct {
		Name  string
		Ports []int
	}
	keys := BuildOptions([]listUser{{"a", []int{1}}, {"b", nil}})
	if keys[0].Name != "a" || keys[1].Name != "b" || keys[0].Fingerprint == keys[1].Fingerprint {
		t.Errorf("keys = %+v %+v", *keys[0], *keys[1])
	}
}
