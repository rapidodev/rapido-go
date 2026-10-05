package shadowtls

import "testing"

// TestUpdateUsersPatch pins the one deliberate change against upstream
// sing-shadowtls v0.2.1 (see service.go's own package doc comment):
// UpdateUsers swaps the user list loadUsers() returns, in place, with no
// reconstruction needed. If a future re-sync against a newer upstream
// release drops this patch, this test fails loudly instead of the hot-
// update silently stopping working.
func TestUpdateUsersPatch(t *testing.T) {
	var s Service
	s.users.Store(&[]User{{Name: "alice", Password: "pw-v1"}})

	users := s.loadUsers()
	if len(users) != 1 || users[0].Password != "pw-v1" {
		t.Fatalf("loadUsers() = %+v, want the initial list", users)
	}

	s.UpdateUsers([]User{{Name: "alice", Password: "pw-v2"}})

	users = s.loadUsers()
	if len(users) != 1 || users[0].Password != "pw-v2" {
		t.Fatalf("loadUsers() after UpdateUsers = %+v, want the swapped-in list", users)
	}
}

func TestLoadUsersOnZeroValueServiceReturnsNil(t *testing.T) {
	var s Service
	if users := s.loadUsers(); users != nil {
		t.Errorf("loadUsers() on a never-initialized Service = %+v, want nil", users)
	}
}
