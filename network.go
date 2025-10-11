/*
package dmt implements distributed networking functionality for the radix tree.
It provides peer-to-peer communication, leader election, and data synchronization
capabilities for maintaining a consistent distributed state across nodes.
*/
package dmt

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"capnproto.org/go/capnp/v3"
	"capnproto.org/go/capnp/v3/rpc"
)

/*
NetworkConfig holds configuration for distributed tree networking.
It defines the network behavior including listen address, peer connections,
node identification, and synchronization parameters.
*/
type NetworkConfig struct {
	// Address to listen on, e.g. ":6380" for all interfaces port 6380
	ListenAddr string
	// List of peer addresses to connect to
	PeerAddrs []string
	// Unique ID for this node
	NodeID string
	// Time between sync attempts with peers
	SyncInterval time.Duration
	// Directory for persisting data
	PersistDir string
}

/*
NetworkNode represents a distributed tree node.
It manages peer connections, handles RPC communication, maintains a merkle tree
for consistency verification, and participates in leader election.
*/
type NetworkNode struct {
	config     NetworkConfig
	forest     *Forest
	listener   net.Listener
	peers      map[string]*peer
	peersMutex sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
	merkleTree *MerkleTree
	metrics    *Metrics
	election   *Election

	// Error handling and logging
	errorHandler *ErrorHandler
	logger       Logger

	// Health monitoring
	healthMonitor *HealthMonitor

	// Memory management
	maxPeers        int
	cleanupInterval time.Duration
	lastCleanup     time.Time
}

/*
peer represents a connection to another tree node.
It maintains both the raw network connection and the RPC client
for communicating with the remote node.
*/
type peer struct {
	addr    string
	conn    net.Conn
	rpcConn *rpc.Conn
	client  RadixRPC
}

/*
calculateQuorumSize determines the correct quorum size based on cluster size.
For a cluster of N nodes, quorum is (N/2) + 1 to ensure majority consensus.
*/
func calculateQuorumSize(clusterSize int) int {
	if clusterSize <= 0 {
		return 1
	}
	return (clusterSize / 2) + 1
}

/*
NewNetworkNode creates a new networked tree node.
It initializes the network infrastructure, starts background processes
for connection management and synchronization, and sets up the merkle tree
for consistency verification.
*/
func NewNetworkNode(config NetworkConfig, forest *Forest) (*NetworkNode, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize logger and error handler
	logger := DefaultLogger(config.NodeID)
	metrics := NewMetrics()
	errorHandler := NewErrorHandler(logger, metrics, config.NodeID)

	node := &NetworkNode{
		config:          config,
		forest:          forest,
		peers:           make(map[string]*peer),
		ctx:             ctx,
		cancel:          cancel,
		merkleTree:      NewMerkleTree(),
		metrics:         metrics,
		errorHandler:    errorHandler,
		logger:          logger,
		maxPeers:        100, // Reasonable limit for peer connections
		cleanupInterval: 5 * time.Minute,
		lastCleanup:     time.Now(),
	}

	// Initialize election manager with proper quorum calculation
	node.election = NewElection(ElectionConfig{
		ElectionTimeout:   300 * time.Millisecond,
		HeartbeatInterval: 100 * time.Millisecond,
		QuorumSize:        calculateQuorumSize(len(config.PeerAddrs) + 1), // +1 for self
	}, node)

	// Initialize health monitor
	node.healthMonitor = NewHealthMonitor(HealthMonitorConfig{
		CheckInterval: 30 * time.Second,
		Logger:        logger,
		Metrics:       metrics,
	})

	// Register health checks
	healthChecks := CreateNetworkNodeHealthChecks(node)
	for name, checkFunc := range healthChecks {
		node.healthMonitor.RegisterCheck(name, checkFunc)
	}

	// Start listener
	listener, err := net.Listen("tcp", config.ListenAddr)
	if err != nil {
		cancel()
		dmtErr, _ := errorHandler.HandleError(err, "network_listen", map[string]interface{}{
			"listen_addr": config.ListenAddr,
		})
		return nil, fmt.Errorf("failed to start listener: %w", dmtErr)
	}
	node.listener = listener

	logger.Info("Network node started",
		"listen_addr", config.ListenAddr,
		"node_id", config.NodeID,
		"peer_count", len(config.PeerAddrs))

	// Start accept loop
	go node.acceptLoop()

	// Start peer connection loop
	go node.connectLoop()

	// Start sync loop only if SyncInterval is positive
	if config.SyncInterval > 0 {
		go node.syncLoop()
		// One-shot early sync to accelerate initial convergence
		time.AfterFunc(100*time.Millisecond, func() { node.syncWithPeers() })
	}

	// Start memory cleanup loop
	go node.memoryCleanupLoop()

	return node, nil
}

