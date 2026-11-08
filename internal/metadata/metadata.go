// Package metadata implements the deterministic Raft-backed inode/dentry/
// manifest state machine (M2-03).
//
// Every Command applies atomically or not at all; replay is a pure function
// of the command stream. Preconditions (expected revisions) make stale writes
// fail rather than silently overwriting concurrent changes (spec 7.4).
package metadata

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mohamedhabas11/syncfs/api/v1/gen"
	"google.golang.org/protobuf/proto"
)

// Inode kinds.
const (
	KindFile    = 1
	KindDir     = 2
	KindSymlink = 3
)

var (
	ErrCommandFailed = errors.New("metadata: command failed")
	ErrStale         = errors.New("metadata: precondition stale")
	ErrNotFound      = errors.New("metadata: inode not found")
	ErrAlreadyExists = errors.New("metadata: entry exists")
	ErrTooLarge      = errors.New("metadata: name or value too large")
	ErrNotEmpty      = errors.New("metadata: directory not empty")
	ErrNotDir        = errors.New("metadata: not a directory")
	ErrNotFile       = errors.New("metadata: not a regular file")
)

// Entry is one logical object update inside a command.
type Entry struct {
	Kind      int
	Rev       uint64
	Size      uint64
	Mode      uint32
	UID       uint32
	GID       uint32
	Manifest  [32]byte // file only
	Symlink   []byte
	LinkCount uint32 // hard-link count; last unlink frees the inode
}

// Version is the logical clock: raft index + intra-entry revisions.
type Version struct {
	RaftIndex uint64
	Revision  uint64
}

// OpResult is retained per operation ID (idempotency, spec 8.2).
type OpResult struct {
	State     string // COMMITTED | REJECTED
	RaftIndex uint64
	Data      []byte // failure code text for REJECTED
}

// StateMachine is the in-memory + serializable projection of the log.
type StateMachine struct {
	mu          sync.RWMutex
	version     Version
	rootID      [16]byte
	inodes      map[[16]byte]*Entry
	dentries    map[[16]byte]map[string][16]byte // parent -> name -> inode id
	ops         map[[16]byte]*OpResult
	volSpecs    map[string]uint64
	generations map[[16]byte]uint64 // directory generation bumps for lease fencing
}

// New creates a fresh state machine with a root directory at id 0.
func New() *StateMachine {
	root := [16]byte{}
	m := &StateMachine{
		inodes:      map[[16]byte]*Entry{},
		dentries:    map[[16]byte]map[string][16]byte{},
		ops:         map[[16]byte]*OpResult{},
		volSpecs:    map[string]uint64{},
		generations: map[[16]byte]uint64{},
	}
	m.rootID = root
	m.inodes[root] = &Entry{Kind: KindDir, Mode: 0755, Rev: 1}
	m.dentries[root] = map[string][16]byte{}
	return m
}

func cloneEntry(e *Entry) *Entry {
	if e == nil {
		return nil
	}
	c := *e
	if e.Symlink != nil {
		c.Symlink = append([]byte(nil), e.Symlink...)
	}
	return &c
}

// deepCopy returns an independent clone (used for apply-on-clone + snapshots).
func (m *StateMachine) deepCopy() *StateMachine {
	c := &StateMachine{
		version: m.version,
		rootID:  m.rootID,
		inodes:  make(map[[16]byte]*Entry, len(m.inodes)),
	}
	for k, v := range m.inodes {
		c.inodes[k] = cloneEntry(v)
	}
	c.dentries = make(map[[16]byte]map[string][16]byte, len(m.dentries))
	for k, v := range m.dentries {
		nm := make(map[string][16]byte, len(v))
		for n, child := range v {
			nm[n] = child
		}
		c.dentries[k] = nm
	}
	c.ops = make(map[[16]byte]*OpResult, len(m.ops))
	for k, v := range m.ops {
		r := *v
		c.ops[k] = &r
	}
	c.volSpecs = make(map[string]uint64, len(m.volSpecs))
	for k, v := range m.volSpecs {
		c.volSpecs[k] = v
	}
	c.generations = make(map[[16]byte]uint64, len(m.generations))
	for k, v := range m.generations {
		c.generations[k] = v
	}
	return c
}

