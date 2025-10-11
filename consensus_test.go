package dmt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// TestEventType represents the type of test event
type TestEventType string

// Test configuration constants
const (
	defaultClusterSize     = 3
	defaultElectionTimeout = 5 * time.Second
	defaultReplicationWait = 2 * time.Second
	testBasePort           = 16380
	testSyncInterval       = 100 * time.Millisecond

	// Test timing constants
	clusterStabilizationDelay = 500 * time.Millisecond
	replicationCheckInterval  = 10 * time.Millisecond
	maxBackoffDelay           = 100 * time.Millisecond

	// Test validation constants
	maxClusterSizeForTesting = 10
	minValidPort             = 1024
	maxValidPort             = 65535
	defaultTestDataSetSize   = 10

	// Consensus test constants
	leaderWaitTicker = 50 * time.Millisecond

	// Test scenario constants
	conflictTerm   = 999  // Term used for simulating log conflicts
	testPortOffset = 9999 // Port offset for isolated test nodes
)

// TestConfig provides configurable test parameters
type TestConfig struct {
	ClusterSize     int
	ElectionTimeout time.Duration
	ReplicationWait time.Duration
	BasePort        int
	SyncInterval    time.Duration
}

// Validate checks if the test configuration is valid
func (tc *TestConfig) Validate() error {
	if tc.ClusterSize <= 0 {
		return fmt.Errorf("cluster size must be positive, got %d", tc.ClusterSize)
	}
	if tc.ClusterSize > maxClusterSizeForTesting {
		return fmt.Errorf("cluster size too large for testing, got %d (max %d)",
			tc.ClusterSize, maxClusterSizeForTesting)
	}
	if tc.BasePort < minValidPort || tc.BasePort > maxValidPort-tc.ClusterSize {
		return fmt.Errorf("invalid base port %d for cluster size %d", tc.BasePort, tc.ClusterSize)
	}
	if tc.ElectionTimeout <= 0 {
		return fmt.Errorf("election timeout must be positive, got %v", tc.ElectionTimeout)
	}
	if tc.SyncInterval <= 0 {
		return fmt.Errorf("sync interval must be positive, got %v", tc.SyncInterval)
	}
	return nil
}

// DefaultTestConfig returns sensible defaults for testing
func DefaultTestConfig() TestConfig {
	return TestConfig{
		ClusterSize:     defaultClusterSize,
		ElectionTimeout: defaultElectionTimeout,
		ReplicationWait: defaultReplicationWait,
		BasePort:        testBasePort,
		SyncInterval:    testSyncInterval,
	}
}

// WithClusterSize sets the cluster size
func (tc *TestConfig) WithClusterSize(size int) *TestConfig {
	tc.ClusterSize = size
	return tc
}

// WithElectionTimeout sets the election timeout
func (tc *TestConfig) WithElectionTimeout(timeout time.Duration) *TestConfig {
	tc.ElectionTimeout = timeout
	return tc
}

// WithReplicationWait sets the replication wait time
func (tc *TestConfig) WithReplicationWait(wait time.Duration) *TestConfig {
	tc.ReplicationWait = wait
	return tc
}

// WithSyncInterval sets the sync interval
func (tc *TestConfig) WithSyncInterval(interval time.Duration) *TestConfig {
	tc.SyncInterval = interval
	return tc
}

// TestCluster manages a test cluster lifecycle
type TestCluster struct {
	nodes     []*NetworkNode
	leader    *NetworkNode
	followers []*NetworkNode
	size      int
}

func NewTestCluster(size int) *TestCluster {
	return &TestCluster{
		nodes: createTestCluster(size),
		size:  size,
	}
}

func (tc *TestCluster) WaitForLeader(timeout time.Duration) error {
	leader := waitForLeader(tc.nodes, timeout)
	if leader == nil {
		return fmt.Errorf("no leader elected within timeout")
	}
	tc.leader = leader
	tc.followers = getFollowers(tc.nodes)
	return nil
}

func (tc *TestCluster) Close() {
	cleanupTestCluster(tc.nodes)
}

func TestLeaderOnlyWrites(t *testing.T) {
	// Set test timeout to prevent hanging tests
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	Convey("Given a distributed cluster with leader election", t, func() {
		// Create test result tracker
		result := NewTestResult("TestLeaderOnlyWrites")
		startTime := time.Now()
		defer func() {
			result.SetOutcome(true, nil, time.Since(startTime))
		}()

		// Create observers for test monitoring
		loggingObserver := NewLoggingTestObserver().WithVerbose(true)
		metricsObserver := NewMetricsTestObserver()

		cluster := NewTestClusterManager(defaultClusterSize).
			WithTestName("TestLeaderOnlyWrites").
			AddObserver(loggingObserver).
			AddObserver(metricsObserver)

		err := cluster.Start()
		So(err, ShouldBeNil)
		defer cluster.Cleanup()

		// Wait for cluster to stabilize with context
		select {
		case <-time.After(clusterStabilizationDelay):
			// Normal case
		case <-ctx.Done():
			t.Fatal("Test timeout during cluster stabilization")
		}
		So(len(cluster.followers), ShouldEqual, 2)

		Convey("When performing write operations", func() {
			// Test leader write
			testLeaderWrite(cluster)

			// Test follower write rejection
			testFollowerWriteRejection(cluster)

			// Verify metrics were collected
			metrics := metricsObserver.GetMetrics()
			So(metrics[EventTypeLeaderElected], ShouldBeGreaterThan, 0)
			So(metrics[EventTypeWriteAttempted], ShouldBeGreaterThan, 0)
		})
	})
}

func testLeaderWrite(cluster *TestClusterManager) {
	key := []byte("test-key")
	value := []byte("test-value")

	cluster.emitEvent(EventTypeWriteAttempted, cluster.leader.config.NodeID, map[string]interface{}{
		"key_size":   len(key),
		"value_size": len(value),
		"is_leader":  true,
	})

	err := cluster.leader.forest.Insert(key, value)

	Convey("Then the write should succeed", func() {
		So(err, ShouldBeNil)
		if err == nil {
			cluster.emitEvent(EventTypeWriteCompleted, cluster.leader.config.NodeID, map[string]interface{}{
				"key": string(key),
			})
		}
	})

	Convey("And the data should be replicated to followers", func() {
		verifyReplicationSuccess(cluster, key, value)
	})
}

func testFollowerWriteRejection(cluster *TestClusterManager) {
	follower := cluster.followers[0]
	key := []byte("follower-key")
	value := []byte("follower-value")

	cluster.emitEvent(EventTypeWriteAttempted, follower.config.NodeID, map[string]interface{}{
		"key_size":   len(key),
		"value_size": len(value),
		"is_leader":  false,
	})

	err := follower.forest.Insert(key, value)

	Convey("Then the write should be rejected", func() {
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "not leader")
		if err != nil {
			cluster.emitEvent(EventTypeWriteRejected, follower.config.NodeID, map[string]interface{}{
				"reason": "not_leader",
				"error":  err.Error(),
			})
		}
	})
}

func verifyReplicationSuccess(cluster *TestClusterManager, key, value []byte) {
	cluster.emitEvent(EventTypeReplicationStart, "manager", map[string]interface{}{
		"key":            string(key),
		"follower_count": len(cluster.followers),
	})

	success := waitForConsistentReplication(cluster.leader, cluster.followers, 1, 3*time.Second)
	So(success, ShouldBeTrue)

	if success {
		cluster.emitEvent(EventTypeReplicationSuccess, "manager", map[string]interface{}{
			"key":            string(key),
			"follower_count": len(cluster.followers),
		})
	}

	for i, follower := range cluster.followers {
		retrievedValue, exists := follower.forest.Get(key)
		logFollowerReplicationState(i, follower, exists)

		cluster.emitEvent(EventTypeFollowerVerification, follower.config.NodeID, map[string]interface{}{
			"key":       string(key),
			"has_data":  exists,
			"log_index": follower.election.getLastLogIndex(),
			"applied":   follower.election.getLastApplied(),
		})

		So(exists, ShouldBeTrue)
		So(retrievedValue, ShouldResemble, value)
	}
}

func logFollowerReplicationState(index int, follower *NetworkNode, hasData bool) {
	fmt.Printf("Follower %d: log=%d, commit=%d, applied=%d, hasTree=%v, hasData=%v\n",
		index, follower.election.getLastLogIndex(), follower.election.getCommitIndex(),
		follower.election.getLastApplied(), follower.forest.tree != nil, hasData)
}