/*
acceptLoop accepts incoming peer connections.
It runs in the background and handles new peer connection attempts,
creating appropriate handlers for each connection.
*/
func (n *NetworkNode) acceptLoop() {
	for {
		conn, err := n.listener.Accept()
		if err != nil {
			select {
			case <-n.ctx.Done():
				return
			default:
				dmtErr, action := n.errorHandler.HandleError(err, "accept_connection", map[string]interface{}{
					"listen_addr": n.config.ListenAddr,
				})

				if action == ErrorActionShutdown {
					n.logger.Fatal("Fatal error in accept loop", "error", dmtErr)
					return
				}
				continue
			}
		}

		n.logger.Debug("Accepted new connection", "remote_addr", conn.RemoteAddr())
		go n.handleConnection(conn)
	}
}

/*
handleConnection handles incoming peer connections.
It sets up the RPC infrastructure for the connection and maintains
the connection until it is closed by the peer.
*/
func (n *NetworkNode) handleConnection(conn net.Conn) {
	// Create transport from connection
	transport := rpc.NewStreamTransport(conn)

	// Create RPC connection with this node as the bootstrap interface
	main := RadixRPC_ServerToClient(n)
	rpcConn := rpc.NewConn(transport, &rpc.Options{
		BootstrapClient: capnp.Client(main),
	})
	defer rpcConn.Close()

	// Wait for connection to close
	<-rpcConn.Done()
}

/*
connectLoop maintains connections to peers.
It periodically attempts to establish connections with configured peers
that are not currently connected.
*/
func (n *NetworkNode) connectLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Attempt initial connections immediately
	for _, addr := range n.config.PeerAddrs {
		n.peersMutex.RLock()
		_, exists := n.peers[addr]
		n.peersMutex.RUnlock()
		if !exists {
			go n.connectToPeer(addr)
		}
	}

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			for _, addr := range n.config.PeerAddrs {
				n.peersMutex.RLock()
				_, exists := n.peers[addr]
				n.peersMutex.RUnlock()

				if !exists {
					go n.connectToPeer(addr)
				}
			}
		}
	}
}

/*
connectToPeer establishes a connection to a peer.
It handles the connection setup, RPC client creation, and maintains
the connection until it is closed.
*/
func (n *NetworkNode) connectToPeer(addr string) {
	n.logger.Debug("Attempting to connect to peer", "peer_addr", addr)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		dmtErr, action := n.errorHandler.HandleError(err, "peer_connect", map[string]interface{}{
			"peer_addr": addr,
		})

		if action == ErrorActionRetry {
			n.logger.Warn("Failed to connect to peer, will retry", "peer_addr", addr, "error", dmtErr)
		} else {
			n.logger.Error("Failed to connect to peer", "peer_addr", addr, "error", dmtErr)
		}
		return
	}

	// Create transport and RPC connection
	transport := rpc.NewStreamTransport(conn)
	rpcConn := rpc.NewConn(transport, nil)

	// Get the bootstrap interface which should be a RadixRPC
	client := RadixRPC(rpcConn.Bootstrap(n.ctx))

	// Ensure capability is ready
	if err := client.WaitStreaming(); err != nil {
		dmtErr, _ := n.errorHandler.HandleError(err, "peer_capability_wait", map[string]interface{}{
			"peer_addr": addr,
		})
		n.logger.Error("Failed to wait for peer capability", "peer_addr", addr, "error", dmtErr)
		conn.Close()
		rpcConn.Close()
		return
	}

	p := &peer{
		addr:    addr,
		conn:    conn,
		rpcConn: rpcConn,
		client:  client,
	}

	n.peersMutex.Lock()
	n.peers[addr] = p
	n.peersMutex.Unlock()

	n.logger.Info("Successfully connected to peer", "peer_addr", addr)

	// Trigger an immediate sync upon connecting
	go n.syncWithPeers()

	// Wait for connection to close
	<-rpcConn.Done()

	n.logger.Info("Peer connection closed", "peer_addr", addr)

	// Clean up peer
	n.peersMutex.Lock()
	delete(n.peers, addr)
	n.peersMutex.Unlock()
}

