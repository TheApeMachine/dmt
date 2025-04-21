/*
package dmt implements metrics tracking for the radix tree system.
This includes performance metrics, operational counters, and network statistics
that help monitor and optimize the distributed tree's behavior.
*/
package dmt

import (
	"sync"
	"sync/atomic"
	"time"
)

/*
Metrics tracks performance and operational metrics for the radix tree.
It maintains atomic counters for operations, latency tracking for performance
measurement, and various network and election-related metrics for distributed
operation monitoring.
*/
type Metrics struct {
	// Operation counters
	insertCount   atomic.Uint64
	lookupCount   atomic.Uint64
	syncCount     atomic.Uint64
	conflictCount atomic.Uint64

	// Election metrics
	votesReceived atomic.Uint64
	termNumber    atomic.Uint64
	lastVoter     string

	// Latency tracking
	insertLatency  *LatencyTracker
	lookupLatency  *LatencyTracker
	syncLatency    *LatencyTracker
	networkLatency *LatencyTracker

	// Network metrics
	bytesTransmitted atomic.Uint64
	bytesReceived    atomic.Uint64
	peerCount        atomic.Int32

	// Node status
	isLeader     atomic.Bool
	nodeRole     string
	nodeWeight   float64
	lastSyncTime time.Time
	mu           sync.RWMutex
}

/*
LatencyTracker maintains a rolling window of operation latencies.
It provides thread-safe tracking of operation durations and calculates
average latency over the window period.
*/
type LatencyTracker struct {
	window []time.Duration
	mu     sync.RWMutex
	size   int
	pos    int
}

/*
NewMetrics creates a new metrics tracker with initialized latency trackers
for various operation types. Each latency tracker maintains a window of
100 measurements.
*/
func NewMetrics() *Metrics {
	return &Metrics{
		insertLatency:  NewLatencyTracker(100),
		lookupLatency:  NewLatencyTracker(100),
		syncLatency:    NewLatencyTracker(100),
		networkLatency: NewLatencyTracker(100),
	}
}

/*
NewLatencyTracker creates a new latency tracker with the specified window size.
The window size determines how many measurements are kept for calculating
the rolling average.
*/
func NewLatencyTracker(size int) *LatencyTracker {
	return &LatencyTracker{
		window: make([]time.Duration, size),
		size:   size,
	}
}

/*
RecordLatency adds a new latency measurement to the tracker.
It stores the duration in the rolling window and updates the position
for the next measurement.
*/
func (lt *LatencyTracker) RecordLatency(d time.Duration) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	lt.window[lt.pos] = d
	lt.pos = (lt.pos + 1) % lt.size
}

/*
AverageLatency returns the average latency over the window.
It calculates the mean of all non-zero measurements in the window,
providing a rolling average of operation latencies.
*/
func (lt *LatencyTracker) AverageLatency() time.Duration {
	lt.mu.RLock()
	defer lt.mu.RUnlock()
	var sum time.Duration
	count := 0
	for _, d := range lt.window {
		if d > 0 {
			sum += d
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / time.Duration(count)
}

/*
RecordInsert records metrics for an insert operation.
It updates the insert counter, records the operation latency, and
tracks the number of bytes transmitted.
*/
func (m *Metrics) RecordInsert(duration time.Duration, bytes int) {
	m.insertCount.Add(1)
	m.insertLatency.RecordLatency(duration)
	m.bytesTransmitted.Add(uint64(bytes))
}

/*
RecordLookup records metrics for a lookup operation.
It updates the lookup counter and records the operation latency.
*/
func (m *Metrics) RecordLookup(duration time.Duration) {
	m.lookupCount.Add(1)
	m.lookupLatency.RecordLatency(duration)
}

/*
RecordSync records metrics for a sync operation.
It updates the sync counter, records the operation latency,
tracks received bytes, and updates the last sync timestamp.
*/
func (m *Metrics) RecordSync(duration time.Duration, bytes int) {
	m.syncCount.Add(1)
	m.syncLatency.RecordLatency(duration)
	m.bytesReceived.Add(uint64(bytes))
	m.mu.Lock()
	m.lastSyncTime = time.Now()
	m.mu.Unlock()
}

/*
RecordConflict records a detected conflict during operations.
It increments the conflict counter for monitoring consistency issues.
*/
func (m *Metrics) RecordConflict() {
	m.conflictCount.Add(1)
}

/*
UpdatePeerCount updates the current peer count.
It atomically stores the new count of connected peers.
*/
func (m *Metrics) UpdatePeerCount(count int32) {
	m.peerCount.Store(count)
}

/*
SetNodeRole updates the node's role and weight in the network.
It stores the role string and associated weight value for metrics reporting.
*/
func (m *Metrics) SetNodeRole(role string, weight float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodeRole = role
	m.nodeWeight = weight
}

/*
SetLeader updates the node's leader status.
It atomically stores whether this node is currently the leader.
*/
func (m *Metrics) SetLeader(isLeader bool) {
	m.isLeader.Store(isLeader)
}

/*
RecordVote records a vote received during election.
It increments the votes received counter and updates the last voter.
*/
func (m *Metrics) RecordVote(voter string) {
	m.votesReceived.Add(1)
	m.mu.Lock()
	m.lastVoter = voter
	m.mu.Unlock()
}

/*
GetMetrics returns a snapshot of current metrics.
It provides a comprehensive view of the node's operational state,
including performance metrics, election status, and network statistics.
*/
func (m *Metrics) GetMetrics() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return map[string]interface{}{
		"operations": map[string]uint64{
			"insert":   m.insertCount.Load(),
			"lookup":   m.lookupCount.Load(),
			"sync":     m.syncCount.Load(),
			"conflict": m.conflictCount.Load(),
		},
		"election": map[string]interface{}{
			"votes_received": m.votesReceived.Load(),
			"term_number":    m.termNumber.Load(),
			"last_voter":     m.lastVoter,
		},
		"latencies": map[string]float64{
			"insert":  float64(m.insertLatency.AverageLatency()) / float64(time.Millisecond),
			"lookup":  float64(m.lookupLatency.AverageLatency()) / float64(time.Millisecond),
			"sync":    float64(m.syncLatency.AverageLatency()) / float64(time.Millisecond),
			"network": float64(m.networkLatency.AverageLatency()) / float64(time.Millisecond),
		},
		"network": map[string]interface{}{
			"bytes_tx":   m.bytesTransmitted.Load(),
			"bytes_rx":   m.bytesReceived.Load(),
			"peer_count": m.peerCount.Load(),
		},
		"node": map[string]interface{}{
			"is_leader":      m.isLeader.Load(),
			"role":           m.nodeRole,
			"weight":         m.nodeWeight,
			"last_sync_time": m.lastSyncTime,
		},
	}
}
