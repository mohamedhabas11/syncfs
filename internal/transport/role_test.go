package transport

import (
	"testing"

	"github.com/mohamedhabas11/syncfs/internal/auth"
)

func TestPerStreamRoleEnforcement(t *testing.T) {
	client := auth.Identity{PrincipalType: "client", ID: "app-1"}
	node := auth.Identity{PrincipalType: "node", ID: "n-1"}
	admin := auth.Identity{PrincipalType: "admin", ID: "op-1"}
	cases := []struct {
		id    auth.Identity
		class StreamClass
		ok    bool
	}{
		{node, ClassRaft, true},
		{client, ClassRaft, false}, // client cert must not open raft streams
		{node, ClassChunk, true},
		{client, ClassChunk, false},
		{client, ClassClient, true},
		{node, ClassClient, false},
		{admin, ClassControl, true},
		{client, ClassControl, false},
		{node, ClassControl, true},
	}
	for _, tc := range cases {
		err := ValidatePrincipalForClass(tc.id, tc.class)
		if tc.ok && err != nil {
			t.Errorf("%s on class %d must be allowed: %v", tc.id.PrincipalType, tc.class, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s on class %d must be denied", tc.id.PrincipalType, tc.class)
		}
	}
}