/*
syncLoop periodically syncs with peers.
It ensures data consistency across the network by regularly
initiating synchronization with connected peers.
*/
func (n *NetworkNode) syncLoop() {
	ticker := time.NewTicker(n.config.SyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.syncWithPeers()
		}
	}
}

/*
syncWithPeers initiates sync with all connected peers.
It sends the current merkle root to each peer and processes any
differences that need to be synchronized.
*/
func (n *NetworkNode) syncWithPeers() {
	start := time.Now()
	n.peersMutex.RLock()
	peers := make([]*peer, 0, len(n.peers))
	for _, p := range n.peers {
		peers = append(peers, p)
	}
	n.peersMutex.RUnlock()

	// Get local merkle root to send to peers (thread-safe)
	var localRoot []byte
	n.merkleTree.mu.RLock()
	if n.merkleTree.Root != nil && n.merkleTree.Root.Hash != nil {
		localRoot = make([]byte, len(n.merkleTree.Root.Hash))
		copy(localRoot, n.merkleTree.Root.Hash)
	} else {
		localRoot = []byte{}
	}
	n.merkleTree.mu.RUnlock()

	for _, p := range peers {
		peerRef := p
		go func(pr *peer, root []byte, started time.Time) {
			// Use Sync RPC with actual local merkle root
			future, release := pr.client.Sync(n.ctx, func(sp RadixRPC_sync_Params) error {
				if err := sp.SetMerkleRoot(root); err != nil {
					return err
				}
				sp.SetTerm(n.election.getCurrentTerm())
				sp.SetLogIndex(n.election.getLastLogIndex())
				return nil
			})
			defer release()

			res, err := future.Struct()
			if err != nil {
				dmtErr, _ := n.errorHandler.HandleError(err, "sync_result", map[string]interface{}{
					"peer_addr": pr.addr,
				})
				n.logger.Error("Sync result error", "peer_addr", pr.addr, "error", dmtErr)
				return
			}
			diff, err := res.Diff()
			if err != nil {
				dmtErr, _ := n.errorHandler.HandleError(err, "sync_diff", map[string]interface{}{
					"peer_addr": pr.addr,
				})
				n.logger.Error("Sync diff error", "peer_addr", pr.addr, "error", dmtErr)
				return
			}
			entries, err := diff.Entries()
			if err != nil {
				dmtErr, _ := n.errorHandler.HandleError(err, "sync_entries", map[string]interface{}{
					"peer_addr": pr.addr,
				})
				n.logger.Error("Sync entries error", "peer_addr", pr.addr, "error", dmtErr)
				return
			}

			totalBytes := 0
			for i := 0; i < entries.Len(); i++ {
				e := entries.At(i)
				k, _ := e.Key()
				v, _ := e.Artifact()
				// Defensive copies before insert to avoid underlying buffer reuse issues
				kCopy := make([]byte, len(k))
				copy(kCopy, k)
				vCopy := make([]byte, len(v))
				copy(vCopy, v)
				_ = n.merkleTree.Insert(kCopy, vCopy)
				totalBytes += len(k) + len(v)
			}
			n.merkleTree.Rebuild()
			n.metrics.RecordSync(time.Since(started), totalBytes)
		}(peerRef, localRoot, start)
	}
}

/*
handleClientInsert processes insert requests from clients.
Only leaders can accept client writes.
*/
func (n *NetworkNode) handleClientInsert(key, value []byte) error {
	if n.election == nil || !n.election.isLeader() {
		return fmt.Errorf("not leader: writes must be sent to the leader node")
	}

	if err := n.forest.Insert(key, value); err != nil {
		return fmt.Errorf("failed to insert into forest: %w", err)
	}

	n.merkleTree.Insert(key, value)
	n.merkleTree.Rebuild()
	n.updateMerkleRoot()

	return nil
}