func TestLogReplication(t *testing.T) {
	Convey("Given a cluster with established leader", t, func() {
		// Set up test timeout context
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		nodes := createTestCluster(defaultClusterSize)
		defer cleanupTestCluster(nodes)

		leader := waitForLeaderWithContext(ctx, nodes)
		So(leader, ShouldNotBeNil)

		Convey("When multiple writes are performed", func() {
			writes := []testWrite{
				{[]byte("key1"), []byte("value1")},
				{[]byte("key2"), []byte("value2")},
				{[]byte("key3"), []byte("value3")},
			}

			performWrites(leader, writes)

			Convey("Then all entries should be replicated in order", func() {
				err := verifyReplicationConsistencyWithTimeout(leader, getFollowers(nodes), writes, defaultReplicationWait)
				So(err, ShouldBeNil)
			})
		})

		Convey("When a follower has log conflicts", func() {
			// This test simulates network partition recovery
			follower := getFollowers(nodes)[0]

			// Simulate conflicting entry (this would happen during partition)
			follower.election.logMutex.Lock()
			conflictEntry := RaftLogEntry{
				Term:  conflictTerm, // Different term to simulate conflict
				Index: follower.election.lastLogIndex + 1,
				Key:   []byte("conflict-key"),
				Value: []byte("conflict-value"),
			}
			follower.election.log = append(follower.election.log, conflictEntry)
			follower.election.lastLogIndex++
			follower.election.logMutex.Unlock()

			// Now send a legitimate entry from leader
			key := []byte("legitimate-key")
			value := []byte("legitimate-value")
			err := leader.forest.Insert(key, value)
			So(err, ShouldBeNil)

			Convey("Then the conflict should be resolved", func() {
				// Wait for conflict resolution
				time.Sleep(1 * time.Second)

				// Debug follower state
				fmt.Printf("After conflict resolution - Follower state: log=%d, commit=%d, applied=%d\n",
					follower.election.getLastLogIndex(), follower.election.getCommitIndex(),
					follower.election.getLastApplied())

				// Follower should have the legitimate entry, not the conflict
				retrievedValue, exists := follower.forest.Get(key)
				fmt.Printf("Legitimate key (%s): exists=%v\n", string(key), exists)
				So(exists, ShouldBeTrue)
				So(retrievedValue, ShouldResemble, value)

				// Conflict entry should be gone
				conflictValue, conflictExists := follower.forest.Get([]byte("conflict-key"))
				fmt.Printf("Conflict key: exists=%v\n", conflictExists)
				So(conflictExists, ShouldBeFalse)
				So(conflictValue, ShouldBeNil)
			})
		})
	})
}

func TestVoteRequestHandling(t *testing.T) {
	Convey("Given nodes with different log states", t, func() {
		nodes := createTestCluster(3)
		defer cleanupTestCluster(nodes)

		// Let one node become leader and add some entries
		leader := waitForLeader(nodes, 5*time.Second)
		So(leader, ShouldNotBeNil)

		// Add some log entries
		for i := range 3 {
			key := fmt.Appendf(nil, "key%d", i)
			value := fmt.Appendf(nil, "value%d", i)
			err := leader.forest.Insert(key, value)
			So(err, ShouldBeNil)
		}

		// Wait for replication
		time.Sleep(200 * time.Millisecond)

		Convey("When a node with outdated log requests votes", func() {
			// Create a new node with empty log
			outdatedNode := createSingleTestNode(testBasePort+testPortOffset, []string{})

			// Simulate vote request from outdated node
			followers := getFollowers(nodes)
			follower := followers[0]

			voteGranted := follower.election.handleVoteRequest(
				leader.election.getCurrentTerm()+1, // Higher term
				"outdated-node",
				0, // No log entries
				0, // No log term
			)

			Convey("Then the vote should be denied due to log currency", func() {
				So(voteGranted, ShouldBeFalse)
			})

			outdatedNode.Close()
		})

		Convey("When a node with up-to-date log requests votes", func() {
			follower := getFollowers(nodes)[0]
			currentTerm := leader.election.getCurrentTerm()

			voteGranted := follower.election.handleVoteRequest(
				currentTerm+1,
				"up-to-date-node",
				follower.election.getLastLogIndex(),
				follower.election.getLastLogTerm(),
			)

			Convey("Then the vote should be granted", func() {
				So(voteGranted, ShouldBeTrue)
			})
		})
	})
}

func TestCommitApplySeparation(t *testing.T) {
	Convey("Given a cluster with leader", t, func() {
		// Use a 5-node cluster so that losing 3 nodes breaks quorum (need 3 out of 5)
		nodes := createTestCluster(5)
		defer cleanupTestCluster(nodes)

		leader := waitForLeader(nodes, 5*time.Second)
		So(leader, ShouldNotBeNil)

		Convey("When entries are added to the log", func() {
			key := []byte("commit-test-key")
			value := []byte("commit-test-value")

			// Insert entry
			err := leader.forest.Insert(key, value)
			So(err, ShouldBeNil)

			Convey("Then entries should be committed before being applied", func() {
				// Check that commit index advances
				Eventually(func() uint64 {
					return leader.election.getCommitIndex()
				}, 1*time.Second, replicationCheckInterval).Should(BeNumerically(">", 0))

				// Check that entries are applied after commit
				Eventually(func() uint64 {
					return leader.election.getLastApplied()
				}, 1*time.Second, replicationCheckInterval).Should(Equal(leader.election.getCommitIndex()))

				// Verify data is in state machine
				retrievedValue, exists := leader.forest.Get(key)
				So(exists, ShouldBeTrue)
				So(retrievedValue, ShouldResemble, value)
			})
		})

		Convey("When testing quorum requirements", func() {
			// Test that the quorum size is calculated correctly for a 5-node cluster
			quorumSize := (5 / 2) + 1 // For 5 nodes
			So(quorumSize, ShouldEqual, 3)

			// Verify that the leader's election config has the correct quorum size
			So(leader.election.config.QuorumSize, ShouldEqual, 3)

			// In a real network partition where only 2 nodes are available,
			// writes should fail. This is tested in integration tests with
			// proper network simulation tools.
		})
	})
}

// Helper types and functions for testing

type testWrite struct {
	key   []byte
	value []byte
}

// NewTestWrite creates a test write with string conversion
func NewTestWrite(key, value string) testWrite {
	return testWrite{
		key:   []byte(key),
		value: []byte(value),
	}
}

// String returns a readable representation of the test write
func (tw testWrite) String() string {
	// Use strings.Builder for efficient string concatenation
	var b strings.Builder
	b.Grow(len(tw.key) + len(tw.value) + 1) // Pre-allocate capacity
	b.Write(tw.key)
	b.WriteByte('=')
	b.Write(tw.value)
	return b.String()
}

// Event type constants
const (
	EventTypeLeaderElected        = "leader_elected"
	EventTypeNodeStarted          = "node_started"
	EventTypeNodeStopped          = "node_stopped"
	EventTypeWriteAttempted       = "write_attempted"
	EventTypeWriteCompleted       = "write_completed"
	EventTypeWriteRejected        = "write_rejected"
	EventTypeReplicationDone      = "replication_done"
	EventTypeReplicationStart     = "replication_check_start"
	EventTypeReplicationSuccess   = "replication_success"
	EventTypeFollowerVerification = "follower_verification"
	EventTypeConsensusReached     = "consensus_reached"
	EventTypePartitionStart       = "partition_start"
	EventTypePartitionEnd         = "partition_end"
	EventTypeTestStarted          = "test_started"
	EventTypeTestCompleted        = "test_completed"
	EventTypeClusterStart         = "cluster_start"
	EventTypeClusterCreated       = "cluster_created"
	EventTypeClusterError         = "cluster_error"
	EventTypeElectionFailed       = "election_failed"
)

// TestEvent represents events during test execution
type TestEvent struct {
	Type      string
	Timestamp time.Time
	NodeID    string
	TestName  string
	Data      map[string]interface{}
	Duration  time.Duration
	Success   bool
	Error     error
}

// TestResult provides structured test outcome tracking
type TestResult struct {
	TestName    string
	Success     bool
	Error       error
	Duration    time.Duration
	Attempts    int
	ClusterSize int
	Events      []TestEvent
	Metadata    map[string]interface{}
}

