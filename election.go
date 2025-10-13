/*
package dmt implements leader election functionality for the distributed radix tree.
It uses a Raft-like consensus algorithm to maintain a consistent leader across the
network and handle leader failures gracefully.
*/
package dmt

import (
	"math/rand"
	"sync"
	"time"
)

/*
NodeState represents the current state of a node in the election process.
A node can be in one of three states: Follower, Candidate, or Leader,
following the Raft consensus algorithm.
*/
type NodeState int

const (
	Follower NodeState = iota
	Candidate
	Leader
)

/*
ElectionConfig holds configuration for leader election.
It defines timeouts, intervals, and quorum requirements that control
the behavior of the election process.
*/
type ElectionConfig struct {
	// Base timeout for elections (will be randomized)
	ElectionTimeout time.Duration
	// How often to send heartbeats when leader
	HeartbeatInterval time.Duration
	// Minimum number of nodes needed for election
	QuorumSize int
}

/*
RaftLogEntry represents a single entry in the Raft log.
Each entry contains the term when it was created, its index in the log,
and the key-value data to be applied to the state machine.
*/
type RaftLogEntry struct {
	Term  uint64
	Index uint64
	Key   []byte
	Value []byte
}

/*
Election manages the leader election process.
It implements a Raft-like consensus algorithm to maintain a consistent
leader across the distributed system, handling state transitions,
vote counting, and heartbeat mechanisms.
*/
type Election struct {
	config ElectionConfig
	node   *NetworkNode

	// Election state
	state     NodeState
	term      uint64
	votedFor  string
	stateLock sync.RWMutex

	// Log tracking
	lastLogTerm  uint64
	lastLogIndex uint64
	logLock      sync.RWMutex

	// Raft log and commit tracking
	log         []RaftLogEntry
	commitIndex uint64
	lastApplied uint64
	logMutex    sync.RWMutex

	// Election timers
	electionTimer  *time.Timer
	heartbeatTimer *time.Timer
	timerMutex     sync.Mutex

	// Control channels
	votes    chan string
	shutdown chan struct{}
}

/*
NewElection creates a new election manager.
It initializes the election state machine with the provided configuration
and network node, starting in the Follower state.
*/
func NewElection(config ElectionConfig, node *NetworkNode) *Election {
	e := &Election{
		config:      config,
		node:        node,
		state:       Follower,
		log:         make([]RaftLogEntry, 0),
		commitIndex: 0,
		lastApplied: 0,
		votes:       make(chan string, 100),
		shutdown:    make(chan struct{}),
	}

	// Register this election instance with the node so node-level maintenance
	// (e.g., cleanupElectionLog) operates on the correct election state.
	node.election = e

	// Start election management
	go e.run()

	// Start apply loop for committed entries
	go e.applyLoop()

	return e
}

/*
run manages the election state machine.
It handles timer events, vote processing, and heartbeat sending
in a continuous loop until shutdown.
*/
func (e *Election) run() {
	e.resetElectionTimer()

	var hbC <-chan time.Time
	for {
		// Safely get heartbeat channel
		hbC = e.getHeartbeatChannel()

		select {
		case <-e.shutdown:
			return

		case <-e.electionTimer.C:
			e.startElection()

		case voter := <-e.votes:
			e.handleVote(voter)

		case <-hbC:
			e.sendHeartbeats()
		}
	}
}

/*
getHeartbeatChannel safely returns the heartbeat timer channel.
It returns nil if not a leader or if the timer is not set.
*/
func (e *Election) getHeartbeatChannel() <-chan time.Time {
	if e.getState() != Leader {
		return nil
	}

	e.timerMutex.Lock()
	defer e.timerMutex.Unlock()

	if e.heartbeatTimer != nil {
		return e.heartbeatTimer.C
	}
	return nil
}