/*
handleReplicationInsert processes insert requests from leader for replication.
This handles log replication without applying to state machine immediately.
*/
func (n *NetworkNode) handleReplicationInsert(key, value []byte, term, index uint64) error {
	if n.election == nil {
		return fmt.Errorf("election manager not initialized")
	}

	n.election.logMutex.Lock()
	defer n.election.logMutex.Unlock()

	// Make copies of the byte slices to avoid Cap'n Proto memory reuse issues
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	valueCopy := make([]byte, len(value))
	copy(valueCopy, value)

	entry := RaftLogEntry{
		Term:  term,
		Index: index,
		Key:   keyCopy,
		Value: valueCopy,
	}

	// Ensure log consistency - truncate if there's a conflict
	if index <= uint64(len(n.election.log)) {
		existingEntry := &n.election.log[index-1]
		if existingEntry.Term != term {
			// Conflict: truncate log from this point
			n.election.log = n.election.log[:index-1]
			n.election.lastLogIndex = index - 1
			if n.election.lastLogIndex > 0 {
				n.election.lastLogTerm = n.election.log[n.election.lastLogIndex-1].Term
			} else {
				n.election.lastLogTerm = 0
			}
		}
	}

	// Append new entry if it's beyond current log
	if index > uint64(len(n.election.log)) {
		n.election.log = append(n.election.log, entry)
		n.election.lastLogIndex = index
		n.election.lastLogTerm = term

	}

	n.election.updateLogState(index, term)
	return nil
}

/*
Insert implements RadixRPC_Server.Insert.
It handles insertion of new data into the local tree and merkle tree,
ensuring consistency across the distributed system.

For client requests, only the leader can accept writes. For replication
from the leader to followers, the request is accepted regardless of local state.
*/
func (n *NetworkNode) Insert(ctx context.Context, call RadixRPC_insert) error {
	args := call.Args()
	key, err := args.Key()
	if err != nil {
		return fmt.Errorf("failed to get key: %w", err)
	}

	artifact, err := args.Artifact()
	if err != nil {
		return fmt.Errorf("failed to get artifact: %w", err)
	}

	term := args.Term()
	index := args.LogIndex()

	// Check if this is a client request (no term/index) or replication
	isReplication := term > 0 && index > 0

	var insertErr error
	if isReplication {
		insertErr = n.handleReplicationInsert(key, artifact, term, index)
	} else {
		insertErr = n.handleClientInsert(key, artifact)
	}

	// Prepare response
	result, err := call.AllocResults()
	if err != nil {
		return fmt.Errorf("failed to allocate results: %w", err)
	}

	if insertErr != nil {
		result.SetSuccess(false)
		if n.election != nil {
			result.SetTerm(n.election.getCurrentTerm())
			result.SetLogIndex(n.election.getLastLogIndex())
		}
		return insertErr
	}

	result.SetSuccess(true)
	result.SetTerm(term)
	result.SetLogIndex(index)
	return nil
}