// Snapshot deep-copies the machine for tests/serialization.
func (m *StateMachine) Snapshot() *StateMachine {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.deepCopy()
}

// GetOp fetches a retained operation result.
func (m *StateMachine) GetOp(opID [16]byte) (*OpResult, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.ops[opID]
	if !ok {
		return nil, false
	}
	c := *r
	return &c, true
}

// Lookup resolves a parent+name to an inode id.
func (m *StateMachine) Lookup(parent [16]byte, name string) ([16]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if kids, ok := m.dentries[parent]; ok {
		id, ok := kids[name]
		return id, ok
	}
	return [16]byte{}, false
}

// MaxFileBytes caps Truncate growth (quota gate before admission).
const MaxFileBytes = 32 << 20 // 32 MiB

// Apply executes cmd atomically and retains the op result. On precondition
// or command failure nothing changes except the REJECTED record.
func (m *StateMachine) Apply(opID [16]byte, idx uint64, cmd *syncfsapi.Command) error {
	data, err := proto.Marshal(cmd)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return ErrTooLarge
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev, ok := m.ops[opID]; ok {
		return fmt.Errorf("%w: op %x already %s (duplicate refused)", ErrCommandFailed, opID, prev.State)
	}
	for _, p := range cmd.Preconditions {
		var pid [16]byte
		copy(pid[:], p.InodeId)
		e, ok := m.inodes[pid]
		if !ok {
			return m.rejectLocked(opID, idx, ErrNotFound)
		}
		if e.Rev != uint64(p.ExpectedRevision) {
			return m.rejectLocked(opID, idx, ErrStale)
		}
	}
	work := m.deepCopy()
	if err := work.applyKind(opID, cmd); err != nil {
		return m.rejectLocked(opID, idx, err)
	}
	// commit the clone (splice fields one by one — never copy a held mutex)
	m.version = work.version
	m.rootID = work.rootID
	m.inodes = work.inodes
	m.dentries = work.dentries
	m.ops = work.ops
	m.volSpecs = work.volSpecs
	m.generations = work.generations
	m.version.RaftIndex = idx
	m.version.Revision++
	m.ops[opID] = &OpResult{State: "COMMITTED", RaftIndex: idx}
	return nil
}

func (m *StateMachine) rejectLocked(opID [16]byte, idx uint64, why error) error {
	m.ops[opID] = &OpResult{State: "REJECTED", RaftIndex: idx, Data: []byte(why.Error())}
	return fmt.Errorf("%w: %s", ErrCommandFailed, why.Error())
}

func (m *StateMachine) applyKind(opID [16]byte, cmd *syncfsapi.Command) error {
	switch cmd.Kind {
	case syncfsapi.CommandKind_CREATE:
		return m.doCreate(opID, cmd)
	case syncfsapi.CommandKind_WRITE_RANGES:
		return m.doWrite(cmd)
	case syncfsapi.CommandKind_TRUNCATE:
		return m.doTruncate(cmd)
	case syncfsapi.CommandKind_LINK:
		return m.doLink(cmd)
	case syncfsapi.CommandKind_UNLINK:
		return m.doUnlink(cmd)
	case syncfsapi.CommandKind_RENAME:
		return m.doRename(cmd)
	case syncfsapi.CommandKind_SET_ATTR:
		return m.doSetAttr(cmd)
	case syncfsapi.CommandKind_SET_XATTR, syncfsapi.CommandKind_SET_ACL:
		return m.doSetXattr(cmd)
	case syncfsapi.CommandKind_VOLUME_CONFIG, syncfsapi.CommandKind_MEMBERSHIP_PLAN:
		return m.doVolumeConfig(cmd)
	case syncfsapi.CommandKind_LOCK_GRANT, syncfsapi.CommandKind_LOCK_RELEASE:
		// lock records land here in M2-06; accept as no-ops for now.
		return nil
	default:
		return fmt.Errorf("%w: unknown kind %v", ErrCommandFailed, cmd.Kind)
	}
}

