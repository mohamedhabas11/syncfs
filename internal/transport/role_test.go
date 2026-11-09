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

// TestOpenClassifiedStreamDeniesOpener verifies opener role enforcement lives
// inside OpenClassifiedStream: a denied principal errors before touching the
// connection (nil conn would panic if validation were skipped).
func TestOpenClassifiedStreamDeniesOpener(t *testing.T) {
	client := auth.Identity{PrincipalType: "client", ID: "app-1"}
	if _, err := OpenClassifiedStream(nil, client, ClassRaft); err == nil {
		t.Fatalf("client opening ClassRaft must be denied inside the API")
	}
	// Allowed opener with nil conn panics only if validation passed; guard
	// with recover to prove denial happens first for the wrong role above.
}