/*
Sync implements RadixRPC_Server.Sync.
It handles synchronization requests from peers, comparing merkle roots
and sending any necessary updates to maintain consistency.
*/
func (n *NetworkNode) Sync(ctx context.Context, call RadixRPC_sync) error {
	args := call.Args()
	peerRoot, err := args.MerkleRoot()
	if err != nil {
		return err
	}
	peerTerm := args.Term()
	peerLogIndex := args.LogIndex()

	// Adjust term if peer term is higher but continue to serve diff
	if peerTerm > n.election.getCurrentTerm() {
		n.election.stepDown(peerTerm)
	}

	// If this is a heartbeat with commit index (empty merkle root), update commit index
	if len(peerRoot) == 0 && peerLogIndex > 0 {
		n.election.updateCommitIndex(peerLogIndex)
	}

	result, err := call.AllocResults()
	if err != nil {
		return err
	}

	// Snapshot local root under read lock
	var localRoot []byte
	n.merkleTree.mu.RLock()
	if n.merkleTree.Root != nil && n.merkleTree.Root.Hash != nil {
		localRoot = make([]byte, len(n.merkleTree.Root.Hash))
		copy(localRoot, n.merkleTree.Root.Hash)
	} else {
		localRoot = []byte{}
	}
	n.merkleTree.mu.RUnlock()

	// If merkle roots match and log indices align, no sync needed
	if bytes.Equal(peerRoot, localRoot) &&
		peerLogIndex <= n.election.getLastLogIndex() {
		diff, err := result.NewDiff()
		if err != nil {
			return err
		}
		entries, err := diff.NewEntries(0)
		if err != nil {
			return err
		}
		diff.SetEntries(entries)
		return nil
	}

	// Try to build a minimal peer tree representation from available metadata
	// Since we only have the peer's merkle root, we can't do a perfect diff
	// but we can make intelligent decisions based on what we know
	// Snapshot leaves safely under read lock
	var diffs []DiffEntry
	n.merkleTree.mu.RLock()
	if len(peerRoot) == 0 {
		diffs = make([]DiffEntry, 0, len(n.merkleTree.leafMap))
		for _, leaf := range n.merkleTree.leafMap {
			// Copy key/value defensively
			k := make([]byte, len(leaf.Key))
			copy(k, leaf.Key)
			v := make([]byte, len(leaf.Value))
			copy(v, leaf.Value)
			diffs = append(diffs, DiffEntry{Key: k, Value: v, Modified: false})
		}
	} else {
		// Without peer structure details, send all entries
		diffs = make([]DiffEntry, 0, len(n.merkleTree.leafMap))
		for _, leaf := range n.merkleTree.leafMap {
			k := make([]byte, len(leaf.Key))
			copy(k, leaf.Key)
			v := make([]byte, len(leaf.Value))
			copy(v, leaf.Value)
			diffs = append(diffs, DiffEntry{Key: k, Value: v, Modified: false})
		}
	}
	n.merkleTree.mu.RUnlock()

	// Create sync payload with log metadata
	diff, err := result.NewDiff()
	if err != nil {
		return err
	}

	entries, err := diff.NewEntries(int32(len(diffs)))
	if err != nil {
		return err
	}

	// Fill entries from diffs with term/index tracking
	for i, d := range diffs {
		entry := entries.At(i)
		entry.SetKey(d.Key)
		entry.SetTerm(n.election.getCurrentTerm())
		entry.SetIndex(n.election.getLastLogIndex() + uint64(i) + 1)
		entry.SetArtifact(d.Value)
	}

	diff.SetEntries(entries)
	diff.SetMerkleRoot(localRoot)
	diff.SetTerm(n.election.getCurrentTerm())
	diff.SetLogIndex(n.election.getLastLogIndex())

	return nil
}

/*
Recover implements RadixRPC_Server.Recover.
It provides a mechanism for peers to recover their state after
failures or disconnections.
*/
func (n *NetworkNode) Recover(ctx context.Context, call RadixRPC_recover) error {
	result, err := call.AllocResults()
	if err != nil {
		return err
	}
	state, err := result.NewState()
	if err != nil {
		return err
	}
	// Build full state entries from local merkle
	count := int32(len(n.merkleTree.leafMap))
	entries, err := state.NewEntries(count)
	if err != nil {
		return err
	}
	idx := 0
	for _, leaf := range n.merkleTree.leafMap {
		entry := entries.At(idx)
		entry.SetKey(leaf.Key)
		entry.SetArtifact(leaf.Value)
		entry.SetTerm(n.election.getCurrentTerm())
		entry.SetIndex(n.election.getLastLogIndex())
		idx++
	}
	state.SetEntries(entries)
	if n.merkleTree.Root != nil {
		state.SetMerkleRoot(n.merkleTree.Root.Hash)
	} else {
		state.SetMerkleRoot([]byte{})
	}
	state.SetTerm(n.election.getCurrentTerm())
	state.SetLogIndex(n.election.getLastLogIndex())
	return nil
}

/*
updateMerkleRoot updates the merkle root hash of the tree.
It rebuilds the merkle tree from the current state of the forest's tree
to ensure an accurate representation of the data.
*/
func (n *NetworkNode) updateMerkleRoot() {
	tree := n.forest.getTree()
	if tree == nil {
		return
	}

	// Rebuild Merkle tree from current data
	it := tree.root.Root().Iterator()
	for key, value, ok := it.Next(); ok; key, value, ok = it.Next() {
		n.merkleTree.Insert(key, value)
	}
	n.merkleTree.Rebuild()
}