/*
startElection initiates a new election.
It transitions the node to Candidate state, increments the term,
and requests votes from all peers. If sufficient votes are received,
the node becomes the leader.
*/
func (e *Election) startElection() {
	e.stateLock.Lock()
	e.state = Candidate
	e.term++
	e.votedFor = e.node.config.NodeID
	currentTerm := e.term
	e.stateLock.Unlock()

	// Update metrics
	e.node.metrics.SetLeader(false)
	e.node.metrics.RecordLeaderElection()

	// Request votes from all peers
	e.node.peersMutex.RLock()
	peers := make([]*peer, 0, len(e.node.peers))
	for _, p := range e.node.peers {
		peers = append(peers, p)
	}
	e.node.peersMutex.RUnlock()

	// Track votes received (including self-vote)
	votesReceived := 1
	// Use configured quorum size when set; otherwise majority of cluster
	votesNeeded := e.config.QuorumSize
	if votesNeeded <= 0 {
		votesNeeded = ((len(peers) + 1) / 2) + 1
	}

	// Request votes from all peers
	for _, p := range peers {
		go func(peer *peer) {
			future, release := peer.client.RequestVote(e.node.ctx, func(p RadixRPC_requestVote_Params) error {
				p.SetTerm(currentTerm)
				p.SetCandidateId(e.node.config.NodeID)
				p.SetLastLogIndex(e.getLastLogIndex())
				p.SetLastLogTerm(e.getLastLogTerm())
				return nil
			})
			defer release()

			result, err := future.Struct()
			if err != nil {
				return
			}

			// Check if peer has higher term
			if result.Term() > currentTerm {
				e.stepDown(result.Term())
				return
			}

			if result.VoteGranted() {
				e.votes <- peer.addr
			}
		}(p)
	}

	// Wait for votes or timeout
	timeout := time.After(e.config.ElectionTimeout)
	for votesReceived < votesNeeded {
		select {
		case <-e.votes:
			votesReceived++
		case <-timeout:
			return
		case <-e.shutdown:
			return
		}
	}

	// Won election - but check if we're still a candidate
	if votesReceived >= votesNeeded && e.getState() == Candidate {
		e.node.metrics.RecordLeaderElection()
		e.becomeLeader()
	}
}

/*
becomeLeader transitions the node to leader state.
It updates the node's state to Leader, starts the heartbeat timer,
and updates relevant metrics.
*/
func (e *Election) becomeLeader() {
	e.stateLock.Lock()
	e.state = Leader
	e.stateLock.Unlock()

	// Update metrics
	e.node.metrics.SetLeader(true)

	// Start heartbeat timer safely
	e.resetHeartbeatTimer()
}

/*
sendHeartbeats sends heartbeat messages to all peers.
Leaders periodically send heartbeats to maintain their authority
and prevent new elections from being started. Heartbeats also
propagate the commit index to followers.
*/
func (e *Election) sendHeartbeats() {
	e.node.peersMutex.RLock()
	peers := make([]*peer, 0, len(e.node.peers))
	for _, p := range e.node.peers {
		peers = append(peers, p)
	}
	e.node.peersMutex.RUnlock()

	currentCommitIndex := e.getCommitIndex()

	// Record heartbeat metrics
	e.node.metrics.RecordHeartbeatSent()

	for _, p := range peers {
		go func(peer *peer) {
			// Use Sync RPC to send commit index information
			future, release := peer.client.Sync(e.node.ctx, func(sp RadixRPC_sync_Params) error {
				sp.SetMerkleRoot([]byte{}) // Empty for heartbeat
				sp.SetTerm(e.term)
				sp.SetLogIndex(currentCommitIndex) // Send commit index
				return nil
			})
			defer release()

			result, err := future.Struct()
			if err != nil {
				e.node.metrics.RecordError("heartbeat", "network_error")
				return
			}

			// Check term in response diff
			diff, err := result.Diff()
			if err != nil {
				e.node.metrics.RecordError("heartbeat", "response_parse_error")
				return
			}

			if diff.Term() > e.term {
				e.stepDown(diff.Term())
			}
		}(p)
	}

	// Reset heartbeat timer safely
	e.resetHeartbeatTimer()
}

/*
stepDown steps down from leader/candidate to follower.
This occurs when a node discovers a higher term or when it needs
to relinquish leadership for other reasons.
*/
func (e *Election) stepDown(newTerm uint64) {
	e.stateLock.Lock()
	e.stepDownLocked(newTerm)
	e.stateLock.Unlock()

	// Update metrics
	e.node.metrics.SetLeader(false)

	// Reset election timer
	e.resetElectionTimer()
}

