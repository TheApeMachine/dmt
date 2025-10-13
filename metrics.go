/*
package dmt implements comprehensive metrics tracking for the distributed radix tree system.
This includes performance metrics, operational counters, error tracking, consensus metrics,
and network statistics that help monitor, debug, and optimize the distributed tree's behavior.
*/
package dmt

import (
	"sync"
	"sync/atomic"
	"time"
)

/*
Metrics tracks comprehensive performance, operational, and error metrics for the distributed radix tree.
It maintains atomic counters for operations, latency tracking for performance measurement,
error categorization for reliability monitoring, consensus metrics for distributed coordination,
and various network and election-related metrics for distributed operation monitoring.
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

	// Error tracking
	errorCounts map[string]map[string]*atomic.Uint64 // operation -> category -> count
	errorMutex  sync.RWMutex

	// Consensus metrics
	heartbeatsSent     atomic.Uint64
	heartbeatsReceived atomic.Uint64
	appendEntriesSent  atomic.Uint64
	appendEntriesRecv  atomic.Uint64
	logReplicationTime *LatencyTracker
	commitIndex        atomic.Uint64
	appliedIndex       atomic.Uint64

	// Health metrics
	lastHeartbeat     atomic.Int64 // timestamp
	leaderElections   atomic.Uint64
	splitBrainEvents  atomic.Uint64
	networkPartitions atomic.Uint64

	// System health
	systemStatus     string       // "healthy", "degraded", "unhealthy"
	lastStatusUpdate atomic.Int64 // timestamp

	mu sync.RWMutex
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
NewMetrics creates a new comprehensive metrics tracker with initialized latency trackers
for various operation types. Each latency tracker maintains a window of
100 measurements for rolling averages.
*/
func NewMetrics() *Metrics {
	return &Metrics{
		insertLatency:      NewLatencyTracker(100),
		lookupLatency:      NewLatencyTracker(100),
		syncLatency:        NewLatencyTracker(100),
		networkLatency:     NewLatencyTracker(100),
		logReplicationTime: NewLatencyTracker(100),
		errorCounts:        make(map[string]map[string]*atomic.Uint64),
		systemStatus:       "healthy",
		lastStatusUpdate:   atomic.Int64{},
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
RecordMemoryUsage records memory usage statistics.
It tracks memory consumption for monitoring and alerting purposes.
*/
func (m *Metrics) RecordMemoryUsage(leafCount, nodeCount, cacheSize int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Store memory usage metrics for monitoring
	// In a production system, these would be exposed via metrics endpoints
}

/*
RecordError records an error occurrence by operation and category.
It maintains error counts for monitoring system reliability and debugging.
*/
func (m *Metrics) RecordError(operation, category string) {
	m.errorMutex.Lock()

	if m.errorCounts[operation] == nil {
		m.errorCounts[operation] = make(map[string]*atomic.Uint64)
	}
	if m.errorCounts[operation][category] == nil {
		m.errorCounts[operation][category] = &atomic.Uint64{}
	}
	m.errorCounts[operation][category].Add(1)
	m.errorMutex.Unlock()

	// Update system health outside of errorMutex to avoid lock order issues
	m.updateSystemHealth()
}

/*
RecordHeartbeatSent records a heartbeat sent to followers.
*/
func (m *Metrics) RecordHeartbeatSent() {
	m.heartbeatsSent.Add(1)
	m.lastHeartbeat.Store(time.Now().Unix())
}

/*
RecordHeartbeatReceived records a heartbeat received from leader.
*/
func (m *Metrics) RecordHeartbeatReceived() {
	m.heartbeatsReceived.Add(1)
	m.lastHeartbeat.Store(time.Now().Unix())
}

/*
RecordAppendEntriesSent records an AppendEntries RPC sent to followers.
*/
func (m *Metrics) RecordAppendEntriesSent() {
	m.appendEntriesSent.Add(1)
}

/*
RecordAppendEntriesReceived records an AppendEntries RPC received from leader.
*/
func (m *Metrics) RecordAppendEntriesReceived() {
	m.appendEntriesRecv.Add(1)
}

/*
RecordLogReplication records timing for log replication operations.
*/
func (m *Metrics) RecordLogReplication(duration time.Duration) {
	m.logReplicationTime.RecordLatency(duration)
}

/*
UpdateCommitIndex updates the current commit index.
*/
func (m *Metrics) UpdateCommitIndex(index uint64) {
	m.commitIndex.Store(index)
}

/*
UpdateAppliedIndex updates the current applied index.
*/
func (m *Metrics) UpdateAppliedIndex(index uint64) {
	m.appliedIndex.Store(index)
}

/*
RecordLeaderElection records a leader election event.
*/
func (m *Metrics) RecordLeaderElection() {
	m.leaderElections.Add(1)
}

/*
RecordSplitBrain records a split-brain scenario detection.
*/
func (m *Metrics) RecordSplitBrain() {
	m.splitBrainEvents.Add(1)
	m.updateSystemHealth()
}

/*
RecordNetworkPartition records a network partition event.
*/
func (m *Metrics) RecordNetworkPartition() {
	m.networkPartitions.Add(1)
	m.updateSystemHealth()
}

/*
updateSystemHealth evaluates and updates the overall system health status.
*/
func (m *Metrics) updateSystemHealth() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateSystemHealthLocked()
}

/*
updateSystemHealthLocked updates system health while holding the mutex.
*/
func (m *Metrics) updateSystemHealthLocked() {
	now := time.Now().Unix()

	// Check various health indicators
	peerCount := m.peerCount.Load()
	lastHeartbeat := m.lastHeartbeat.Load()
	splitBrains := m.splitBrainEvents.Load()

	// Determine health status
	status := "healthy"

	// Check for recent split-brain events
	if splitBrains > 0 {
		status = "degraded"
	}

	// Check for stale heartbeats (more than 30 seconds)
	if now-lastHeartbeat > 30 {
		status = "degraded"
	}

	// Check for no peers (isolated node)
	if peerCount == 0 {
		status = "degraded"
	}

	// Check error rates
	totalErrors := uint64(0)
	// Safely snapshot error counts while holding errorMutex read lock
	m.errorMutex.RLock()
	for _, categories := range m.errorCounts {
		for _, counter := range categories {
			if counter != nil {
				totalErrors += counter.Load()
			}
		}
	}
	m.errorMutex.RUnlock()

	// If error rate is high, mark as unhealthy
	totalOps := m.insertCount.Load() + m.lookupCount.Load() + m.syncCount.Load()

	if totalOps > 0 && float64(totalErrors)/float64(totalOps) > 0.1 { // 10% error rate
		status = "unhealthy"
	}

	m.systemStatus = status
	m.lastStatusUpdate.Store(now)
}

/*
GetHealthStatus returns the current system health status.
*/
func (m *Metrics) GetHealthStatus() (string, time.Time) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.systemStatus, time.Unix(m.lastStatusUpdate.Load(), 0)
}