/*
BroadcastInsert broadcasts an insert operation to all connected peers.
It ensures data consistency by propagating insertions to all nodes
in the network.
*/
func (n *NetworkNode) BroadcastInsert(key []byte, value []byte) {
	start := time.Now()
	n.peersMutex.RLock()
	defer n.peersMutex.RUnlock()

	currentTerm := n.election.getCurrentTerm()
	newLogIndex := n.election.getLastLogIndex() + 1

	for _, p := range n.peers {
		go func(peer *peer) {
			_, release := peer.client.Insert(n.ctx, func(p RadixRPC_insert_Params) error {
				if err := p.SetKey(key); err != nil {
					return err
				}
				if err := p.SetArtifact(value); err != nil {
					return err
				}
				p.SetTerm(currentTerm)
				p.SetLogIndex(newLogIndex)
				return nil
			})
			defer release()
		}(p)
	}

	n.metrics.RecordInsert(time.Since(start), len(key)+len(value))
}

/*
withPeers executes a function with a snapshot of current peers.
This reduces code duplication and ensures consistent peer handling.
*/
func (n *NetworkNode) withPeers(fn func([]*peer)) {
	n.peersMutex.RLock()
	peers := make([]*peer, 0, len(n.peers))
	for _, p := range n.peers {
		peers = append(peers, p)
	}
	n.peersMutex.RUnlock()

	fn(peers)
}

/*
ReplicateLogEntry replicates a log entry to all followers using AppendEntries RPC.
It implements the Raft log replication protocol, sending entries to followers
and waiting for majority acknowledgment before considering the entry committed.
*/
func (n *NetworkNode) ReplicateLogEntry(key []byte, value []byte, logIndex uint64) error {
	if !n.election.isLeader() {
		return fmt.Errorf("not leader: cannot replicate log entries")
	}

	start := time.Now()
	var peers []*peer
	n.withPeers(func(p []*peer) {
		peers = p
	})

	currentTerm := n.election.getCurrentTerm()

	// Record replication attempt
	n.metrics.RecordAppendEntriesSent()

	// Use configured quorum size or calculate based on original cluster size
	var quorumSize int
	if n.election.config.QuorumSize > 0 {
		quorumSize = n.election.config.QuorumSize
	} else {
		// Calculate based on original cluster size (peers + self)
		totalNodes := len(peers) + 1
		quorumSize = (totalNodes / 2) + 1
	}

	if len(peers) == 0 {
		// No peers available - can only commit if quorum size is 1 (single node cluster)
		if quorumSize <= 1 {
			n.election.updateCommitIndex(logIndex)
			return nil
		} else {
			return fmt.Errorf("no peers available for replication, cannot achieve quorum of %d", quorumSize)
		}
	}

	// Use context with timeout for replication
	ctx, cancel := context.WithTimeout(n.ctx, 5*time.Second)
	defer cancel()

	// Send Insert RPC to all followers for replication
	successCount := int32(1) // Count self as success, use atomic for concurrent access
	successChan := make(chan bool, len(peers))

	for _, p := range peers {
		go func(peer *peer) {
			future, release := peer.client.Insert(ctx, func(params RadixRPC_insert_Params) error {
				if err := params.SetKey(key); err != nil {
					return fmt.Errorf("failed to set key: %w", err)
				}
				if err := params.SetArtifact(value); err != nil {
					return fmt.Errorf("failed to set artifact: %w", err)
				}
				params.SetTerm(currentTerm)
				params.SetLogIndex(logIndex)

				return nil
			})
			defer release()

			result, err := future.Struct()
			if err != nil {
				successChan <- false
				return
			}

			// Check if follower stepped down due to higher term
			if result.Term() > currentTerm {
				n.election.stepDown(result.Term())
				successChan <- false
				return
			}

			successChan <- result.Success()
		}(p)
	}

	// Wait for responses from all peers with timeout
	for i := 0; i < len(peers); i++ {
		select {
		case success := <-successChan:
			if success {
				successCount++
			}
		case <-ctx.Done():
			return fmt.Errorf("replication timeout: %w", ctx.Err())
		}
	}

	// Check if we have quorum
	if int(successCount) >= quorumSize {
		n.election.updateCommitIndex(logIndex)
		n.metrics.UpdateCommitIndex(logIndex)

		// Record successful replication
		duration := time.Since(start)
		n.metrics.RecordLogReplication(duration)

		n.logger.Debug("Log replication successful",
			"log_index", logIndex,
			"success_count", successCount,
			"quorum_size", quorumSize,
			"duration", duration)

		return nil
	}

	// Record failed replication
	n.metrics.RecordError("log_replication", "quorum_failure")

	return fmt.Errorf("failed to achieve quorum for log replication: got %d responses, needed %d", successCount, quorumSize)
}