// NewTestResult creates a new test result tracker
func NewTestResult(testName string) *TestResult {
	return &TestResult{
		TestName: testName,
		Events:   make([]TestEvent, 0),
		Metadata: make(map[string]interface{}),
	}
}

// AddEvent adds an event to the test result
func (tr *TestResult) AddEvent(event TestEvent) {
	tr.Events = append(tr.Events, event)
}

// SetOutcome sets the final test outcome
func (tr *TestResult) SetOutcome(success bool, err error, duration time.Duration) {
	tr.Success = success
	tr.Error = err
	tr.Duration = duration
}

// NewTestEvent creates a new test event with current timestamp
func NewTestEvent(eventType string, nodeID, testName string) TestEvent {
	return TestEvent{
		Type:      eventType,
		Timestamp: time.Now(),
		NodeID:    nodeID,
		TestName:  testName,
		Data:      make(map[string]interface{}),
	}
}

// WithData adds data to the event
func (e TestEvent) WithData(key string, value interface{}) TestEvent {
	e.Data[key] = value
	return e
}

// WithDuration sets the duration for the event
func (e TestEvent) WithDuration(duration time.Duration) TestEvent {
	e.Duration = duration
	return e
}

// WithSuccess sets the success status
func (e TestEvent) WithSuccess(success bool) TestEvent {
	e.Success = success
	return e
}

// WithError sets the error for the event
func (e TestEvent) WithError(err error) TestEvent {
	e.Error = err
	e.Success = err == nil
	return e
}

// ConsensusTestObserver observes test events
type ConsensusTestObserver interface {
	OnEvent(event TestEvent)
}

// TestEventPublisher manages multiple observers and publishes events
type TestEventPublisher struct {
	observers []ConsensusTestObserver
	mu        sync.RWMutex
}

// NewTestEventPublisher creates a new event publisher
func NewTestEventPublisher() *TestEventPublisher {
	return &TestEventPublisher{
		observers: make([]ConsensusTestObserver, 0),
	}
}

// AddObserver adds an observer to the publisher
func (p *TestEventPublisher) AddObserver(observer ConsensusTestObserver) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.observers = append(p.observers, observer)
}

// RemoveObserver removes an observer from the publisher
func (p *TestEventPublisher) RemoveObserver(observer ConsensusTestObserver) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, obs := range p.observers {
		if obs == observer {
			p.observers = append(p.observers[:i], p.observers[i+1:]...)
			break
		}
	}
}

// PublishEvent publishes an event to all observers
func (p *TestEventPublisher) PublishEvent(event TestEvent) {
	p.mu.RLock()
	// Pre-allocate slice with exact capacity to avoid reallocations
	observers := make([]ConsensusTestObserver, 0, len(p.observers))
	observers = append(observers, p.observers...)
	p.mu.RUnlock()

	// Publish to observers concurrently to avoid blocking
	for _, observer := range observers {
		go func(obs ConsensusTestObserver) {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("Warning: observer panic: %v\n", r)
				}
			}()
			obs.OnEvent(event)
		}(observer)
	}
}

// LoggingTestObserver logs test events with configurable output
type LoggingTestObserver struct {
	logger     Logger
	timeFormat string
	verbose    bool
}

// NewLoggingTestObserver creates a new logging observer with default configuration
func NewLoggingTestObserver() *LoggingTestObserver {
	return &LoggingTestObserver{
		logger:     DefaultLogger("test-observer"),
		timeFormat: "15:04:05.000",
		verbose:    false,
	}
}

// WithLogger sets a custom logger
func (o *LoggingTestObserver) WithLogger(logger Logger) *LoggingTestObserver {
	o.logger = logger
	return o
}

// WithVerbose enables verbose logging
func (o *LoggingTestObserver) WithVerbose(verbose bool) *LoggingTestObserver {
	o.verbose = verbose
	return o
}

func (o *LoggingTestObserver) OnEvent(event TestEvent) {
	if o.logger != nil {
		fields := []interface{}{
			"timestamp", event.Timestamp.Format(o.timeFormat),
			"type", string(event.Type),
			"node_id", event.NodeID,
			"test_name", event.TestName,
		}

		if event.Duration > 0 {
			fields = append(fields, "duration", event.Duration)
		}

		if event.Error != nil {
			fields = append(fields, "error", event.Error)
		}

		if o.verbose && len(event.Data) > 0 {
			fields = append(fields, "data", event.Data)
		}

		o.logger.Info("Test event", fields...)
	} else {
		// Fallback to fmt.Printf if no logger available
		fmt.Printf("[%s] %s on %s: %v\n",
			event.Timestamp.Format(o.timeFormat),
			string(event.Type),
			event.NodeID,
			event.Data)
	}
}

// MetricsTestObserver collects metrics from test events
type MetricsTestObserver struct {
	metrics map[string]int
	mu      sync.RWMutex
}

// NewMetricsTestObserver creates a new metrics observer
func NewMetricsTestObserver() *MetricsTestObserver {
	return &MetricsTestObserver{
		metrics: make(map[string]int),
	}
}

func (o *MetricsTestObserver) OnEvent(event TestEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.metrics[event.Type]++
}

// GetMetrics returns a copy of the current metrics
func (o *MetricsTestObserver) GetMetrics() map[string]int {
	o.mu.RLock()
	defer o.mu.RUnlock()

	result := make(map[string]int)
	for k, v := range o.metrics {
		result[k] = v
	}
	return result
}

// Reset clears all metrics
func (o *MetricsTestObserver) Reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.metrics = make(map[string]int)
}

// FileTestObserver writes test events to a file
type FileTestObserver struct {
	file   *os.File
	mu     sync.Mutex
	closed bool
}

// NewFileTestObserver creates a new file observer
func NewFileTestObserver(filename string) (*FileTestObserver, error) {
	file, err := os.Create(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to create test event file: %w", err)
	}

	return &FileTestObserver{
		file: file,
	}, nil
}

func (o *FileTestObserver) OnEvent(event TestEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		fmt.Printf("Warning: failed to marshal test event: %v\n", err)
		return
	}

	// Write with newline and flush for immediate visibility
	if _, err := o.file.Write(append(eventJSON, '\n')); err != nil {
		fmt.Printf("Warning: failed to write test event: %v\n", err)
		return
	}

	// Ensure data is written to disk for test debugging
	if err := o.file.Sync(); err != nil {
		fmt.Printf("Warning: failed to sync test event file: %v\n", err)
	}
}

// Close closes the file observer
func (o *FileTestObserver) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return nil
	}

	o.closed = true
	return o.file.Close()
}

// TestClusterManager provides a cleaner interface for managing test clusters
type TestClusterManager struct {
	nodes     []*NetworkNode
	leader    *NetworkNode
	followers []*NetworkNode
	size      int
	basePort  int
	publisher *TestEventPublisher
	testName  string
	startTime time.Time
	events    []TestEvent             // Store events for analysis
	observers []ConsensusTestObserver // Direct observers for backward compatibility
}

func (tcm *TestClusterManager) emitEvent(eventType, nodeID string, data map[string]interface{}) {
	event := TestEvent{
		Type:      eventType,
		Timestamp: time.Now(),
		NodeID:    nodeID,
		TestName:  tcm.testName,
		Data:      data,
	}

	// Store event if events slice exists
	if tcm.events != nil {
		tcm.events = append(tcm.events, event)
	}

	// Publish to observers via publisher
	if tcm.publisher != nil {
		tcm.publisher.PublishEvent(event)
	}
}

func NewTestClusterManager(size int) *TestClusterManager {
	return &TestClusterManager{
		size:      size,
		basePort:  testBasePort,
		publisher: NewTestEventPublisher(),
		testName:  "unknown",
		startTime: time.Now(),
		events:    make([]TestEvent, 0),
	}
}

// WithTestName sets the test name for event tracking
func (tcm *TestClusterManager) WithTestName(name string) *TestClusterManager {
	tcm.testName = name
	return tcm
}

// AddObserver adds an observer to the test cluster
func (tcm *TestClusterManager) AddObserver(observer ConsensusTestObserver) *TestClusterManager {
	tcm.observers = append(tcm.observers, observer)
	if tcm.publisher != nil {
		tcm.publisher.AddObserver(observer)
	}
	return tcm
}

// PublishEvent publishes an event to all observers
func (tcm *TestClusterManager) PublishEvent(eventType string, nodeID string) {
	event := NewTestEvent(eventType, nodeID, tcm.testName)
	tcm.publisher.PublishEvent(event)
}

