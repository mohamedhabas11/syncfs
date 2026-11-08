// Package auth defines SyncFS certificate identity, role checks, enrollment,
// and secret redaction primitives (M1-03).
//
// Identity model (technical spec sections 8.1, 9): exactly one URI SAN of the
// form spiffe://syncfs.cluster/<cluster-id>/<type>/<id> where <type> is one of
// node, client, admin. Cluster ID and principal type are bound to the cert;
// richer runtime roles (voter/learner) are cluster membership, not cert fields.
package auth

// Identity is a verified certificate identity.
type Identity struct {
	ClusterID     string
	PrincipalType string // "node" | "client" | "admin"
	ID            string
	SANURI        string
	PublicKeyHash string // hex SHA-256 of the subject public key
}

func (i Identity) IsNode() bool   { return i.PrincipalType == "node" }
func (i Identity) IsClient() bool { return i.PrincipalType == "client" }
func (i Identity) IsAdmin() bool  { return i.PrincipalType == "admin" }

// Roles known to the cluster (membership-derived, not cert-derived).
type Role string

const (
	RoleVoter   Role = "voter"
	RoleLearner Role = "learner"
	RoleClient  Role = "client"
	RoleAdmin   Role = "admin"
)

// RoleFor returns the role a node id holds given cluster membership state.
// learner is the pre-promotion state; voter is fully promoted.
func RoleFor(principalType, id string, voters, learners map[string]bool) (Role, error) {
	switch principalType {
	case "node":
		if voters[id] {
			return RoleVoter, nil
		}
		if learners[id] {
			return RoleLearner, nil
		}
		return "", errUnknownRole
	case "client":
		return RoleClient, nil
	case "admin":
		return RoleAdmin, nil
	default:
		return "", errUnknownRole
	}
}