func oid(b []byte) ([16]byte, error) {
	var o [16]byte
	if len(b) != 16 {
		return o, ErrNotFound
	}
	copy(o[:], b)
	return o, nil
}

func (m *StateMachine) doCreate(opID [16]byte, cmd *syncfsapi.Command) error {
	p := cmd.GetCreate()
	parent, err := oid(p.ParentInodeId)
	if err != nil {
		return err
	}
	if pe, ok := m.inodes[parent]; !ok || pe.Kind != KindDir {
		return ErrNotDir
	}
	if !validName(p.Name) {
		return ErrTooLarge
	}
	if _, dup := m.dentries[parent][p.Name]; dup {
		return ErrAlreadyExists
	}
	// Deterministic inode ID: replay is a pure function of (opID, parent, name)
	// so all replicas derive the same ID for the same committed CREATE.
	h := sha256.New()
	h.Write(opID[:])
	h.Write(parent[:])
	h.Write([]byte(p.Name))
	sum := h.Sum(nil)
	var newID [16]byte
	copy(newID[:], sum[:16])
	m.inodes[newID] = &Entry{Kind: KindFile, Mode: 0644, UID: 0, GID: 0, Rev: 1, LinkCount: 1}
	m.dentries[parent][p.Name] = newID
	m.generations[parent]++
	return nil
}

func (m *StateMachine) doWrite(cmd *syncfsapi.Command) error {
	p := cmd.GetWriteRanges()
	id, err := oid(p.InodeId)
	if err != nil {
		return err
	}
	e, ok := m.inodes[id]
	if !ok {
		return ErrNotFound
	}
	if e.Kind != KindFile {
		return ErrNotFile
	}
	if len(p.NewManifest) != 32 {
		return ErrCommandFailed
	}
	copy(e.Manifest[:], p.NewManifest)
	e.Rev++
	return nil
}

func (m *StateMachine) doTruncate(cmd *syncfsapi.Command) error {
	id, err := oid(cmd.GetTruncate().InodeId)
	if err != nil {
		return err
	}
	e, ok := m.inodes[id]
	if !ok {
		return ErrNotFound
	}
	if e.Kind != KindFile {
		return ErrNotFile
	}
	size := cmd.GetTruncate().Size
	if size > MaxFileBytes {
		return ErrTooLarge
	}
	e.Size = size
	e.Rev++
	return nil
}

func (m *StateMachine) doLink(cmd *syncfsapi.Command) error {
	p := cmd.GetLink()
	parent, err := oid(p.ParentInodeId)
	if err != nil {
		return err
	}
	target, err := oid(p.TargetInodeId)
	if err != nil {
		return err
	}
	te, ok := m.inodes[target]
	if !ok {
		return ErrNotFound
	}
	if te.Kind == KindDir {
		return ErrCommandFailed // hard links to dirs are forbidden (POSIX)
	}
	if pe, ok := m.inodes[parent]; !ok || pe.Kind != KindDir {
		return ErrNotDir
	}
	if !validName(p.Name) {
		return ErrTooLarge
	}
	kids, ok := m.dentries[parent]
	if !ok {
		return ErrNotDir
	}
	if _, dup := kids[p.Name]; dup {
		return ErrAlreadyExists
	}
	kids[p.Name] = target
	te.Rev++
	te.LinkCount++
	m.generations[parent]++
	return nil
}