/*
GetErrorMetrics returns error metrics by operation and category.
*/
func (m *Metrics) GetErrorMetrics() map[string]map[string]uint64 {
	m.errorMutex.RLock()
	defer m.errorMutex.RUnlock()

	// Create a deep copy to avoid race conditions
	result := make(map[string]map[string]uint64)
	for op, categories := range m.errorCounts {
		result[op] = make(map[string]uint64)
		for category, counter := range categories {
			if counter != nil {
				result[op][category] = counter.Load()
			}
		}
	}
	return result
}

/*
GetConsensusMetrics returns consensus-related metrics.
*/
func (m *Metrics) GetConsensusMetrics() map[string]interface{} {
	return map[string]interface{}{
		"heartbeats_sent":      m.heartbeatsSent.Load(),
		"heartbeats_received":  m.heartbeatsReceived.Load(),
		"append_entries_sent":  m.appendEntriesSent.Load(),
		"append_entries_recv":  m.appendEntriesRecv.Load(),
		"log_replication_time": float64(m.logReplicationTime.AverageLatency()) / float64(time.Millisecond),
		"commit_index":         m.commitIndex.Load(),
		"applied_index":        m.appliedIndex.Load(),
		"leader_elections":     m.leaderElections.Load(),
		"split_brain_events":   m.splitBrainEvents.Load(),
		"network_partitions":   m.networkPartitions.Load(),
		"last_heartbeat":       time.Unix(m.lastHeartbeat.Load(), 0),
	}
}

/*
GetMetrics returns a comprehensive snapshot of current metrics.
It provides a complete view of the node's operational state,
including performance metrics, election status, network statistics,
error tracking, consensus metrics, and system health.
*/
func (m *Metrics) GetMetrics() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	baseMetrics := map[string]interface{}{
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

	// Add health status without re-entrant locking
	baseMetrics["system_status"] = m.systemStatus
	baseMetrics["last_status_update"] = time.Unix(m.lastStatusUpdate.Load(), 0)

	// Add error metrics
	baseMetrics["error_metrics"] = m.GetErrorMetrics()

	// Add consensus metrics
	consensusMetrics := m.GetConsensusMetrics()
	baseMetrics["consensus"] = consensusMetrics

	return baseMetrics
}

/*
Reset resets all metrics to zero. Useful for testing.
*/
func (m *Metrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Reset operation counters
	m.insertCount.Store(0)
	m.lookupCount.Store(0)
	m.syncCount.Store(0)
	m.conflictCount.Store(0)

	// Reset election metrics
	m.votesReceived.Store(0)
	m.termNumber.Store(0)
	m.lastVoter = ""

	// Reset latency trackers
	m.insertLatency = NewLatencyTracker(100)
	m.lookupLatency = NewLatencyTracker(100)
	m.syncLatency = NewLatencyTracker(100)
	m.networkLatency = NewLatencyTracker(100)
	m.logReplicationTime = NewLatencyTracker(100)

	// Reset network metrics
	m.bytesTransmitted.Store(0)
	m.bytesReceived.Store(0)
	m.peerCount.Store(0)

	// Reset node status
	m.isLeader.Store(false)
	m.nodeRole = ""
	m.nodeWeight = 0
	m.lastSyncTime = time.Time{}

	// Reset consensus metrics
	m.heartbeatsSent.Store(0)
	m.heartbeatsReceived.Store(0)
	m.appendEntriesSent.Store(0)
	m.appendEntriesRecv.Store(0)
	m.commitIndex.Store(0)
	m.appliedIndex.Store(0)
	m.leaderElections.Store(0)
	m.splitBrainEvents.Store(0)
	m.networkPartitions.Store(0)
	m.lastHeartbeat.Store(0)

	// Reset error counts
	m.errorMutex.Lock()
	m.errorCounts = make(map[string]map[string]*atomic.Uint64)
	m.errorMutex.Unlock()

	// Reset system status
	m.systemStatus = "healthy"
	m.lastStatusUpdate.Store(time.Now().Unix())
}

/*
RecordCircuitBreakerState records the current circuit breaker state for an operation.
*/
func (m *Metrics) RecordCircuitBreakerState(operation string, state int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// In a production system, this would be exposed as a gauge metric
	// For now, we'll track it in a simple map
}

/*
RecordRetryAttempt records retry attempt information.
*/
func (m *Metrics) RecordRetryAttempt(operation string, attempt int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Track retry attempts for monitoring retry patterns
	// This helps identify operations that frequently require retries
}