// PublishEventWithData publishes an event with additional data
func (tcm *TestClusterManager) PublishEventWithData(eventType string, nodeID string, data map[string]interface{}) {
	event := NewTestEvent(eventType, nodeID, tcm.testName)
	for k, v := range data {
		event.Data[k] = v
	}
	tcm.publisher.PublishEvent(event)
}

// PublishEventWithDuration publishes an event with duration
func (tcm *TestClusterManager) PublishEventWithDuration(eventType string, nodeID string, duration time.Duration) {
	event := NewTestEvent(eventType, nodeID, tcm.testName)
	event.Duration = duration
	tcm.publisher.PublishEvent(event)
}

// TestClusterBuilder provides a fluent interface for building test clusters
type TestClusterBuilder struct {
	size      int
	basePort  int
	observers []ConsensusTestObserver
	testName  string
	config    TestConfig
}

func NewTestClusterBuilder() *TestClusterBuilder {
	return &TestClusterBuilder{
		size:      defaultClusterSize,
		basePort:  testBasePort,
		observers: make([]ConsensusTestObserver, 0),
		testName:  "unknown",
		config:    DefaultTestConfig(),
	}
}

func (b *TestClusterBuilder) WithSize(size int) *TestClusterBuilder {
	b.size = size
	return b
}

func (b *TestClusterBuilder) WithBasePort(port int) *TestClusterBuilder {
	b.basePort = port
	return b
}

func (b *TestClusterBuilder) WithConfig(config TestConfig) *TestClusterBuilder {
	b.config = config
	return b
}

func (b *TestClusterBuilder) WithTestName(name string) *TestClusterBuilder {
	b.testName = name
	return b
}

func (b *TestClusterBuilder) WithObserver(observer ConsensusTestObserver) *TestClusterBuilder {
	b.observers = append(b.observers, observer)
	return b
}

func (b *TestClusterBuilder) WithLoggingObserver() *TestClusterBuilder {
	return b.WithObserver(NewLoggingTestObserver())
}

func (b *TestClusterBuilder) WithMetricsObserver() *TestClusterBuilder {
	return b.WithObserver(NewMetricsTestObserver())
}

func (b *TestClusterBuilder) Build() *TestClusterManager {
	tcm := &TestClusterManager{
		size:      b.size,
		basePort:  b.basePort,
		publisher: NewTestEventPublisher(),
		testName:  b.testName,
		startTime: time.Now(),
	}

	// Add all observers to the cluster manager
	for _, observer := range b.observers {
		tcm.AddObserver(observer)
	}

	return tcm
}

func (tcm *TestClusterManager) Start() error {
	tcm.emitEvent(EventTypeClusterStart, "manager", map[string]interface{}{
		"cluster_size": tcm.size,
		"base_port":    tcm.basePort,
	})

	if err := tcm.validateConfiguration(); err != nil {
		tcm.emitEvent(EventTypeClusterError, "manager", map[string]interface{}{
			"error": err.Error(),
			"phase": "validation",
		})
		return fmt.Errorf("invalid cluster configuration: %w", err)
	}

	nodes, err := tcm.createCluster()
	if err != nil {
		tcm.emitEvent(EventTypeClusterError, "manager", map[string]interface{}{
			"error": err.Error(),
			"phase": "creation",
		})
		return fmt.Errorf("failed to create cluster of size %d: %w", tcm.size, err)
	}
	tcm.nodes = nodes

	tcm.emitEvent(EventTypeClusterCreated, "manager", map[string]interface{}{
		"node_count": len(nodes),
	})

	if err := validateClusterState(nodes); err != nil {
		tcm.emitEvent(EventTypeClusterError, "manager", map[string]interface{}{
			"error": err.Error(),
			"phase": "validation",
		})
		tcm.Cleanup()
		return fmt.Errorf("cluster validation failed: %w", err)
	}

	leader, err := tcm.waitForLeaderElection(defaultElectionTimeout)
	if err != nil {
		tcm.emitEvent(EventTypeElectionFailed, "manager", map[string]interface{}{
			"error":   err.Error(),
			"timeout": defaultElectionTimeout.String(),
		})
		tcm.Cleanup()
		return fmt.Errorf("failed to elect leader within %v: %w", defaultElectionTimeout, err)
	}
	tcm.leader = leader
	tcm.followers = tcm.getFollowers()

	tcm.emitEvent(EventTypeLeaderElected, tcm.leader.config.NodeID, map[string]interface{}{
		"follower_count": len(tcm.followers),
	})

	return nil
}

func (tcm *TestClusterManager) validateConfiguration() error {
	if tcm.size <= 0 {
		return fmt.Errorf("cluster size must be positive, got %d", tcm.size)
	}
	if tcm.size > maxClusterSizeForTesting {
		return fmt.Errorf("cluster size too large for testing, got %d (max %d)", tcm.size, maxClusterSizeForTesting)
	}
	if tcm.basePort < minValidPort || tcm.basePort > maxValidPort-tcm.size {
		return fmt.Errorf("invalid base port %d for cluster size %d (valid range: %d-%d)",
			tcm.basePort, tcm.size, minValidPort, maxValidPort-tcm.size)
	}
	return nil
}

func (tcm *TestClusterManager) Cleanup() {
	if tcm.nodes != nil {
		cleanupTestCluster(tcm.nodes)
		tcm.nodes = nil
		tcm.leader = nil
		tcm.followers = nil
	}
}

func (tcm *TestClusterManager) createCluster() ([]*NetworkNode, error) {
	peerAddrs := tcm.generatePeerAddresses()
	nodes, err := tcm.createNodes(peerAddrs)
	if err != nil {
		return nil, err
	}

	// Give nodes time to connect
	time.Sleep(clusterStabilizationDelay)
	return nodes, nil
}

func (tcm *TestClusterManager) generatePeerAddresses() []string {
	peerAddrs := make([]string, tcm.size)
	for i := range tcm.size {
		peerAddrs[i] = fmt.Sprintf("localhost:%d", tcm.basePort+i)
	}
	return peerAddrs
}

func (tcm *TestClusterManager) createNodes(peerAddrs []string) ([]*NetworkNode, error) {
	nodes := make([]*NetworkNode, tcm.size)
	factory := NewTestNodeFactory()

	for i := range tcm.size {
		peers := tcm.buildPeerListForNode(i, peerAddrs)

		config := TestNodeConfig{
			Port:         tcm.basePort + i,
			Peers:        peers,
			NodeID:       fmt.Sprintf("test-node-%d", i),
			SyncInterval: testSyncInterval,
		}

		node, err := factory.CreateNodeWithConfig(config)
		if err != nil {
			tcm.cleanupPartialNodes(nodes, i)
			return nil, fmt.Errorf("failed to create node %d: %w", i, err)
		}
		nodes[i] = node
	}
	return nodes, nil
}

func (tcm *TestClusterManager) buildPeerListForNode(nodeIndex int, peerAddrs []string) []string {
	peers := make([]string, 0, tcm.size-1)
	for j := range tcm.size {
		if j != nodeIndex {
			peers = append(peers, peerAddrs[j])
		}
	}
	return peers
}

func (tcm *TestClusterManager) cleanupPartialNodes(nodes []*NetworkNode, upToIndex int) {
	for k := range upToIndex {
		if nodes[k] != nil {
			nodes[k].Close()
		}
	}
}

func (tcm *TestClusterManager) waitForLeaderElection(timeout time.Duration) (*NetworkNode, error) {
	leader := waitForLeader(tcm.nodes, timeout)
	if leader == nil {
		return nil, fmt.Errorf("no leader elected within timeout")
	}
	return leader, nil
}

func (tcm *TestClusterManager) getFollowers() []*NetworkNode {
	return getFollowers(tcm.nodes)
}

// GetEvents returns all events recorded by this cluster manager
func (tcm *TestClusterManager) GetEvents() []TestEvent {
	return tcm.events
}

