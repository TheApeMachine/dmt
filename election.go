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

	// Election timers
	electionTimer  *time.Timer
	heartbeatTimer *time.Timer

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
		config:   config,
		node:     node,
		state:    Follower,
		votes:    make(chan string, 100),
		shutdown: make(chan struct{}),
	}

	// Start election management
	go e.run()

	return e
}

/*
run manages the election state machine.
It handles timer events, vote processing, and heartbeat sending
in a continuous loop until shutdown.
*/
func (e *Election) run() {
	e.resetElectionTimer()

	for {
		select {
		case <-e.shutdown:
			return

		case <-e.electionTimer.C:
			e.startElection()

		case voter := <-e.votes:
			e.handleVote(voter)

		case <-e.heartbeatTimer.C:
			if e.getState() == Leader {
				e.sendHeartbeats()
			}
		}
	}
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

	// Request votes from all peers
	e.node.peersMutex.RLock()
	peers := make([]*peer, 0, len(e.node.peers))
	for _, p := range e.node.peers {
		peers = append(peers, p)
	}
	e.node.peersMutex.RUnlock()

	// Track votes received (including self-vote)
	votesReceived := 1
	votesNeeded := (len(peers) / 2) + 1

	// Request votes from all peers
	for _, p := range peers {
		go func(peer *peer) {
			future, release := peer.client.RequestVote(e.node.ctx, func(p RadixRPC_requestVote_Params) error {
				p.SetTerm(currentTerm)
				p.SetCandidateId(e.node.config.NodeID)
				p.SetLastLogIndex(uint64(len(e.node.merkleTree.Root.Hash)))
				return nil
			})
			defer release()

			result, err := future.Struct()
			if err != nil {
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

	// Won election
	if votesReceived >= votesNeeded {
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

	// Start heartbeat timer
	e.heartbeatTimer = time.NewTimer(e.config.HeartbeatInterval)
}

/*
sendHeartbeats sends heartbeat messages to all peers.
Leaders periodically send heartbeats to maintain their authority
and prevent new elections from being started.
*/
func (e *Election) sendHeartbeats() {
	e.node.peersMutex.RLock()
	peers := make([]*peer, 0, len(e.node.peers))
	for _, p := range e.node.peers {
		peers = append(peers, p)
	}
	e.node.peersMutex.RUnlock()

	for _, p := range peers {
		go func(peer *peer) {
			future, release := peer.client.Heartbeat(e.node.ctx, func(p RadixRPC_heartbeat_Params) error {
				p.SetTerm(e.term)
				p.SetLeaderId(e.node.config.NodeID)
				return nil
			})
			defer release()

			result, err := future.Struct()
			if err != nil {
				return
			}

			// Step down if peer has higher term
			if result.Term() > e.term {
				e.stepDown(result.Term())
			}
		}(p)
	}

	// Reset heartbeat timer
	e.heartbeatTimer.Reset(e.config.HeartbeatInterval)
}

/*
stepDown steps down from leader/candidate to follower.
This occurs when a node discovers a higher term or when it needs
to relinquish leadership for other reasons.
*/
func (e *Election) stepDown(newTerm uint64) {
	e.stateLock.Lock()
	e.state = Follower
	e.term = newTerm
	e.votedFor = ""
	e.stateLock.Unlock()

	// Update metrics
	e.node.metrics.SetLeader(false)

	// Reset election timer
	e.resetElectionTimer()
}

/*
resetElectionTimer resets the election timeout with random jitter.
The randomization helps prevent split votes by ensuring nodes don't
all timeout at the same time.
*/
func (e *Election) resetElectionTimer() {
	if e.electionTimer != nil {
		e.electionTimer.Stop()
	}

	// Add random jitter to election timeout
	jitter := time.Duration(rand.Int63n(int64(e.config.ElectionTimeout)))
	timeout := e.config.ElectionTimeout + jitter

	e.electionTimer = time.NewTimer(timeout)
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
	e.stateLock.Lock()
	defer e.stateLock.Unlock()

	// Step down if term is newer
	if term > e.term {
		e.stepDown(term)
	}

	// Check term and whether we've voted this term
	if term < e.term || (e.votedFor != "" && e.votedFor != candidateId) {
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

	// Grant vote
	e.votedFor = candidateId
	e.resetElectionTimer()
	return true
}

/*
handleHeartbeat processes a heartbeat from the leader.
It updates terms if necessary, resets election timeouts, and
maintains the leader-follower relationship in the cluster.
*/
func (e *Election) handleHeartbeat(term uint64, leaderId string) bool {
	e.stateLock.Lock()
	defer e.stateLock.Unlock()

	// Step down if term is higher
	if term > e.term {
		e.stepDown(term)
		// Update metrics with new leader
		e.node.metrics.SetNodeRole("follower", 0.0)
		return true
	}

	// Reject if term is lower
	if term < e.term {
		return false
	}

	// Accept heartbeat if term matches and from valid leader
	if e.state != Leader && leaderId != "" {
		e.resetElectionTimer()
		// Update metrics
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
	close(e.shutdown)
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
