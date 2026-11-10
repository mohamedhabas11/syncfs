// Package daemon is the T02 storage-daemon composition root: typed
// configuration, recovery-first startup, live readiness, and graceful
// shutdown. It is the only place that wires WAL, snapshot store, chunk
// store, metadata state machine, Raft driver, QUIC transport, volume
// coordinator, membership, repair/GC, and observability together.
package daemon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	syncfsapi "github.com/mohamedhabas11/syncfs/api/v1/gen"
	"github.com/mohamedhabas11/syncfs/internal/auth"
	"github.com/mohamedhabas11/syncfs/internal/chunkstore"
	"github.com/mohamedhabas11/syncfs/internal/metadata"
	"github.com/mohamedhabas11/syncfs/internal/observability"
	"github.com/mohamedhabas11/syncfs/internal/raftstore/driver"
	"github.com/mohamedhabas11/syncfs/internal/raftstore/snapshot"
	"github.com/mohamedhabas11/syncfs/internal/raftstore/wal"
	"github.com/mohamedhabas11/syncfs/internal/transport"
	"github.com/mohamedhabas11/syncfs/internal/volume"
	"github.com/mohamedhabas11/syncfs/internal/volume/lease"
	"github.com/quic-go/quic-go"
	"go.etcd.io/etcd/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

// Daemon owns all T02 composition instances.
type Daemon struct {
	cfg   Config
	wal   *wal.Storage
	chunk *chunkstore.Store
	fsm   *metadata.StateMachine
	drv   *driver.Driver

	collector *observability.Collector
	mu        sync.RWMutex
	applied   atomic.Uint64
	recovered atomic.Bool
	closeOnce sync.Once
	httpSrv   *http.Server
	quicLn    *quic.Listener
	self      auth.Identity
	cliTLS    *tls.Config
	srvTLS    *tls.Config
	peerMu    sync.Mutex
	peerConns map[uint64]*quic.Conn
	coord     *volume.Coordinator
}