/*
stepDownLocked steps down from leader/candidate to follower.
This internal method assumes the stateLock is already held by the caller.
*/
func (e *Election) stepDownLocked(newTerm uint64) {
	e.state = Follower
	e.term = newTerm
	e.votedFor = ""
}

/*
resetElectionTimer resets the election timeout with random jitter.
The randomization helps prevent split votes by ensuring nodes don't
all timeout at the same time.
*/
func (e *Election) resetElectionTimer() {
	e.timerMutex.Lock()
	defer e.timerMutex.Unlock()

	if e.electionTimer != nil {
		e.electionTimer.Stop()
		// Drain the channel to prevent spurious events
		select {
		case <-e.electionTimer.C:
		default:
		}
	}

	// Add random jitter to election timeout
	jitter := time.Duration(rand.Int63n(int64(e.config.ElectionTimeout)))
	timeout := e.config.ElectionTimeout + jitter

	e.electionTimer = time.NewTimer(timeout)
}

/*
resetHeartbeatTimer safely resets the heartbeat timer.
This method handles concurrent access to the heartbeat timer.
*/
func (e *Election) resetHeartbeatTimer() {
	e.timerMutex.Lock()
	defer e.timerMutex.Unlock()

	if e.heartbeatTimer != nil {
		e.heartbeatTimer.Stop()
		// Drain the channel to prevent spurious events
		select {
		case <-e.heartbeatTimer.C:
		default:
		}
	}

	e.heartbeatTimer = time.NewTimer(e.config.HeartbeatInterval)
}

/*
getState returns the current node state.
It provides thread-safe access to the node's current role in the
election process.
*/
func (e *Election) getState() NodeState {
	e.stateLock.RLock()
	defer e.stateLock.RUnlock()
	return e.state
}

/*
handleVote processes a vote received from a peer.
It updates voting metrics and counts votes during an election,
but only if the node is still a candidate.
*/
func (e *Election) handleVote(voter string) {
	e.stateLock.Lock()
	defer e.stateLock.Unlock()

	// Only count votes if still a candidate
	if e.state != Candidate {
		return
	}

	// Record vote in metrics
	e.node.metrics.RecordVote(voter)
}

/*
handleVoteRequest processes a vote request from a candidate.
It implements the Raft voting rules, checking term numbers and log
indices to decide whether to grant the vote.
*/
func (e *Election) handleVoteRequest(term uint64, candidateId string, lastLogIndex uint64, lastLogTerm uint64) bool {
	// Read current state without holding write lock
	e.stateLock.RLock()
	curTerm := e.term
	votedFor := e.votedFor
	e.stateLock.RUnlock()

	// Step down outside of lock if term is newer
	if term > curTerm {
		e.stepDown(term)
		// refresh local view
		e.stateLock.RLock()
		curTerm = e.term
		votedFor = e.votedFor
		e.stateLock.RUnlock()
	}

	// Check term and whether we've voted this term
	if term < curTerm || (votedFor != "" && votedFor != candidateId) {
		return false
	}

	// Check if candidate's log is at least as up-to-date as ours
	e.logLock.RLock()
	logOK := lastLogTerm > e.lastLogTerm ||
		(lastLogTerm == e.lastLogTerm && lastLogIndex >= e.lastLogIndex)
	e.logLock.RUnlock()

	if !logOK {
		return false
	}

	// Grant vote and reset timer
	e.stateLock.Lock()
	e.votedFor = candidateId
	e.stateLock.Unlock()
	e.resetElectionTimer()
	return true
}

/*
handleHeartbeat processes a heartbeat from the leader.
It updates terms if necessary, resets election timeouts, and
maintains the leader-follower relationship in the cluster.
*/
func (e *Election) handleHeartbeat(term uint64, leaderId string) bool {
	// Record heartbeat received
	e.node.metrics.RecordHeartbeatReceived()

	// Read current term safely
	e.stateLock.RLock()
	curTerm := e.term
	role := e.state
	e.stateLock.RUnlock()

	// Step down outside lock if higher term
	if term > curTerm {
		e.stepDown(term)
		e.node.metrics.SetNodeRole("follower", 0.0)
		return true
	}

	// Reject if term is lower
	if term < curTerm {
		return false
	}

	// Accept heartbeat if term matches and from valid leader
	if role != Leader && leaderId != "" {
		e.resetElectionTimer()
		e.node.metrics.termNumber.Store(term)
		e.node.metrics.SetNodeRole("follower", 0.0)
		return true
	}

	return false
}