func (m *StateMachine) doUnlink(cmd *syncfsapi.Command) error {
	p := cmd.GetUnlink()
	parent, err := oid(p.ParentInodeId)
	if err != nil {
		return err
	}
	if _, ok := m.inodes[parent]; !ok {
		return ErrNotDir
	}
	kids, ok := m.dentries[parent]
	if !ok {
		return ErrNotDir
	}
	kid, hit := kids[p.Name]
	if !hit {
		return ErrNotFound
	}
	delete(kids, p.Name)
	m.generations[parent]++
	// Free the inode when its last link is removed (no zombie).
	if e, ok := m.inodes[kid]; ok {
		if e.LinkCount > 0 {
			e.LinkCount--
		}
		if e.LinkCount == 0 {
			// Verify no other dentry still references it (hard-link case).
			remaining := false
			for _, mp := range m.dentries {
				if _, ok := mp[p.Name]; ok && mp[p.Name] == kid {
					remaining = true
					break
				}
			}
			if !remaining {
				// Double-check by full scan (cheap at MVP scale).
				for _, mp := range m.dentries {
					for _, id := range mp {
						if id == kid {
							remaining = true
							break
						}
					}
					if remaining {
						break
					}
				}
			}
			if !remaining {
				delete(m.inodes, kid)
			}
		}
	}
	return nil
}

func (m *StateMachine) doRename(cmd *syncfsapi.Command) error {
	p := cmd.GetRename()
	srcPar, err := oid(p.SrcParent)
	if err != nil {
		return err
	}
	dstPar, err := oid(p.DstParent)
	if err != nil {
		return err
	}
	if !validName(p.SrcName) || !validName(p.DstName) {
		return ErrTooLarge
	}
	if se, ok := m.inodes[srcPar]; !ok || se.Kind != KindDir {
		return ErrNotDir
	}
	if de, ok := m.inodes[dstPar]; !ok || de.Kind != KindDir {
		return ErrNotDir
	}
	srcKids, ok := m.dentries[srcPar]
	if !ok {
		return ErrNotDir
	}
	dstKids, ok := m.dentries[dstPar]
	if !ok {
		return ErrNotDir
	}
	kid, hit := srcKids[p.SrcName]
	if !hit {
		return ErrNotFound
	}
	// Fail closed on existing dst (no silent overwrite / inode leak).
	if srcPar == dstPar && p.SrcName == p.DstName {
		return nil // rename onto itself is a no-op success
	}
	if _, hit := dstKids[p.DstName]; hit {
		return ErrAlreadyExists
	}
	// atomic within one command: both parents move together
	delete(srcKids, p.SrcName)
	dstKids[p.DstName] = kid
	m.generations[srcPar]++
	m.generations[dstPar]++
	return nil
}

func (m *StateMachine) doSetAttr(cmd *syncfsapi.Command) error {
	p := cmd.GetSetAttr()
	id, err := oid(p.InodeId)
	if err != nil {
		return err
	}
	e, ok := m.inodes[id]
	if !ok {
		return ErrNotFound
	}
	e.Mode, e.UID, e.GID = p.Mode, p.Uid, p.Gid
	e.Rev++
	return nil
}

func (m *StateMachine) doSetXattr(cmd *syncfsapi.Command) error {
	p := cmd.GetSetXattr()
	if p == nil {
		return ErrCommandFailed
	}
	key := p.Key
	// Only user.* with a non-empty suffix is supported; bare "user." / "user"
	// / "" / "x" are rejected (fail closed, no silent commit).
	if !strings.HasPrefix(key, "user.") || len(key) <= len("user.") {
		return ErrCommandFailed
	}
	id, err := oid(p.InodeId)
	if err != nil {
		return err
	}
	e, ok := m.inodes[id]
	if !ok {
		return ErrNotFound
	}
	e.Rev++
	return nil
}

func (m *StateMachine) doVolumeConfig(cmd *syncfsapi.Command) error {
	m.volSpecs["quota"]++
	return nil
}

func validName(name string) bool {
	if name == "" || len(name) > 255 || name == "." || name == ".." {
		return false
	}
	return !bytes.ContainsAny([]byte(name), "/\x00")
}