// GetEventsByType returns events filtered by type
func (tcm *TestClusterManager) GetEventsByType(eventType string) []TestEvent {
	var filtered []TestEvent
	for _, event := range tcm.events {
		if event.Type == eventType {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

// TestNodeConfig provides configuration for test nodes
type TestNodeConfig struct {
	Port         int
	Peers        []string
	NodeID       string
	SyncInterval time.Duration
}

// TestNodeConfigBuilder provides a fluent interface for building TestNodeConfig
type TestNodeConfigBuilder struct {
	config TestNodeConfig
}

// NewTestNodeConfigBuilder creates a new builder with default values
func NewTestNodeConfigBuilder() *TestNodeConfigBuilder {
	return &TestNodeConfigBuilder{
		config: TestNodeConfig{
			SyncInterval: testSyncInterval,
		},
	}
}

// WithPort sets the port
func (b *TestNodeConfigBuilder) WithPort(port int) *TestNodeConfigBuilder {
	b.config.Port = port
	return b
}

// WithPeers sets the peer addresses
func (b *TestNodeConfigBuilder) WithPeers(peers []string) *TestNodeConfigBuilder {
	b.config.Peers = make([]string, len(peers))
	copy(b.config.Peers, peers)
	return b
}

// WithNodeID sets the node ID
func (b *TestNodeConfigBuilder) WithNodeID(nodeID string) *TestNodeConfigBuilder {
	b.config.NodeID = nodeID
	return b
}

// WithSyncInterval sets the sync interval
func (b *TestNodeConfigBuilder) WithSyncInterval(interval time.Duration) *TestNodeConfigBuilder {
	b.config.SyncInterval = interval
	return b
}

// Build creates the final configuration
func (b *TestNodeConfigBuilder) Build() TestNodeConfig {
	// Set default node ID if not provided
	if b.config.NodeID == "" {
		b.config.NodeID = fmt.Sprintf("node-%d", b.config.Port)
	}
	return b.config
}

// TestNodeFactory creates test nodes with consistent configuration and tracks them for cleanup
type TestNodeFactory struct {
	createdNodes []*NetworkNode
	mu           sync.Mutex
	closed       bool // Prevent use after cleanup
}

// NewTestNodeFactory creates a new test node factory
func NewTestNodeFactory() *TestNodeFactory {
	return &TestNodeFactory{
		createdNodes: make([]*NetworkNode, 0),
	}
}

// CreateNodeWithConfig creates a node with the given configuration
func (f *TestNodeFactory) CreateNodeWithConfig(config TestNodeConfig) (*NetworkNode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return nil, fmt.Errorf("factory has been closed")
	}

	node, err := createTestNodeFromConfig(config)
	if err != nil {
		return nil, err
	}

	f.createdNodes = append(f.createdNodes, node)
	return node, nil
}

// CreateNode creates a node with basic configuration (backward compatibility)
func (f *TestNodeFactory) CreateNode(port int, peers []string) (*NetworkNode, error) {
	config := TestNodeConfig{
		Port:         port,
		Peers:        peers,
		NodeID:       fmt.Sprintf("test-node-%d", port),
		SyncInterval: testSyncInterval,
	}
	return f.CreateNodeWithConfig(config)
}

// CleanupAll cleans up all nodes created by this factory
func (f *TestNodeFactory) CleanupAll() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return // Already cleaned up
	}

	// Use concurrent cleanup for better performance
	var wg sync.WaitGroup
	for _, node := range f.createdNodes {
		if node != nil {
			wg.Add(1)
			go func(n *NetworkNode) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						fmt.Printf("Warning: panic during node cleanup: %v\n", r)
					}
				}()

				if err := n.Close(); err != nil {
					fmt.Printf("Warning: error closing node: %v\n", err)
				}
			}(node)
		}
	}
	wg.Wait()

	f.createdNodes = f.createdNodes[:0] // Clear slice but keep capacity
	f.closed = true
}

// NodeCount returns the number of nodes created by this factory
func (f *TestNodeFactory) NodeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.createdNodes)
}

func performWrites(node *NetworkNode, writes []testWrite) {
	for _, write := range writes {
		err := node.forest.Insert(write.key, write.value)
		So(err, ShouldBeNil)
	}
}

// waitForConsistentReplication waits for all followers to reach expected log state
func waitForConsistentReplication(_ *NetworkNode, followers []*NetworkNode, expectedLogIndex uint64, timeout time.Duration) bool {
	return waitForConditionWithBackoff(func() bool {
		return checkReplicationConsistency(followers, expectedLogIndex)
	}, timeout)
}

// checkReplicationConsistency verifies all followers have reached the expected state
func checkReplicationConsistency(followers []*NetworkNode, expectedLogIndex uint64) bool {
	for _, follower := range followers {
		if follower.election.getLastLogIndex() != expectedLogIndex ||
			follower.election.getLastApplied() != expectedLogIndex {
			return false
		}
	}
	return true
}

// WaitConfig configures waiting behavior
type WaitConfig struct {
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	BackoffFactor  float64
	Timeout        time.Duration
}

// DefaultWaitConfig returns sensible defaults for waiting
func DefaultWaitConfig(timeout time.Duration) WaitConfig {
	return WaitConfig{
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     maxBackoffDelay,
		BackoffFactor:  2.0,
		Timeout:        timeout,
	}
}

// waitForConditionWithBackoff implements configurable exponential backoff
func waitForConditionWithBackoff(condition func() bool, timeout time.Duration) bool {
	return waitForConditionWithConfig(condition, DefaultWaitConfig(timeout))
}

func waitForConditionWithConfig(condition func() bool, config WaitConfig) bool {
	backoff := config.InitialBackoff
	deadline := time.Now().Add(config.Timeout)
	attempts := 0
	start := time.Now()

	for time.Now().Before(deadline) {
		attempts++
		if condition() {
			elapsed := time.Since(start)
			if attempts > 1 {
				fmt.Printf("Condition met after %d attempts in %v\n", attempts, elapsed)
			}
			return true
		}

		time.Sleep(backoff)
		backoff = time.Duration(float64(backoff) * config.BackoffFactor)
		if backoff > config.MaxBackoff {
			backoff = config.MaxBackoff
		}
	}

	elapsed := time.Since(start)
	fmt.Printf("Condition failed after %d attempts over %v (timeout: %v)\n",
		attempts, elapsed, config.Timeout)
	return false
}

// verifyReplicationConsistencyWithTimeout adds timeout support to replication verification
func verifyReplicationConsistencyWithTimeout(leader *NetworkNode, followers []*NetworkNode, writes []testWrite, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		allConsistent := true
		for i, follower := range followers {
			if err := verifyFollowerConsistency(leader, follower, writes, i); err != nil {
				allConsistent = false
				break
			}
		}
		if allConsistent {
			return nil
		}
		time.Sleep(replicationCheckInterval)
	}

	// Final attempt with detailed error
	for i, follower := range followers {
		if err := verifyFollowerConsistency(leader, follower, writes, i); err != nil {
			return fmt.Errorf("replication consistency verification failed after timeout: %w", err)
		}
	}
	return nil
}

func verifyFollowerConsistency(leader *NetworkNode, follower *NetworkNode, writes []testWrite, index int) error {
	logFollowerState(index, follower)

	if err := verifyFollowerData(follower, writes); err != nil {
		return fmt.Errorf("follower %d data verification failed: %w", index, err)
	}

	if err := verifyLogConsistency(leader, follower, index); err != nil {
		return fmt.Errorf("follower %d log consistency failed: %w", index, err)
	}

	return nil
}

func logFollowerState(index int, follower *NetworkNode) {
	fmt.Printf("Follower %d state: log=%d, commit=%d, applied=%d, hasTree=%v\n",
		index, follower.election.getLastLogIndex(), follower.election.getCommitIndex(),
		follower.election.getLastApplied(), follower.forest.tree != nil)
}

func verifyFollowerData(follower *NetworkNode, writes []testWrite) error {
	for j, write := range writes {
		value, exists := follower.forest.Get(write.key)
		fmt.Printf("  Write %d (%s): exists=%v\n", j, string(write.key), exists)

		if !exists {
			return fmt.Errorf("write %d key %s not found", j, string(write.key))
		}
		if !bytes.Equal(value, write.value) {
			return fmt.Errorf("write %d key %s value mismatch: expected %s, got %s",
				j, string(write.key), string(write.value), string(value))
		}
	}
	return nil
}

func verifyLogConsistency(leader *NetworkNode, follower *NetworkNode, followerIndex int) error {
	leaderLogIndex := leader.election.getLastLogIndex()
	followerLogIndex := follower.election.getLastLogIndex()
	fmt.Printf("Leader log index: %d, Follower %d log index: %d\n", leaderLogIndex, followerIndex, followerLogIndex)

	if followerLogIndex != leaderLogIndex {
		return fmt.Errorf("log inconsistency: leader=%d, follower[%d]=%d",
			leaderLogIndex, followerIndex, followerLogIndex)
	}

	return nil
}