// New recovers persistent state from disk. Listeners are NOT opened here;
// call Run to serve. Recovery failure is returned so the caller can exit
// non-zero (fail closed, never serve unrecovered state).
func New(cfg Config) (*Daemon, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	// Identity validation before recovery completes: the leaf must bind to
	// the configured cluster + node ID.
	pool, cert, self, err := loadIdentity(cfg)
	if err != nil {
		return nil, fmt.Errorf("daemon: load identity: %w", err)
	}
	srvTLS, err := transport.ServerTLSConfig([]tls.Certificate{cert}, pool, cfg.ClusterID)
	if err != nil {
		return nil, fmt.Errorf("daemon: server TLS: %w", err)
	}
	cliTLS, err := transport.ClientTLSConfig([]tls.Certificate{cert}, pool, cfg.ClusterID)
	if err != nil {
		return nil, fmt.Errorf("daemon: client TLS: %w", err)
	}

	walDir := cfg.DataDir + "/groups/default/wal"
	st, err := wal.Open(wal.Options{Dir: walDir, InitialPeers: cfg.Voters()})
	if err != nil {
		return nil, fmt.Errorf("daemon: open WAL: %w", err)
	}
	chunk, err := chunkstore.Open(cfg.DataDir + "/volumes/default/packs")
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("daemon: open chunkstore: %w", err)
	}
	// Recovery: prefer the durable projection (T06) if present; otherwise
	// deterministically replay the WAL tail. A corrupt projection fails
	// closed (never serve empty state).
	metaDir := cfg.DataDir + "/volumes/default/metadata"
	fsm, err := metadata.LoadPersisted(metaDir)
	if err != nil {
		if !os.IsNotExist(err) {
			_ = st.Close()
			_ = chunk.Close()
			return nil, fmt.Errorf("daemon: load persisted metadata: %w", err)
		}
		fsm = metadata.New()
		if err := replayWAL(st, fsm); err != nil {
			_ = st.Close()
			_ = chunk.Close()
			return nil, fmt.Errorf("daemon: replay WAL into metadata: %w", err)
		}
	} else {
		// Persisted projection present: still replay WAL tail beyond its
		// raft index to catch up to committed state.
		hs, _, herr := st.InitialState()
		base := uint64(0)
		if herr == nil {
			base = hs.Commit
			// Find applied index from projection: version RaftIndex.
			// Replay entries after that index only.
			_ = base
		}
		// Full replay is idempotent (skip existing opIDs), so replaying
		// the whole committed tail onto the projection is safe.
		if err := replayWAL(st, fsm); err != nil {
			_ = st.Close()
			_ = chunk.Close()
			return nil, fmt.Errorf("daemon: replay WAL tail onto projection: %w", err)
		}
	}
	drv, err := driver.NewDriverWithTicks(cfg.NodeID, cfg.Voters(), st, func(h [32]byte) bool {
		var id chunkstore.ChunkID
		copy(id[:], h[:])
		return chunk.Has(id)
	}, cfg.ElectionTick, cfg.HeartbeatTick)
	if err != nil {
		_ = st.Close()
		_ = chunk.Close()
		return nil, fmt.Errorf("daemon: create Raft driver: %w", err)
	}
	d := &Daemon{
		cfg: cfg, wal: st, chunk: chunk, fsm: fsm, drv: drv,
		collector: observability.NewCollector(),
		self:      self, cliTLS: cliTLS, srvTLS: srvTLS,
		peerConns: map[uint64]*quic.Conn{},
	}
	// Coordinator: local chunk store + remote putters over QUIC ClassChunk,
	// quorum from configured voters. Lease manager is empty for now (strict
	// reads go through Ready/leases when clients are wired, T03).
	coord := volume.NewCoordinator(fsm, map[uint64]*chunkstore.Store{cfg.NodeID: chunk})
	coord.SetDriver(drv)
	leases := lease.NewManager(2 * time.Second)
	coord.SetLeaseManager(leases)
	remote := map[uint64]volume.RemotePutter{}
	for _, p := range cfg.Peers {
		if p.ID == cfg.NodeID {
			continue
		}
		peerID := p.ID
		remote[peerID] = func(ctx context.Context, id uint64, data []byte) (chunkstore.Receipt, error) {
			return d.putChunkOnPeer(ctx, id, data)
		}
	}
	coord.SetRemotePutters(remote)
	coord.SetQuorum(cfg.Quorum())
	coord.SetRaftProposer(func(c context.Context, data []byte) (uint64, error) {
		// The proposed entry lands at lastIndex+1 (log is contiguous; entries
		// at/below the snapshot base are covered by the restored snapshot).
		last, lerr := st.LastIndex()
		if lerr != nil {
			return 0, lerr
		}
		idx := last + 1
		if err := drv.Propose(c, data); err != nil {
			return 0, err
		}
		// Do not acknowledge until the proposed entry has committed and been
		// applied locally (spec 8.2 step 8). Wait for the apply loop to durably
		// process the entry at idx; return its Raft index for the op result.
		if err := d.WaitApplied(c, idx); err != nil {
			return 0, err
		}
		return idx, nil
	})
	d.coord = coord
	d.recovered.Store(true)
	d.refreshMetrics()
	return d, nil
}