/*
Close shuts down the election manager.
It signals the run loop to stop and cleans up resources.
*/
func (e *Election) Close() {
	select {
	case <-e.shutdown:
		// Already closed
		return
	default:
		close(e.shutdown)
	}

	e.timerMutex.Lock()
	defer e.timerMutex.Unlock()

	if e.electionTimer != nil {
		e.electionTimer.Stop()
	}
	if e.heartbeatTimer != nil {
		e.heartbeatTimer.Stop()
	}
}

/*
Update log state when applying new entries
*/
func (e *Election) updateLogState(index uint64, term uint64) {
	e.logLock.Lock()
	defer e.logLock.Unlock()

	if index > e.lastLogIndex {
		e.lastLogIndex = index
		e.lastLogTerm = term
	}
}

// getCurrentTerm returns the current term number
func (e *Election) getCurrentTerm() uint64 {
	e.stateLock.RLock()
	defer e.stateLock.RUnlock()
	return e.term
}

// getLastLogIndex returns the index of the last log entry
func (e *Election) getLastLogIndex() uint64 {
	e.logLock.RLock()
	defer e.logLock.RUnlock()
	return e.lastLogIndex
}

// getLastLogTerm returns the term of the last log entry
func (e *Election) getLastLogTerm() uint64 {
	e.logLock.RLock()
	defer e.logLock.RUnlock()
	return e.lastLogTerm
}

// isLeader returns true if this node is currently the leader
func (e *Election) isLeader() bool {
	e.stateLock.RLock()
	defer e.stateLock.RUnlock()
	return e.state == Leader
}

/*
appendLogEntry adds a new entry to the log.
It assigns the next available index and the current term to the entry.
*/
func (e *Election) appendLogEntry(key, value []byte) uint64 {
	// Prepare defensive copies
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	valueCopy := make([]byte, len(value))
	copy(valueCopy, value)

	// Append under logMutex
	e.logMutex.Lock()
	// Determine new index based on current log length
	newIndex := uint64(len(e.log)) + 1
	entry := RaftLogEntry{
		Term:  e.getCurrentTerm(),
		Index: newIndex,
		Key:   keyCopy,
		Value: valueCopy,
	}
	e.log = append(e.log, entry)
	e.logMutex.Unlock()

	// Publish updated log state under logLock to avoid races with readers
	e.updateLogState(newIndex, entry.Term)

	return newIndex
}

/*
getLogEntry returns the log entry at the specified index.
Returns nil if the index is out of bounds.
*/
func (e *Election) getLogEntry(index uint64) *RaftLogEntry {
	e.logMutex.RLock()
	defer e.logMutex.RUnlock()

	if index == 0 || index > uint64(len(e.log)) {
		return nil
	}

	return &e.log[index-1] // log is 0-indexed, but indices are 1-indexed
}