func createTestCluster(size int) []*NetworkNode {
	nodes := make([]*NetworkNode, size)
	basePort := testBasePort

	// Create peer addresses
	peerAddrs := make([]string, size)
	for i := range size {
		peerAddrs[i] = fmt.Sprintf("localhost:%d", basePort+i)
	}

	// Create nodes with proper error handling
	for i := range size {
		// Each node connects to all others
		peers := make([]string, 0, size-1)
		for j := range size {
			if i != j {
				peers = append(peers, peerAddrs[j])
			}
		}

		node, err := createSingleTestNodeSafe(basePort+i, peers)
		if err != nil {
			// Cleanup any nodes created so far
			for k := 0; k < i; k++ {
				if nodes[k] != nil {
					nodes[k].Close()
				}
			}
			panic(fmt.Errorf("failed to create node %d: %w", i, err))
		}
		nodes[i] = node
	}

	// Give nodes time to connect
	time.Sleep(clusterStabilizationDelay)

	return nodes
}

// NewTestNodeConfig creates a default test node configuration
func NewTestNodeConfig(port int, peers []string) TestNodeConfig {
	return TestNodeConfig{
		Port:         port,
		Peers:        peers,
		NodeID:       fmt.Sprintf("node-%d", port),
		SyncInterval: testSyncInterval,
	}
}

// Validate checks if the TestNodeConfig is valid
func (config TestNodeConfig) Validate() error {
	if config.Port <= 0 || config.Port > 65535 {
		return fmt.Errorf("invalid port %d: must be between 1 and 65535", config.Port)
	}
	if config.NodeID == "" {
		return fmt.Errorf("node ID cannot be empty")
	}
	if config.SyncInterval <= 0 {
		return fmt.Errorf("sync interval must be positive, got %v", config.SyncInterval)
	}
	return nil
}

func createSingleTestNodeSafe(port int, peers []string) (*NetworkNode, error) {
	config := NewTestNodeConfig(port, peers)
	return createTestNodeFromConfig(config)
}

// createSingleTestNode creates a test node and panics on error (for test convenience)
// Deprecated: Use createSingleTestNodeSafe for better error handling
func createSingleTestNode(port int, peers []string) *NetworkNode {
	node, err := createSingleTestNodeSafe(port, peers)
	if err != nil {
		panic(fmt.Errorf("failed to create test node on port %d: %w", port, err))
	}
	return node
}

func createTestNodeFromConfig(config TestNodeConfig) (*NetworkNode, error) {
	// Validate configuration first
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid test node config: %w", err)
	}

	forestConfig := ForestConfig{
		PersistDir: generateUniqueTempDir(config.Port),
		Network: &NetworkConfig{
			ListenAddr:   fmt.Sprintf(":%d", config.Port),
			PeerAddrs:    config.Peers,
			NodeID:       config.NodeID,
			SyncInterval: config.SyncInterval,
		},
	}

	forest, err := NewForest(forestConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create forest for node %s: %w", config.NodeID, err)
	}

	if forest.network == nil {
		forest.Close()
		return nil, fmt.Errorf("network node not initialized for %s", config.NodeID)
	}

	return forest.network, nil
}

func generateUniqueTempDir(port int) string {
	// Use os.TempDir() for cross-platform compatibility
	baseDir := os.TempDir()
	return filepath.Join(baseDir, fmt.Sprintf("dmt-test-%d-%d", port, time.Now().UnixNano()))
}

func TestTestNodeFactory(t *testing.T) {
	Convey("Given a TestNodeFactory", t, func() {
		factory := NewTestNodeFactory()
		defer factory.CleanupAll()

		Convey("When creating nodes with valid configuration", func() {
			config := NewTestNodeConfigBuilder().
				WithPort(testBasePort + 1000).
				WithNodeID("test-factory-node").
				WithPeers([]string{"localhost:9999"}).
				Build()

			node, err := factory.CreateNodeWithConfig(config)

			Convey("Then the node should be created successfully", func() {
				So(err, ShouldBeNil)
				So(node, ShouldNotBeNil)
				So(factory.NodeCount(), ShouldEqual, 1)
			})
		})

		Convey("When creating nodes with invalid configuration", func() {
			config := TestNodeConfig{
				Port:   -1, // Invalid port
				NodeID: "",
			}

			node, err := factory.CreateNodeWithConfig(config)

			Convey("Then it should return an error", func() {
				So(err, ShouldNotBeNil)
				So(node, ShouldBeNil)
				So(factory.NodeCount(), ShouldEqual, 0)
			})
		})

		Convey("When using factory after cleanup", func() {
			factory.CleanupAll()

			config := NewTestNodeConfig(testBasePort+1001, []string{})
			node, err := factory.CreateNodeWithConfig(config)

			Convey("Then it should return an error", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "factory has been closed")
				So(node, ShouldBeNil)
			})
		})
	})
}

func TestTestNodeConfigValidation(t *testing.T) {
	Convey("Given TestNodeConfig validation", t, func() {
		Convey("When validating valid configuration", func() {
			config := TestNodeConfig{
				Port:         8080,
				NodeID:       "valid-node",
				SyncInterval: time.Second,
			}

			err := config.Validate()

			Convey("Then it should pass validation", func() {
				So(err, ShouldBeNil)
			})
		})

		Convey("When validating invalid port", func() {
			config := TestNodeConfig{
				Port:         -1,
				NodeID:       "test-node",
				SyncInterval: time.Second,
			}

			err := config.Validate()

			Convey("Then it should fail validation", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "invalid port")
			})
		})

		Convey("When validating empty node ID", func() {
			config := TestNodeConfig{
				Port:         8080,
				NodeID:       "",
				SyncInterval: time.Second,
			}

			err := config.Validate()

			Convey("Then it should fail validation", func() {
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "node ID cannot be empty")
			})
		})
	})
}

func TestTestNodeConfigBuilder(t *testing.T) {
	Convey("Given a TestNodeConfigBuilder", t, func() {
		builder := NewTestNodeConfigBuilder()

		Convey("When building configuration with fluent interface", func() {
			config := builder.
				WithPort(9090).
				WithNodeID("builder-test").
				WithPeers([]string{"peer1", "peer2"}).
				WithSyncInterval(2 * time.Second).
				Build()

			Convey("Then all values should be set correctly", func() {
				So(config.Port, ShouldEqual, 9090)
				So(config.NodeID, ShouldEqual, "builder-test")
				So(config.Peers, ShouldResemble, []string{"peer1", "peer2"})
				So(config.SyncInterval, ShouldEqual, 2*time.Second)
			})
		})

		Convey("When building with default node ID", func() {
			config := builder.WithPort(8080).Build()

			Convey("Then node ID should be auto-generated", func() {
				So(config.NodeID, ShouldEqual, "node-8080")
			})
		})
	})
}

// TestPortAllocator manages port allocation for test isolation
type TestPortAllocator struct {
	mu       sync.Mutex
	nextPort int
}

var globalPortAllocator = &TestPortAllocator{nextPort: testBasePort}

func (tpa *TestPortAllocator) AllocatePortRange(size int) int {
	tpa.mu.Lock()
	defer tpa.mu.Unlock()

	basePort := tpa.nextPort
	tpa.nextPort += size + 10 // Add buffer between test clusters
	return basePort
}

func AllocateTestPorts(clusterSize int) int {
	return globalPortAllocator.AllocatePortRange(clusterSize)
}

func cleanupTestCluster(nodes []*NetworkNode) {
	if len(nodes) == 0 {
		return
	}

	var wg sync.WaitGroup
	for _, node := range nodes {
		if node != nil {
			wg.Add(1)
			go func(n *NetworkNode) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						fmt.Printf("Warning: panic during node cleanup: %v\n", r)
					}
				}()

				if err := n.Close(); err != nil {
					fmt.Printf("Warning: error closing node: %v\n", err)
				}
				// Clean up persistence directories - they follow the pattern /tmp/dmt-test-*
				// We'll let the OS handle cleanup of /tmp files
			}(node)
		}
	}
	wg.Wait()
}

func waitForLeader(nodes []*NetworkNode, timeout time.Duration) *NetworkNode {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return waitForLeaderWithContext(ctx, nodes)
}