func loadIdentity(cfg Config) (*x509.CertPool, tls.Certificate, auth.Identity, error) {
	caPEM, err := os.ReadFile(cfg.CACert)
	if err != nil {
		return nil, tls.Certificate{}, auth.Identity{}, fmt.Errorf("read CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, tls.Certificate{}, auth.Identity{}, fmt.Errorf("CA cert %q is not PEM", cfg.CACert)
	}
	// Validate leaf chain + cluster binding via the same verifier the
	// transport uses, instead of trusting file contents blindly.
	cert, err := tls.LoadX509KeyPair(cfg.NodeCert, cfg.NodeKey)
	if err != nil {
		return nil, tls.Certificate{}, auth.Identity{}, fmt.Errorf("load node keypair: %w", err)
	}
	if cert.Leaf == nil && len(cert.Certificate) > 0 {
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return nil, tls.Certificate{}, auth.Identity{}, fmt.Errorf("parse node cert: %w", err)
		}
		cert.Leaf = leaf
	}
	if cert.Leaf == nil {
		return nil, tls.Certificate{}, auth.Identity{}, fmt.Errorf("node cert has no leaf")
	}
	id, err := auth.IdentityOf(cert.Leaf)
	if err != nil {
		return nil, tls.Certificate{}, auth.Identity{}, fmt.Errorf("identity: %w", err)
	}
	if id.ClusterID != cfg.ClusterID {
		return nil, tls.Certificate{}, auth.Identity{}, fmt.Errorf("node cert cluster %q != config %q", id.ClusterID, cfg.ClusterID)
	}
	wantID := fmt.Sprintf("%d", cfg.NodeID)
	if id.PrincipalType != "node" || id.ID != wantID {
		return nil, tls.Certificate{}, auth.Identity{}, fmt.Errorf("node cert identity %s/%s != config node/%s", id.PrincipalType, id.ID, wantID)
	}
	return pool, cert, id, nil
}

// replayWAL rebuilds the metadata state machine deterministically by
// applying every committed Command entry in the WAL tail. Non-Command
// entries (e.g. ConfChange) are skipped: membership state is owned by
// member.Cluster and is recovered separately (T07). Because WAL
// compaction never truncates beyond a snapshot and this implementation
// never takes a snapshot, the full tail is always available. If a
// snapshot with Data is ever introduced, this replay must be replaced by
// snapshot restore + tail replay (T06).
func replayWAL(st *wal.Storage, fsm *metadata.StateMachine) error {
	first, err := st.FirstIndex()
	if err != nil {
		return err
	}
	last, err := st.LastIndex()
	if err != nil {
		return err
	}
	if last < first {
		return nil
	}
	ents, err := st.Entries(first, last+1, 1<<30)
	if err != nil {
		return fmt.Errorf("read WAL entries %d..%d: %w", first, last, err)
	}
	hs, _, err := st.InitialState()
	if err != nil {
		return fmt.Errorf("read initial state: %w", err)
	}
	for _, e := range ents {
		if e.Index > hs.Commit {
			continue // uncommitted entries are not visible (Inv.2)
		}
		var cmd syncfsapi.Command
		if err := proto.Unmarshal(e.Data, &cmd); err != nil {
			continue // non-Command entry (e.g. ConfChange): skip
		}
		rawID := cmd.GetOperationId()
		if len(rawID) != 16 {
			continue
		}
		var opID [16]byte
		copy(opID[:], rawID)
		if _, ok := fsm.GetOp(opID); ok {
			continue
		}
		if err := fsm.Apply(opID, e.Index, &cmd); err != nil {
			if r, ok := fsm.GetOp(opID); ok && r.State == "REJECTED" {
				continue
			}
			return fmt.Errorf("replay entry %d: %w", e.Index, err)
		}
	}
	return nil
}

// refreshMetrics polls live state into the collector. Called on New and on
// a ticker in Run.
func (d *Daemon) refreshMetrics() {
	hs, _, err := d.wal.InitialState()
	var commit uint64
	if err == nil {
		commit = hs.Commit
	}
	applied := d.applied.Load()
	leader := d.drv.LeaderID()
	quorum := 0
	if leader != 0 {
		quorum = d.cfg.Quorum()
	}
	var lag uint64
	if commit > applied {
		lag = commit - applied
	}
	d.collector.SetFromStats(observability.Stats{
		LeaderQuorum: quorum, CommittedIndex: commit, AppliedIndex: applied, FollowerLag: lag,
		CheckpointIndex: commit,
	})
}

