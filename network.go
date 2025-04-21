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
	"github.com/theapemachine/caramba/pkg/datura"
	"github.com/theapemachine/caramba/pkg/errnie"
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
NewNetworkNode creates a new networked tree node.
It initializes the network infrastructure, starts background processes
for connection management and synchronization, and sets up the merkle tree
for consistency verification.
*/
func NewNetworkNode(config NetworkConfig, forest *Forest) (*NetworkNode, error) {
	ctx, cancel := context.WithCancel(context.Background())

	node := &NetworkNode{
		config:     config,
		forest:     forest,
		peers:      make(map[string]*peer),
		ctx:        ctx,
		cancel:     cancel,
		merkleTree: NewMerkleTree(),
		metrics:    NewMetrics(),
	}

	// Start listener
	listener, err := net.Listen("tcp", config.ListenAddr)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start listener: %w", err)
	}
	node.listener = listener

	// Start accept loop
	go node.acceptLoop()

	// Start peer connection loop
	go node.connectLoop()

	// Start sync loop
	go node.syncLoop()

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
				errnie.Error(err)
				continue
			}
		}
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
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

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
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		errnie.Error(err)
		return
	}

	// Create transport and RPC connection
	transport := rpc.NewStreamTransport(conn)
	rpcConn := rpc.NewConn(transport, nil)

	// Get the bootstrap interface which should be a RadixRPC
	client := RadixRPC(rpcConn.Bootstrap(n.ctx))

	p := &peer{
		addr:    addr,
		conn:    conn,
		rpcConn: rpcConn,
		client:  client,
	}

	n.peersMutex.Lock()
	n.peers[addr] = p
	n.peersMutex.Unlock()

	// Wait for connection to close
	<-rpcConn.Done()

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
	defer n.peersMutex.RUnlock()

	// Update merkle root and get current log state
	n.updateMerkleRoot()
	currentTerm := n.election.getCurrentTerm()
	lastLogIndex := n.election.getLastLogIndex()

	for _, p := range n.peers {
		go func(peer *peer) {
			future, release := peer.client.Sync(n.ctx, func(p RadixRPC_sync_Params) error {
				if err := p.SetMerkleRoot(n.merkleTree.Root.Hash); err != nil {
					return err
				}
				p.SetTerm(currentTerm)
				p.SetLogIndex(lastLogIndex)
				return nil
			})
			defer release()

			result, err := future.Struct()
			if err != nil {
				errnie.Error(err)
				return
			}

			diff, err := result.Diff()
			if err != nil {
				errnie.Error(err)
				return
			}

			// Apply received entries
			entries, err := diff.Entries()
			if err != nil {
				errnie.Error(err)
				return
			}

			totalBytes := 0
			for i := 0; i < entries.Len(); i++ {
				entry := entries.At(i)
				key, err := entry.Key()
				if err != nil {
					errnie.Error(err)
					continue
				}

				artifact, err := entry.Artifact()
				if err != nil {
					errnie.Error(err)
					continue
				}

				data := artifact.ToPtr().Data()
				totalBytes += len(key) + len(data)
				n.forest.Insert(key, data)
			}

			n.metrics.RecordSync(time.Since(start), totalBytes)
		}(p)
	}
}

/*
Insert implements RadixRPC_Server.Insert.
It handles insertion of new data into the local tree and merkle tree,
ensuring consistency across the distributed system.
*/
func (n *NetworkNode) Insert(ctx context.Context, call RadixRPC_insert) error {
	args := call.Args()
	key, err := args.Key()
	if err != nil {
		return err
	}

	artifact, err := args.Artifact()
	if err != nil {
		return err
	}

	term := args.Term()
	index := args.LogIndex()

	// Validate term/index
	if term < n.election.getCurrentTerm() {
		return fmt.Errorf("stale term")
	}

	data := artifact.ToPtr().Data()

	// Update local state with log tracking
	n.forest.Insert(key, data)
	n.merkleTree.Insert(key, data)
	n.merkleTree.Rebuild()
	n.election.updateLogState(index, term)
	n.updateMerkleRoot()

	result, err := call.AllocResults()
	if err != nil {
		return err
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

	// Step down if peer term is higher
	if peerTerm > n.election.getCurrentTerm() {
		n.election.stepDown(peerTerm)
		return fmt.Errorf("outdated term")
	}

	result, err := call.AllocResults()
	if err != nil {
		return err
	}

	// If merkle roots match and log indices align, no sync needed
	if bytes.Equal(peerRoot, n.merkleTree.Root.Hash) &&
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

	// Get differences using Merkle tree
	otherTree := NewMerkleTree()
	diffs := n.merkleTree.GetDiff(otherTree)

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

		value, ok := n.forest.getFastestTree().Get(d.Key)
		if !ok {
			continue
		}

		artifact := datura.New(
			datura.WithBytes(value),
		)

		entry.SetArtifact(*artifact)
	}

	diff.SetEntries(entries)
	diff.SetMerkleRoot(n.merkleTree.Root.Hash)
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
	// Similar to Sync but sends complete state
	return n.Sync(ctx, RadixRPC_sync(call))
}

/*
updateMerkleRoot updates the merkle root hash of the tree.
It rebuilds the merkle tree from the current state of the fastest tree
to ensure an accurate representation of the data.
*/
func (n *NetworkNode) updateMerkleRoot() {
	tree := n.forest.getFastestTree()
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
				artifact := datura.New(
					datura.WithBytes(value),
				)
				if err := p.SetArtifact(*artifact); err != nil {
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
Close shuts down the network node.
It properly closes all peer connections and releases resources.
*/
func (n *NetworkNode) Close() error {
	n.cancel()

	if n.listener != nil {
		n.listener.Close()
	}

	n.peersMutex.Lock()
	defer n.peersMutex.Unlock()

	for _, p := range n.peers {
		p.rpcConn.Close()
		p.conn.Close()
	}

	return nil
}

/*
GetMetrics returns the current metrics.
It provides a snapshot of the node's operational metrics including
peer count and other performance indicators.
*/
func (n *NetworkNode) GetMetrics() map[string]interface{} {
	n.peersMutex.RLock()
	n.metrics.UpdatePeerCount(int32(len(n.peers)))
	n.peersMutex.RUnlock()
	return n.metrics.GetMetrics()
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

	// Let election manager handle the vote request
	voteGranted := n.election.handleVoteRequest(term, candidateId, lastLogIndex, term)

	result, err := call.AllocResults()
	if err != nil {
		return err
	}

	result.SetTerm(term)
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