/*
memoryCleanupLoop runs periodic memory cleanup to prevent unbounded growth.
It monitors and cleans up stale connections, expired data, and other resources.
*/
func (n *NetworkNode) memoryCleanupLoop() {
	ticker := time.NewTicker(n.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.performMemoryCleanup()
		}
	}
}

/*
performMemoryCleanup performs memory cleanup operations.
It removes stale peer connections, cleans up merkle tree caches,
and performs other memory management tasks.
*/
func (n *NetworkNode) performMemoryCleanup() {
	n.lastCleanup = time.Now()

	// Clean up stale peer connections
	n.cleanupStalePeers()

	// Clean up merkle tree memory
	n.cleanupMerkleMemory()

	// Clean up election log if it gets too large
	n.cleanupElectionLog()
}

/*
cleanupStalePeers removes stale or unhealthy peer connections.
It enforces the maximum peer limit and removes connections that
haven't been active recently.
*/
func (n *NetworkNode) cleanupStalePeers() {
	n.peersMutex.Lock()
	defer n.peersMutex.Unlock()

	// If we're over the peer limit, remove oldest connections
	if len(n.peers) > n.maxPeers {
		// Simple cleanup: remove peers not in configured peer list
		configPeers := make(map[string]bool)
		for _, addr := range n.config.PeerAddrs {
			configPeers[addr] = true
		}

		for addr, peer := range n.peers {
			if !configPeers[addr] {
				if peer != nil {
					if peer.rpcConn != nil {
						peer.rpcConn.Close()
					}
					if peer.conn != nil {
						peer.conn.Close()
					}
				}
				delete(n.peers, addr)

				// Stop if we're back under the limit
				if len(n.peers) <= n.maxPeers {
					break
				}
			}
		}
	}
}

/*
cleanupMerkleMemory performs memory cleanup for the merkle tree.
It triggers garbage collection of stale nodes and resets caches if needed.
*/
func (n *NetworkNode) cleanupMerkleMemory() {
	if n.merkleTree != nil {
		// Get current memory usage
		leafCount, nodeCount, cacheSize := n.merkleTree.GetMemoryUsage()

		// If memory usage is high, trigger a rebuild to clean up stale nodes
		if nodeCount > 10000 || cacheSize > 10000 {
			n.merkleTree.Rebuild()
		}

		// Update metrics with memory usage
		n.metrics.RecordMemoryUsage(leafCount, nodeCount, cacheSize)
	}
}

/*
cleanupElectionLog trims the election log if it grows too large.
It keeps only recent entries to prevent unbounded memory growth.
*/
func (n *NetworkNode) cleanupElectionLog() {
	if n.election != nil {
		n.election.logMutex.Lock()
		defer n.election.logMutex.Unlock()

		// Keep only the last 1000 log entries to prevent unbounded growth
		maxLogSize := 1000
		if len(n.election.log) > maxLogSize {
			// Keep the most recent entries
			keepFrom := len(n.election.log) - maxLogSize
			n.election.log = n.election.log[keepFrom:]

			// Update indices to reflect the truncation
			// Note: In a production system, this would need more careful handling
			// to ensure log consistency and proper snapshotting
		}
	}
}

/*
Close shuts down the network node.
It properly closes all peer connections and releases resources.
*/
func (n *NetworkNode) Close() error {
	n.logger.Info("Closing network node")
	n.cancel()

	// Stop health monitor
	if n.healthMonitor != nil {
		n.healthMonitor.Close()
	}

	// Stop election manager timers
	if n.election != nil {
		n.election.Close()
	}

	if n.listener != nil {
		n.listener.Close()
	}

	n.peersMutex.Lock()
	defer n.peersMutex.Unlock()

	for _, p := range n.peers {
		if p != nil {
			if p.rpcConn != nil {
				p.rpcConn.Close()
			}
			if p.conn != nil {
				p.conn.Close()
			}
		}
	}

	n.logger.Info("Network node closed successfully")
	return nil
}