/*
handleAppendEntries processes AppendEntries RPC from the leader.
It implements the Raft log replication protocol with proper consistency checks.
*/
func (e *Election) handleAppendEntries(term uint64, _ string, prevLogIndex, prevLogTerm uint64, entries LogEntry_List, leaderCommit uint64) bool {
	// Record AppendEntries received
	e.node.metrics.RecordAppendEntriesReceived()

	// Step down if we see a higher term
	if term > e.getCurrentTerm() {
		e.stepDown(term)
	}

	// Reject if term is lower than current term
	if term < e.getCurrentTerm() {
		return false
	}

	// Reset election timer since we heard from the leader
	e.resetElectionTimer()

	// Check if our log contains an entry at prevLogIndex with matching term
	if prevLogIndex > 0 {
		prevEntry := e.getLogEntry(prevLogIndex)
		if prevEntry == nil || prevEntry.Term != prevLogTerm {
			return false // Log consistency check failed
		}
	}

	// Process new entries
	e.logMutex.Lock()

	// If an existing entry conflicts with a new one (same index but different terms),
	// delete the existing entry and all that follow it
	for i := 0; i < entries.Len(); i++ {
		entry := entries.At(i)
		entryIndex := entry.Index()
		entryTerm := entry.Term()

		if entryIndex <= uint64(len(e.log)) {
			existingEntry := &e.log[entryIndex-1]
			if existingEntry.Term != entryTerm {
				// Conflict found, truncate log from this point
				e.log = e.log[:entryIndex-1]
				// Publish updated last index/term via updateLogState
				var termAfter uint64
				if entryIndex > 1 {
					termAfter = e.log[entryIndex-2].Term
				}
				e.updateLogState(entryIndex-1, termAfter)
				break
			}
		}
	}

	// Append new entries that aren't already in the log
	for i := 0; i < entries.Len(); i++ {
		entry := entries.At(i)
		entryIndex := entry.Index()

		if entryIndex > uint64(len(e.log)) {
			key, _ := entry.Key()
			value, _ := entry.Value()

			// Defensive copies to avoid Cap'n Proto buffer reuse
			var keyCopy, valueCopy []byte
			if len(key) > 0 {
				keyCopy = append([]byte(nil), key...)
			}
			if len(value) > 0 {
				valueCopy = append([]byte(nil), value...)
			}

			logEntry := RaftLogEntry{
				Term:  entry.Term(),
				Index: entryIndex,
				Key:   keyCopy,
				Value: valueCopy,
			}

			e.log = append(e.log, logEntry)
			// Immediately publish state under logLock
			e.updateLogState(entryIndex, entry.Term())
		}
	}

	e.logMutex.Unlock()

	// Update commit index
	if leaderCommit > e.commitIndex {
		e.logMutex.Lock()
		e.commitIndex = min(leaderCommit, uint64(len(e.log)))
		e.logMutex.Unlock()

		// Apply committed entries
		go e.applyCommittedEntries()
	}

	return true
}

/*
applyCommittedEntries applies committed but unapplied log entries to the state machine.
*/
func (e *Election) applyCommittedEntries() {
	e.logMutex.Lock()

	// Check if there's work to do before proceeding
	if e.lastApplied >= e.commitIndex {
		e.logMutex.Unlock()
		return
	}

	// Apply all committed but unapplied entries
	for e.lastApplied < e.commitIndex {
		e.lastApplied++

		// Ensure we don't go beyond the log
		if e.lastApplied > uint64(len(e.log)) {
			e.lastApplied = uint64(len(e.log))
			break
		}

		entry := e.log[e.lastApplied-1] // Convert to 0-indexed

		// Apply to state machine (forest) - release lock during application
		e.logMutex.Unlock()

		if e.node.forest != nil {
			// Bypass leader check for applying committed entries
			e.node.forest.mu.Lock()
			tree := e.node.forest.tree
			if tree != nil {
				tree.Insert(entry.Key, entry.Value)
			}
			e.node.forest.mu.Unlock()

			// Update merkle tree
			e.node.merkleTree.Insert(entry.Key, entry.Value)
			e.node.merkleTree.Rebuild()
		} else {

		}

		e.logMutex.Lock()
	}

	e.logMutex.Unlock()
}

/*
updateCommitIndex updates the commit index and triggers application of committed entries.
*/
func (e *Election) updateCommitIndex(newCommitIndex uint64) {
	e.logMutex.Lock()
	if newCommitIndex > e.commitIndex {
		e.commitIndex = newCommitIndex
		e.logMutex.Unlock()
		go e.applyCommittedEntries()
	} else {
		e.logMutex.Unlock()
	}
}

/*
getCommitIndex returns the current commit index.
*/
func (e *Election) getCommitIndex() uint64 {
	e.logMutex.RLock()
	defer e.logMutex.RUnlock()
	return e.commitIndex
}

/*
getLastApplied returns the index of the last applied log entry.
*/
func (e *Election) getLastApplied() uint64 {
	e.logMutex.RLock()
	defer e.logMutex.RUnlock()
	return e.lastApplied
}

/*
applyLoop continuously applies committed but unapplied log entries to the state machine.
It runs in a separate goroutine and processes entries as they become committed.
*/
func (e *Election) applyLoop() {
	ticker := time.NewTicker(10 * time.Millisecond) // Check for new commits frequently
	defer ticker.Stop()

	for {
		select {
		case <-e.shutdown:
			return
		case <-ticker.C:
			e.applyCommittedEntries()
		}
	}
}

// Helper function for min
func min(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