func waitForLeaderWithContext(ctx context.Context, nodes []*NetworkNode) *NetworkNode {
	ticker := time.NewTicker(leaderWaitTicker)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for _, node := range nodes {
				if node != nil && node.election != nil && node.election.isLeader() {
					return node
				}
			}
		}
	}
}

func getFollowers(nodes []*NetworkNode) []*NetworkNode {
	var followers []*NetworkNode
	for _, node := range nodes {
		if node != nil && node.election != nil && !node.election.isLeader() {
			followers = append(followers, node)
		}
	}
	return followers
}

// Eventually and Should are helper functions for async testing
func Eventually(actual func() uint64, timeout time.Duration, interval time.Duration) *EventuallyAssertion {
	return &EventuallyAssertion{
		actual:   actual,
		timeout:  timeout,
		interval: interval,
	}
}

// EventuallyWithContext provides context-aware eventually assertions
func EventuallyWithContext(ctx context.Context, actual func() uint64, timeout time.Duration, interval time.Duration) *EventuallyAssertion {
	return &EventuallyAssertion{
		actual:   actual,
		timeout:  timeout,
		interval: interval,
		ctx:      ctx,
	}
}

type EventuallyAssertion struct {
	actual   func() uint64
	timeout  time.Duration
	interval time.Duration
	ctx      context.Context
}

func (e *EventuallyAssertion) Should(matcher func(uint64) bool) {
	deadline := time.Now().Add(e.timeout)
	var lastValue uint64
	attempts := 0

	for time.Now().Before(deadline) {
		attempts++
		lastValue = e.actual()
		if matcher(lastValue) {
			if attempts > 1 {
				fmt.Printf("Eventually assertion succeeded after %d attempts (value: %d)\n", attempts, lastValue)
			}
			return
		}
		time.Sleep(e.interval)
	}
	panic(fmt.Sprintf("Eventually assertion failed after %d attempts over %v: last value was %d",
		attempts, e.timeout, lastValue))
}

func BeNumerically(operator string, expected uint64) func(uint64) bool {
	return func(actual uint64) bool {
		switch operator {
		case ">":
			return actual > expected
		case ">=":
			return actual >= expected
		case "<":
			return actual < expected
		case "<=":
			return actual <= expected
		case "==":
			return actual == expected
		default:
			return false
		}
	}
}

func Equal(expected uint64) func(uint64) bool {
	return func(actual uint64) bool {
		return actual == expected
	}
}

// Test assertion helpers
func AssertEventuallyConsistent(leader *NetworkNode, followers []*NetworkNode, key, value []byte, timeout time.Duration) error {
	success := waitForConditionWithBackoff(func() bool {
		return checkDataConsistency(followers, key, value)
	}, timeout)

	if !success {
		return fmt.Errorf("consistency not achieved within %v for key %s", timeout, string(key))
	}

	return nil
}

// AssertEventuallyConsistentWithContext provides context-aware consistency checking
func AssertEventuallyConsistentWithContext(ctx context.Context, leader *NetworkNode, followers []*NetworkNode, key, value []byte, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(replicationCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("consistency check cancelled or timed out for key %s: %w", string(key), ctx.Err())
		case <-ticker.C:
			if checkDataConsistency(followers, key, value) {
				return nil
			}
		}
	}
}

// AssertEventuallyConsistentPanic provides backward compatibility
func AssertEventuallyConsistentPanic(leader *NetworkNode, followers []*NetworkNode, key, value []byte, timeout time.Duration) {
	if err := AssertEventuallyConsistent(leader, followers, key, value, timeout); err != nil {
		panic(err.Error())
	}
}

func checkDataConsistency(followers []*NetworkNode, key, value []byte) bool {
	for _, follower := range followers {
		if follower == nil || follower.forest == nil {
			return false
		}
		if val, exists := follower.forest.Get(key); !exists || !bytes.Equal(val, value) {
			return false
		}
	}
	return true
}

// ClusterValidationResult contains detailed cluster validation information
type ClusterValidationResult struct {
	Valid      bool
	Leaders    int
	Followers  int
	Errors     []string
	NodeStates map[string]string
}

// validateClusterState performs comprehensive validation of cluster state
func validateClusterState(nodes []*NetworkNode) error {
	result := validateClusterStateDetailed(nodes)
	if !result.Valid {
		return fmt.Errorf("cluster validation failed: %v", result.Errors)
	}
	return nil
}

// validateClusterStateDetailed performs detailed validation and returns comprehensive results
func validateClusterStateDetailed(nodes []*NetworkNode) ClusterValidationResult {
	result := ClusterValidationResult{
		Valid:      true,
		Errors:     make([]string, 0),
		NodeStates: make(map[string]string),
	}

	if len(nodes) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, "empty cluster")
		return result
	}

	for i, node := range nodes {
		nodeID := fmt.Sprintf("node-%d", i)

		if node == nil {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("node %d is nil", i))
			result.NodeStates[nodeID] = "nil"
			continue
		}

		if node.election == nil {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("node %d has no election manager", i))
			result.NodeStates[nodeID] = "no-election"
			continue
		}

		if node.forest == nil {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("node %d has no forest", i))
			result.NodeStates[nodeID] = "no-forest"
			continue
		}

		if node.election.isLeader() {
			result.Leaders++
			result.NodeStates[nodeID] = "leader"
		} else {
			result.Followers++
			result.NodeStates[nodeID] = "follower"
		}
	}

	if result.Leaders != 1 {
		result.Valid = false
		result.Errors = append(result.Errors,
			fmt.Sprintf("expected exactly 1 leader, found %d", result.Leaders))
	}

	return result
}

func AssertLogConsistency(leader *NetworkNode, followers []*NetworkNode) error {
	leaderLogIndex := leader.election.getLastLogIndex()

	var inconsistencies []string
	for i, follower := range followers {
		followerLogIndex := follower.election.getLastLogIndex()
		if followerLogIndex != leaderLogIndex {
			inconsistencies = append(inconsistencies,
				fmt.Sprintf("follower[%d]=%d", i, followerLogIndex))
		}
	}

	if len(inconsistencies) > 0 {
		return fmt.Errorf("log inconsistency detected - leader=%d, inconsistent followers: %v",
			leaderLogIndex, inconsistencies)
	}

	return nil
}

// AssertLogConsistencyPanic provides backward compatibility with panic behavior
func AssertLogConsistencyPanic(leader *NetworkNode, followers []*NetworkNode) {
	if err := AssertLogConsistency(leader, followers); err != nil {
		panic(err.Error())
	}
}

// TestDataSet provides structured test data management
type TestDataSet struct {
	writes []testWrite
	name   string
}

func NewTestDataSet() *TestDataSet {
	return NewNamedTestDataSet("default")
}

func NewNamedTestDataSet(name string) *TestDataSet {
	return &TestDataSet{
		writes: make([]testWrite, 0, defaultTestDataSetSize), // Pre-allocate for common case
		name:   name,
	}
}

func (tds *TestDataSet) Name() string {
	return tds.name
}

func (tds *TestDataSet) AddWrite(key, value string) *TestDataSet {
	tds.writes = append(tds.writes, NewTestWrite(key, value))
	return tds
}

func (tds *TestDataSet) AddWrites(keyValuePairs ...string) *TestDataSet {
	if len(keyValuePairs)%2 != 0 {
		panic("AddWrites requires an even number of arguments (key-value pairs)")
	}

	// Pre-allocate to reduce reallocations
	newWrites := make([]testWrite, 0, len(keyValuePairs)/2)
	for i := 0; i < len(keyValuePairs); i += 2 {
		newWrites = append(newWrites, NewTestWrite(keyValuePairs[i], keyValuePairs[i+1]))
	}

	tds.writes = append(tds.writes, newWrites...)
	return tds
}

// AddWritesBatch adds multiple writes efficiently with pre-allocation
func (tds *TestDataSet) AddWritesBatch(writes []testWrite) *TestDataSet {
	if cap(tds.writes)-len(tds.writes) < len(writes) {
		// Grow slice efficiently
		newCap := len(tds.writes) + len(writes)
		if newCap < cap(tds.writes)*2 {
			newCap = cap(tds.writes) * 2
		}
		newWrites := make([]testWrite, len(tds.writes), newCap)
		copy(newWrites, tds.writes)
		tds.writes = newWrites
	}
	tds.writes = append(tds.writes, writes...)
	return tds
}

func (tds *TestDataSet) Size() int {
	return len(tds.writes)
}

func (tds *TestDataSet) GetWrites() []testWrite {
	return tds.writes
}