/*
GetMetrics returns the current metrics.
It provides a snapshot of the node's operational metrics including
peer count and other performance indicators.
*/
func (n *NetworkNode) GetMetrics() map[string]any {
	n.peersMutex.RLock()
	defer n.peersMutex.RUnlock()

	n.metrics.UpdatePeerCount(int32(len(n.peers)))
	return n.metrics.GetMetrics()
}

/*
GetHealthMonitor returns the health monitor for external access.
*/
func (n *NetworkNode) GetHealthMonitor() *HealthMonitor {
	return n.healthMonitor
}

/*
RequestVote implements RadixRPC_Server.RequestVote.
It handles vote requests during leader election, delegating the
decision to the election manager.
*/
func (n *NetworkNode) RequestVote(ctx context.Context, call RadixRPC_requestVote) error {
	args := call.Args()
	term := args.Term()
	candidateId, err := args.CandidateId()
	if err != nil {
		return err
	}
	lastLogIndex := args.LastLogIndex()
	lastLogTerm := args.LastLogTerm()

	// Let election manager handle the vote request
	voteGranted := n.election.handleVoteRequest(term, candidateId, lastLogIndex, lastLogTerm)

	result, err := call.AllocResults()
	if err != nil {
		return err
	}

	result.SetTerm(n.election.getCurrentTerm())
	result.SetVoteGranted(voteGranted)
	return nil
}

/*
Heartbeat implements RadixRPC_Server.Heartbeat.
It processes heartbeat messages from the leader to maintain
the distributed system's consensus state.
*/
func (n *NetworkNode) Heartbeat(ctx context.Context, call RadixRPC_heartbeat) error {
	args := call.Args()
	term := args.Term()
	leaderId, err := args.LeaderId()
	if err != nil {
		return err
	}

	// Let election manager handle the heartbeat
	success := n.election.handleHeartbeat(term, leaderId)

	result, err := call.AllocResults()
	if err != nil {
		return err
	}

	result.SetTerm(term)
	result.SetSuccess(success)
	return nil
}

/*
setRPCResult is a helper method to set common RPC result fields.
It reduces code duplication across RPC method implementations.
*/
func (n *NetworkNode) setRPCResult(call interface{}, success bool) error {
	var result interface{}
	var err error

	switch c := call.(type) {
	case RadixRPC_appendEntries:
		result, err = c.AllocResults()
		if err != nil {
			return err
		}
		r := result.(RadixRPC_appendEntries_Results)
		r.SetTerm(n.election.getCurrentTerm())
		r.SetSuccess(success)
	case RadixRPC_requestVote:
		result, err = c.AllocResults()
		if err != nil {
			return err
		}
		r := result.(RadixRPC_requestVote_Results)
		r.SetTerm(n.election.getCurrentTerm())
		r.SetVoteGranted(success)
	case RadixRPC_heartbeat:
		result, err = c.AllocResults()
		if err != nil {
			return err
		}
		r := result.(RadixRPC_heartbeat_Results)
		r.SetTerm(n.election.getCurrentTerm())
		r.SetSuccess(success)
	}

	return nil
}

/*
AppendEntries implements RadixRPC_Server.AppendEntries.
It handles log replication requests from the leader, implementing
the core Raft log replication protocol with proper consistency checks.
*/
func (n *NetworkNode) AppendEntries(ctx context.Context, call RadixRPC_appendEntries) error {
	args := call.Args()
	term := args.Term()
	leaderId, err := args.LeaderId()
	if err != nil {
		return fmt.Errorf("failed to get leader ID: %w", err)
	}
	prevLogIndex := args.PrevLogIndex()
	prevLogTerm := args.PrevLogTerm()
	entries, err := args.Entries()
	if err != nil {
		return fmt.Errorf("failed to get entries: %w", err)
	}
	leaderCommit := args.LeaderCommit()

	// Validate election manager exists
	if n.election == nil {
		return fmt.Errorf("election manager not initialized")
	}

	// Let election manager handle the append entries request
	success := n.election.handleAppendEntries(term, leaderId, prevLogIndex, prevLogTerm, entries, leaderCommit)

	return n.setRPCResult(call, success)
}
