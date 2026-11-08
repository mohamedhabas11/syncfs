package contract_test

import (
	"testing"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func mustMsg(t *testing.T, f protoreflect.FileDescriptor, name protoreflect.Name) protoreflect.MessageDescriptor {
	t.Helper()
	m := f.Messages().ByName(name)
	if m == nil {
		t.Fatalf("message %s not found in %s", name, f.Path())
	}
	return m
}

func fieldByNumber(m protoreflect.MessageDescriptor, n int32) protoreflect.FieldDescriptor {
	for i := 0; i < m.Fields().Len(); i++ {
		f := m.Fields().Get(i)
		if int32(f.Number()) == n {
			return f
		}
	}
	return nil
}

// Envelope fields cluster_id/protocol_version/request_id/principal_type/role/san_id
// are fields 1-6 on every client-facing request (Hello, Command, GetOperation).
func TestContract_EnvelopeOnEveryRequest(t *testing.T) {
	msgs := []protoreflect.MessageDescriptor{
		mustMsg(t, syncfsapi.File_cluster_proto, "Hello"),
		mustMsg(t, syncfsapi.File_commands_proto, "Command"),
		mustMsg(t, syncfsapi.File_commands_proto, "GetOperation"),
	}
	want := []string{"cluster_id", "protocol_version", "request_id", "principal_type", "role", "san_id"}
	for _, m := range msgs {
		for i, name := range want {
			f := fieldByNumber(m, int32(i+1))
			if f == nil || string(f.Name()) != name {
				t.Errorf("%s: field %d should be %q, got %v", m.Name(), i+1, name, f)
			}
		}
	}
}

// Principal is flat envelope fields (4-6), NOT a oneof. PrincipalType carries
// NODE/CLIENT/ADMIN. CommandKind has 14 values 0-13.
func TestContract_PrincipalShape(t *testing.T) {
	cmd := mustMsg(t, syncfsapi.File_commands_proto, "Command")
	if cmd.Oneofs().ByName("principal") != nil {
		t.Error("Command must not use a 'principal' oneof; use flat envelope fields")
	}
	for _, n := range []string{"principal_type", "role", "san_id"} {
		if cmd.Fields().ByName(protoreflect.Name(n)) == nil {
			t.Errorf("Command missing envelope field %q", n)
		}
	}
	common := syncfsapi.File_common_proto
	pt := common.Enums().ByName("PrincipalType")
	if pt == nil {
		t.Fatal("PrincipalType enum not found")
	}
	for name, num := range map[string]int32{"NODE": 1, "CLIENT": 2, "ADMIN": 3} {
		found := false
		for i := 0; i < pt.Values().Len(); i++ {
			v := pt.Values().Get(i)
			if string(v.Name()) == name && int32(v.Number()) == num {
				found = true
			}
		}
		if !found {
			t.Errorf("PrincipalType missing %s=%d", name, num)
		}
	}
	ck := syncfsapi.File_commands_proto.Enums().ByName("CommandKind")
	if ck == nil {
		t.Fatal("CommandKind enum not found")
	}
	if ck.Values().Len() != 14 {
		t.Errorf("CommandKind must have 14 values (0-13), got %d", ck.Values().Len())
	}
}

// RaftMessage must never carry chunk bytes — only cluster binding + term/index/type.
func TestContract_ChunkBytesNotInRaft(t *testing.T) {
	m := mustMsg(t, syncfsapi.File_cluster_proto, "RaftMessage")
	for i := 0; i < m.Fields().Len(); i++ {
		f := m.Fields().Get(i)
		if f.Kind() == protoreflect.BytesKind {
			n := string(f.Name())
			if n != "cluster_id" {
				t.Errorf("RaftMessage field %q is bytes; only cluster_id may be bytes", n)
			}
		}
	}
}

// Every enum starts at 0=UNSPECIFIED; every message has at least one field.
func TestContract_ReservedCompatibility(t *testing.T) {
	files := []protoreflect.FileDescriptor{
		syncfsapi.File_common_proto,
		syncfsapi.File_commands_proto,
		syncfsapi.File_leases_proto,
		syncfsapi.File_cluster_proto,
	}
	for _, f := range files {
		for i := 0; i < f.Enums().Len(); i++ {
			e := f.Enums().Get(i)
			if e.Values().Len() == 0 || int32(e.Values().Get(0).Number()) != 0 {
				t.Errorf("%s: enum %s first value must be 0", f.Path(), e.Name())
			}
		}
		for i := 0; i < f.Messages().Len(); i++ {
			m := f.Messages().Get(i)
			if m.Fields().Len() == 0 {
				t.Errorf("%s: message %s has no fields", f.Path(), m.Name())
			}
		}
	}
}

// ALPN token is "syncfs/1" on Hello/HelloAck.
func TestContract_ALPN(t *testing.T) {
	hello := mustMsg(t, syncfsapi.File_cluster_proto, "Hello")
	if hello.Fields().ByName("alpn") == nil {
		t.Error("Hello must carry alpn field (must be exactly syncfs/1)")
	}
	ack := mustMsg(t, syncfsapi.File_cluster_proto, "HelloAck")
	if ack.Fields().ByName("alpn") == nil {
		t.Error("HelloAck must carry alpn field (must be exactly syncfs/1)")
	}
}