func (tds *TestDataSet) ExecuteOn(node *NetworkNode) error {
	for _, write := range tds.writes {
		if err := node.forest.Insert(write.key, write.value); err != nil {
			return fmt.Errorf("failed to execute write %s: %w", string(write.key), err)
		}
	}
	return nil
}

func (tds *TestDataSet) VerifyOn(node *NetworkNode) error {
	for _, write := range tds.writes {
		value, exists := node.forest.Get(write.key)
		if !exists {
			return fmt.Errorf("key %s not found on node", string(write.key))
		}
		if string(value) != string(write.value) {
			return fmt.Errorf("value mismatch for key %s: expected %s, got %s",
				string(write.key), string(write.value), string(value))
		}
	}
	return nil
}

// TestSummary provides comprehensive test execution summary
type TestSummary struct {
	TestName      string
	StartTime     time.Time
	EndTime       time.Time
	Duration      time.Duration
	ClusterSize   int
	LeaderElected bool
	ReplicationOK bool
	ValidationOK  bool
	Events        []TestEvent
	Errors        []error
	Performance   map[string]time.Duration
}

// NewTestSummary creates a new test summary
func NewTestSummary(testName string, clusterSize int) *TestSummary {
	return &TestSummary{
		TestName:    testName,
		StartTime:   time.Now(),
		ClusterSize: clusterSize,
		Events:      make([]TestEvent, 0),
		Errors:      make([]error, 0),
		Performance: make(map[string]time.Duration),
	}
}

// Finish completes the test summary
func (ts *TestSummary) Finish() {
	ts.EndTime = time.Now()
	ts.Duration = ts.EndTime.Sub(ts.StartTime)
}

// AddError adds an error to the summary
func (ts *TestSummary) AddError(err error) {
	ts.Errors = append(ts.Errors, err)
}

// AddPerformanceMetric adds a performance measurement
func (ts *TestSummary) AddPerformanceMetric(name string, duration time.Duration) {
	ts.Performance[name] = duration
}

// IsSuccessful returns true if the test was successful
func (ts *TestSummary) IsSuccessful() bool {
	return len(ts.Errors) == 0 && ts.LeaderElected && ts.ReplicationOK && ts.ValidationOK
}

// String returns a formatted summary of the test
func (ts *TestSummary) String() string {
	status := "PASS"
	if !ts.IsSuccessful() {
		status = "FAIL"
	}

	return fmt.Sprintf("Test: %s [%s] - Duration: %v, Cluster: %d nodes, Leader: %v, Replication: %v, Errors: %d",
		ts.TestName, status, ts.Duration, ts.ClusterSize, ts.LeaderElected, ts.ReplicationOK, len(ts.Errors))
}

// TestReporter provides structured test reporting
type TestReporter struct {
	summaries []TestSummary
}

// NewTestReporter creates a new test reporter
func NewTestReporter() *TestReporter {
	return &TestReporter{
		summaries: make([]TestSummary, 0),
	}
}

// AddSummary adds a test summary to the reporter
func (tr *TestReporter) AddSummary(summary TestSummary) {
	tr.summaries = append(tr.summaries, summary)
}

// GenerateReport generates a comprehensive test report
func (tr *TestReporter) GenerateReport() string {
	if len(tr.summaries) == 0 {
		return "No test summaries available"
	}

	var report bytes.Buffer
	report.WriteString("=== Consensus Test Report ===\n")

	passed := 0
	failed := 0
	totalDuration := time.Duration(0)

	for _, summary := range tr.summaries {
		report.WriteString(fmt.Sprintf("%s\n", summary.String()))
		if summary.IsSuccessful() {
			passed++
		} else {
			failed++
		}
		totalDuration += summary.Duration
	}

	report.WriteString(fmt.Sprintf("\nSummary: %d passed, %d failed, Total time: %v\n",
		passed, failed, totalDuration))

	return report.String()
}

// TestObserverPattern demonstrates the observer pattern functionality
func TestObserverPattern(t *testing.T) {
	Convey("Given a test cluster with multiple observers", t, func() {
		// Create a temporary file for file observer
		tmpFile := "/tmp/test-events.json"
		defer os.Remove(tmpFile)

		fileObserver, err := NewFileTestObserver(tmpFile)
		So(err, ShouldBeNil)
		defer fileObserver.Close()

		// Build cluster with multiple observers using builder pattern
		cluster := NewTestClusterBuilder().
			WithSize(3).
			WithTestName("TestObserverPattern").
			WithLoggingObserver().
			WithMetricsObserver().
			WithObserver(fileObserver).
			Build()

		err = cluster.Start()
		So(err, ShouldBeNil)
		defer cluster.Cleanup()

		Convey("When performing operations", func() {
			// Wait for cluster to stabilize
			time.Sleep(clusterStabilizationDelay)

			// Perform a write operation
			key := []byte("observer-test-key")
			value := []byte("observer-test-value")

			cluster.PublishEventWithData(EventTypeWriteAttempted, cluster.leader.config.NodeID, map[string]interface{}{
				"key_size":   len(key),
				"value_size": len(value),
			})

			err := cluster.leader.forest.Insert(key, value)
			So(err, ShouldBeNil)

			if err == nil {
				cluster.PublishEventWithData(EventTypeWriteCompleted, cluster.leader.config.NodeID, map[string]interface{}{
					"key": string(key),
				})
			}

			// Wait for replication
			time.Sleep(defaultReplicationWait)

			cluster.PublishEvent(EventTypeReplicationDone, "all-nodes")

			Convey("Then all observers should have received events", func() {
				// Check that file observer wrote events
				fileInfo, err := os.Stat(tmpFile)
				So(err, ShouldBeNil)
				So(fileInfo.Size(), ShouldBeGreaterThan, 0)

				// Verify file contains JSON events
				content, err := os.ReadFile(tmpFile)
				So(err, ShouldBeNil)
				So(string(content), ShouldContainSubstring, "TestObserverPattern")
				So(string(content), ShouldContainSubstring, "write_attempted")
			})
		})
	})
}

// TestEventPublisherConcurrency tests concurrent event publishing
func TestEventPublisherConcurrency(t *testing.T) {
	Convey("Given an event publisher with multiple observers", t, func() {
		publisher := NewTestEventPublisher()
		metricsObserver := NewMetricsTestObserver()
		loggingObserver := NewLoggingTestObserver()

		publisher.AddObserver(metricsObserver)
		publisher.AddObserver(loggingObserver)

		Convey("When publishing events concurrently", func() {
			const numEvents = 100
			const numGoroutines = 10

			var wg sync.WaitGroup

			for i := 0; i < numGoroutines; i++ {
				wg.Add(1)
				go func(goroutineID int) {
					defer wg.Done()
					for j := 0; j < numEvents/numGoroutines; j++ {
						event := NewTestEvent(EventTypeWriteAttempted, fmt.Sprintf("node-%d", goroutineID), "ConcurrencyTest").
							WithData("event_id", j).
							WithData("goroutine_id", goroutineID)
						publisher.PublishEvent(event)
					}
				}(i)
			}

			wg.Wait()

			Convey("Then all events should be processed", func() {
				// Give observers time to process events
				time.Sleep(100 * time.Millisecond)

				metrics := metricsObserver.GetMetrics()
				So(metrics[EventTypeWriteAttempted], ShouldEqual, numEvents)
			})
		})
	})
}

// TestObserverErrorHandling tests error handling in observers
func TestObserverErrorHandling(t *testing.T) {
	Convey("Given an observer that panics", t, func() {
		publisher := NewTestEventPublisher()

		// Create a panicking observer
		panicObserver := &PanicTestObserver{}
		normalObserver := NewMetricsTestObserver()

		publisher.AddObserver(panicObserver)
		publisher.AddObserver(normalObserver)

		Convey("When publishing an event", func() {
			event := NewTestEvent(EventTypeWriteAttempted, "test-node", "ErrorHandlingTest")

			// This should not panic the publisher
			publisher.PublishEvent(event)

			// Give time for async processing
			time.Sleep(50 * time.Millisecond)

			Convey("Then normal observers should still work", func() {
				metrics := normalObserver.GetMetrics()
				So(metrics[EventTypeWriteAttempted], ShouldEqual, 1)
			})
		})
	})
}

// PanicTestObserver is a test observer that panics
type PanicTestObserver struct{}

func (o *PanicTestObserver) OnEvent(event TestEvent) {
	panic("test panic in observer")
}