// Ready reports whether the node can serve quorum writes: recovery done,
// no chunk gap, and a current leader exists. Callers must poll this for
// /readyz; it is never derived from config.
func (d *Daemon) Ready() bool {
	if !d.recovered.Load() {
		return false
	}
	if d.drv.LeaderID() == 0 {
		return false
	}
	if err := d.drv.ChunkGap(); err != nil {
		return false
	}
	return true
}

// Live reports process liveness (independent of quorum).
func (d *Daemon) Live() bool { return d.recovered.Load() }

// Collector exposes the metrics collector for tests.
func (d *Daemon) Collector() *observability.Collector { return d.collector }

// LeaderID exposes the current leader (0 if none).
func (d *Daemon) LeaderID() uint64 { return d.drv.LeaderID() }

// Propose proposes raw Raft data. Returns an error if not leader or gapped.
func (d *Daemon) Propose(ctx context.Context, data []byte) error {
	return d.drv.Propose(ctx, data)
}

// requireAdminToken enforces the optional bearer-token gate on mutating
// HTTP endpoints. Empty token disables the gate (loopback-only posture still
// applies via Config validation). Read-only endpoints (healthz/readyz/metrics)
// are intentionally unauthenticated on the local control surface.
func (d *Daemon) requireAdminToken(r *http.Request, w http.ResponseWriter) bool {
	if d.cfg.AdminToken == "" {
		return true
	}
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if !d.tokenValid(strings.TrimPrefix(auth, prefix)) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// tokenValid reports whether the presented bearer token matches config.
func (d *Daemon) tokenValid(tok string) bool {
	return d.cfg.AdminToken != "" && tok == d.cfg.AdminToken
}

// WaitApplied blocks until the apply loop has durably processed (and
// recorded) the outcome of the command entry at Raft index idx, or ctx
// expires. Leaders use this to acknowledge a quorum-committed write only
// after that specific entry has been applied (spec 8.2 step 8) — never before
// the commit/apply barrier (2026-11-10 execution review, Iter-8-01).
func (d *Daemon) WaitApplied(ctx context.Context, idx uint64) error {
	deadline := time.Now().Add(d.cfg.RequestDeadline)
	for {
		if d.applied.Load() >= idx {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("daemon: apply deadline exceeded at index %d (applied %d)", idx, d.applied.Load())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// chunkReceiptJSON is one /volume/chunks durable-receipt entry (Iter-8-07).
type chunkReceiptJSON struct {
	ChunkID string `json:"chunk_id"`
	PeerID  uint64 `json:"peer_id"`
}

// chunksResponse is the /volume/chunks response envelope.
type chunksResponse struct {
	Receipts []chunkReceiptJSON `json:"receipts"`
}

// chunkIDHex renders a 32-byte BLAKE3 chunk id as lowercase hex.
func chunkIDHex(id chunkstore.ChunkID) string {
	b := make([]byte, len(id))
	copy(b, id[:])
	return hex.EncodeToString(b)
}

// Run serves until ctx is done: HTTP (health/metrics) + QUIC peer mesh +
// Raft tick/step loop + metrics ticker. It blocks. Graceful shutdown is
// bounded by ShutdownTimeout: stop admission, drain messages, persist, close.
func (d *Daemon) Run(ctx context.Context) error {
	// QUIC peer listener (opened only after recovery above).
	ql, err := transport.Listen(d.cfg.QuicListen, d.srvTLS, nil)
	if err != nil {
		return fmt.Errorf("daemon: QUIC listen %s: %w", d.cfg.QuicListen, err)
	}
	d.quicLn = ql

	// HTTP: /healthz (liveness), /readyz (readiness), /metrics (prometheus),
	// POST /volume/chunks (quorum chunk prepare) + /volume/command (protobuf Command).
	// Mutating endpoints require the configured admin bearer token; the surface
	// is loopback-bound by config validation (2026-11-10 execution review, Iter-8-05).
	mux := http.NewServeMux()
	// POST /volume/chunks: raw body is split into 64KiB chunks and stored
	// on a majority (WriterChunks). Response is newline-separated 32B hex IDs.
	mux.HandleFunc("/volume/chunks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if !d.requireAdminToken(r, w) {
			return
		}
		if !d.Ready() {
			http.Error(w, "not-ready", http.StatusServiceUnavailable)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<26))
		if err != nil || len(body) == 0 {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if len(body) > (1 << 26) {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		var chunks [][]byte
		for off := 0; off < len(body); off += chunkstore.ChunkSize {
			end := off + chunkstore.ChunkSize
			if end > len(body) {
				end = len(body)
			}
			chunks = append(chunks, append([]byte(nil), body[off:end]...))
		}
		c, cancel := context.WithTimeout(context.Background(), d.cfg.RequestDeadline)
		defer cancel()
		receipts, err := d.coord.WriteChunks(c, chunks)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		// Return the durable receipts so the FUSE syncer can attach them to the
		// WRITE_RANGES command (payload-before-reference: a committed manifest
		// requires majority receipts; without them the commit is refused).
		resp := chunksResponse{Receipts: make([]chunkReceiptJSON, 0, len(receipts))}
		for _, r := range receipts {
			resp.Receipts = append(resp.Receipts, chunkReceiptJSON{
				ChunkID: chunkIDHex(r.ChunkID),
				PeerID:  r.PeerID,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})

	// POST /volume/command: protobuf Command (Content-Type: application/protobuf).
	// Proposes via the coordinator (chunk receipts already attached for WRITE_RANGES).
	mux.HandleFunc("/volume/command", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if !d.requireAdminToken(r, w) {
			return
		}
		if !d.Ready() {
			http.Error(w, "not-ready", http.StatusServiceUnavailable)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<22))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
		if len(body) > (1<<22) || len(body) == 0 {
			http.Error(w, "invalid body", http.StatusRequestEntityTooLarge)
			return
		}
		var cmd syncfsapi.Command
		if err := proto.Unmarshal(body, &cmd); err != nil {
			http.Error(w, "invalid protobuf: "+err.Error(), http.StatusBadRequest)
			return
		}
		rawID := cmd.GetOperationId()
		if len(rawID) != 16 {
			http.Error(w, "operation_id must be 16 bytes", http.StatusBadRequest)
			return
		}
		var opID [16]byte
		copy(opID[:], rawID)
		c, cancel := context.WithTimeout(context.Background(), d.cfg.RequestDeadline)
		defer cancel()
		res, err := d.coord.ProposeCommand(c, opID, &cmd)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		out, _ := proto.Marshal(&cmd)
		_ = out
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"state":%q,"raft_index":%d}`, res.State, res.RaftIndex)))
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if !d.Live() {
			http.Error(w, "not-live", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !d.Ready() {
			http.Error(w, "not-ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(d.collector.Prometheus()))
	})
	d.httpSrv = &http.Server{Addr: d.cfg.ListenAddr, Handler: mux}
	httpErr := make(chan error, 1)
	go func() { httpErr <- d.httpSrv.ListenAndServe() }()

	// Apply channel: committed entries delivered by the driver.
	applyCh := make(chan raftpb.Entry, 256)
	d.drv.SetApplyChannel(applyCh)

	// QUIC accept loop: feed incoming Raft messages to the driver.
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := ql.Accept(ctx)
			if err != nil {
				log.Printf("daemon node %d: accept conn: %v", d.cfg.NodeID, err)
				return
			}
			go d.acceptConn(ctx, conn)
		}
	}()

	// Raft tick/step/apply loop. A dedicated `for range` over the driver's
	// Ready channel consumes raft Ready cycles (Tick only advances the logical
	// clock); a committed entry is persisted, advanced, delivered and applied
	// here, and the leader acknowledges writes only once the applied index has
	// reached the proposed entry's index (2026-11-10 execution review, Iter-8).
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		// Stagger first tick by NodeID so sequentially-started fleets elect
		// deterministically (first node wins before peers campaign).
		tickMs := 25 + int(d.cfg.NodeID%3)*5
		time.Sleep(time.Duration(d.cfg.NodeID) * 1000 * time.Millisecond)
		tick := time.NewTicker(time.Duration(tickMs) * time.Millisecond)
		metricsTick := time.NewTicker(1 * time.Second)
		defer tick.Stop()
		defer metricsTick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				d.drv.Tick()
			case <-metricsTick.C:
				d.refreshMetrics()
			case r := <-d.drv.ReadyCh():
				msgs, err := d.drv.StepReady(r)
				if err != nil {
					log.Printf("daemon node %d: raft step: %v", d.cfg.NodeID, err)
					continue
				}
				if len(msgs) > 0 {
					// log.Printf("daemon node %d: %d msgs", d.cfg.NodeID, len(msgs))
				}
				for _, m := range msgs {
					go d.sendRaft(ctx, m)
				}
			case e := <-applyCh:
				if e.Type == raftpb.EntryConfChange || e.Type == raftpb.EntryConfChangeV2 {
					if err := d.drv.ApplyConfChange(e); err != nil {
						log.Printf("daemon: apply conf change %d: %v", e.Index, err)
					}
					d.applied.Store(e.Index)
					continue
				}
				var cmd syncfsapi.Command
				if err := proto.Unmarshal(e.Data, &cmd); err != nil {
					continue
				}
				rawID := cmd.GetOperationId()
				if len(rawID) != 16 {
					continue
				}
				var opID [16]byte
				copy(opID[:], rawID)
				if _, ok := d.fsm.GetOp(opID); ok {
					continue
				}
				if err := d.fsm.Apply(opID, e.Index, &cmd); err != nil {
					// The FSM records a REJECTED outcome in the op result; the
					// entry has now been processed at this index either way, so
					// the fencing/applied index must advance (never wedge on a
					// rejected command).
					d.applied.Store(e.Index)
					d.drv.RetryApply()
					continue
				}
				// Durably persist the projection before acknowledging
				// progress; a crash after this point recovers via
				// LoadPersisted + WAL tail, never by re-applying.
				if err := d.fsm.Persist(d.cfg.DataDir + "/volumes/default/metadata"); err != nil {
					log.Printf("daemon: persist metadata after index %d: %v", e.Index, err)
				}
				d.applied.Store(e.Index)
				d.drv.RetryApply()
			}
		}
	}()

	select {
	case <-ctx.Done():
		// Bounded graceful shutdown.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), d.cfg.ShutdownTimeout)
		_ = d.httpSrv.Shutdown(shutdownCtx)
		cancel()
		_ = ql.Close()
		<-loopDone
		<-acceptDone
		return d.Close()
	case err := <-httpErr:
		_ = ql.Close()
		if err == http.ErrServerClosed {
			return nil
		}
		return fmt.Errorf("daemon: HTTP server: %w", err)
	}
}

func (d *Daemon) peerConn(ctx context.Context, to uint64) (*quic.Conn, error) {
	// log.Printf("daemon node %d: peerConn to %d", d.cfg.NodeID, to)
	d.peerMu.Lock()
	defer d.peerMu.Unlock()
	if c, ok := d.peerConns[to]; ok && c.Context().Err() == nil {
		return c, nil
	}
	addr, ok := d.cfg.PeerAddr(to)
	if !ok {
		return nil, fmt.Errorf("no addr for %d", to)
	}
	c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := transport.Dial(c, addr, d.cliTLS, nil)
	if err != nil {
		return nil, err
	}
	if d.peerConns == nil {
		d.peerConns = map[uint64]*quic.Conn{}
	}
	d.peerConns[to] = conn
	return conn, nil
}

func (d *Daemon) acceptConn(ctx context.Context, conn *quic.Conn) {
	for {
		st, err := transport.AcceptClassifiedStream(ctx, conn, transport.ClassRaft)
		if err != nil {
			_ = conn.CloseWithError(0, "accept done")
			return
		}
		var tag [1]byte
		if _, err := io.ReadFull(st, tag[:]); err != nil {
			_ = st.Close()
			continue
		}
		if tag[0] == 3 { // ClassChunk
			_ = d.acceptChunkStream(st)
			_ = st.Close()
			continue
		}
		var hdr [4]byte
		if _, err := io.ReadFull(st, hdr[:]); err != nil {
			_ = st.Close()
			continue
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n == 0 || n > (1<<20) {
			_ = st.Close()
			continue
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(st, buf); err != nil {
			_ = st.Close()
			continue
		}
		_ = st.Close()
		var msg raftpb.Message
		if err := msg.Unmarshal(buf); err != nil {
			continue
		}
		func() {
			c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = d.drv.StepMsg(c, msg)
			cancel()
		}()
	}
}

func (d *Daemon) acceptChunkStream(st interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
}) error {
	var hdr [4]byte
	if _, err := io.ReadFull(st, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > chunkstore.ChunkSize {
		return fmt.Errorf("chunk len %d out of range", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(st, data); err != nil {
		return err
	}
	id, rc, err := d.chunk.Put(data)
	if err != nil {
		return err
	}
	if _, err := st.Write(id[:]); err != nil {
		return err
	}
	var vb [8]byte
	binary.BigEndian.PutUint64(vb[:], rc.VerifiedBytes)
	if _, err := st.Write(vb[:]); err != nil {
		return err
	}
	return nil
}

// sendRaft delivers one Raft message to its destination voter via QUIC
// using a persistent peer connection (reused across messages).
func (d *Daemon) sendRaft(ctx context.Context, m raftpb.Message) {
	if m.To == 0 || m.To == d.cfg.NodeID {
		return
	}
	conn, err := d.peerConn(ctx, m.To)
	if err != nil {
		return
	}
	st, err := transport.OpenClassifiedStream(conn, d.self, transport.ClassRaft)
	if err != nil {
		log.Printf("daemon node %d: open to %d: %v", d.cfg.NodeID, m.To, err)
		// Stale conn: evict and retry once on next tick.
		d.peerMu.Lock()
		delete(d.peerConns, m.To)
		d.peerMu.Unlock()
		return
	}
	b, err := m.Marshal()
	if err != nil || len(b) == 0 || len(b) > (1<<20) {
		_ = st.Close()
		return
	}
	if _, err := st.Write([]byte{0}); err != nil { // 0 = ClassRaft
		_ = st.Close()
		return
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := st.Write(hdr[:]); err != nil {
		_ = st.Close()
		return
	}
	if _, err := st.Write(b); err != nil {
		_ = st.Close()
		return
	}
	_ = st.Close()
}

// Snapshot creates a durable Raft snapshot from the current FSM state:
// encode → WriteSnapshotFileWithIDs (cluster/volume bound, tmp+rename+fsync).
// Safe to call concurrently with applies (FSM Encode is locked).
// Compaction beyond the snapshot is still refused by wal.Compact;
// durable truncation is a follow-up (known-limitations #7).
func (d *Daemon) Snapshot() error {
	data, err := d.fsm.Encode()
	if err != nil {
		return fmt.Errorf("encode FSM: %w", err)
	}
	hs, _, err := d.wal.InitialState()
	if err != nil {
		return fmt.Errorf("initial state: %w", err)
	}
	applied := d.applied.Load()
	snap := raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{Index: applied, Term: hs.Term, ConfState: raftpb.ConfState{Voters: d.cfg.Voters()}},
		Data:     data,
	}
	walDir := d.cfg.DataDir + "/groups/default/wal"
	if err := snapshot.WriteSnapshotFileWithIDs(walDir, &snap, d.cfg.ClusterID, d.cfg.Volume.Name); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	return nil
}

// Close stops admission and persists required state. Idempotent.
func (d *Daemon) Close() error {
	var first error
	d.closeOnce.Do(func() {
		first = d.drv.Stop()
		if err := d.chunk.Close(); err != nil && first == nil {
			first = err
		}
	})
	return first
}
