/*
package dmt implements race condition detection tests.
These tests are designed to detect race conditions and memory issues
under concurrent access patterns using Go's race detector.
*/
package dmt

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestRaceConditions(t *testing.T) {
	Convey("Race condition detection tests", t, func() {

		Convey("Tree concurrent operations should be race-free", func() {
			tree, err := NewTree("")
			So(err, ShouldBeNil)
			defer tree.Close()

			const numGoroutines = 10
			const numOperations = 100
			var wg sync.WaitGroup

			// Test concurrent inserts
			wg.Add(numGoroutines)
			for i := 0; i < numGoroutines; i++ {
				go func(id int) {
					defer wg.Done()
					for j := 0; j < numOperations; j++ {
						key := []byte(fmt.Sprintf("key-%d-%d", id, j))
						value := []byte(fmt.Sprintf("value-%d-%d", id, j))
						tree.Insert(key, value)
					}
				}(i)
			}

			// Test concurrent reads while writing
			wg.Add(numGoroutines)
			for i := 0; i < numGoroutines; i++ {
				go func(id int) {
					defer wg.Done()
					for j := 0; j < numOperations; j++ {
						key := []byte(fmt.Sprintf("key-%d-%d", id, j))
						tree.Get(key)
						tree.Seek(key)
						tree.AVG() // Test performance metrics access
					}
				}(i)
			}

			wg.Wait()
		})

		Convey("MerkleTree concurrent operations should be race-free", func() {
			merkle := NewMerkleTree()

			const numGoroutines = 10
			const numOperations = 100
			var wg sync.WaitGroup

			// Test concurrent inserts and rebuilds
			wg.Add(numGoroutines)
			for i := 0; i < numGoroutines; i++ {
				go func(id int) {
					defer wg.Done()
					for j := 0; j < numOperations; j++ {
						key := []byte(fmt.Sprintf("key-%d-%d", id, j))
						value := []byte(fmt.Sprintf("value-%d-%d", id, j))
						merkle.Insert(key, value)

						// Periodically rebuild to test concurrent access
						if j%10 == 0 {
							merkle.Rebuild()
						}
					}
				}(i)
			}

			// Test concurrent reads and verifications
			wg.Add(numGoroutines)
			for i := 0; i < numGoroutines; i++ {
				go func(id int) {
					defer wg.Done()
					for j := 0; j < numOperations; j++ {
						key := []byte(fmt.Sprintf("key-%d-%d", id, j))
						value := []byte(fmt.Sprintf("value-%d-%d", id, j))
						merkle.Verify(key, value)
						merkle.GetMemoryUsage()

						// Test proof generation under concurrent access
						if j%20 == 0 {
							merkle.GetProof(key)
						}
					}
				}(i)
			}

			wg.Wait()
		})

		Convey("Election concurrent operations should be race-free", func() {
			config := ElectionConfig{
				ElectionTimeout:   100 * time.Millisecond,
				HeartbeatInterval: 50 * time.Millisecond,
				QuorumSize:        2,
			}

			// Create a mock network node for testing
			forest, err := NewForest(ForestConfig{})
			So(err, ShouldBeNil)
			defer forest.Close()

			networkConfig := NetworkConfig{
				ListenAddr: ":0", // Use any available port
				NodeID:     "test-node",
			}
			node, err := NewNetworkNode(networkConfig, forest)
			So(err, ShouldBeNil)
			defer node.Close()

			election := NewElection(config, node)
			defer election.Close()

			const numGoroutines = 5
			const numOperations = 50
			var wg sync.WaitGroup

			// Test concurrent state access
			wg.Add(numGoroutines)
			for i := 0; i < numGoroutines; i++ {
				go func() {
					defer wg.Done()
					for j := 0; j < numOperations; j++ {
						election.getState()
						election.getCurrentTerm()
						election.getLastLogIndex()
						election.getLastLogTerm()
						election.isLeader()
						election.getCommitIndex()
						election.getLastApplied()

						// Small delay to allow other goroutines to run
						time.Sleep(time.Microsecond)
					}
				}()
			}

			// Test concurrent log operations
			wg.Add(numGoroutines)
			for i := 0; i < numGoroutines; i++ {
				go func(id int) {
					defer wg.Done()
					for j := 0; j < numOperations; j++ {
						key := []byte(fmt.Sprintf("key-%d-%d", id, j))
						value := []byte(fmt.Sprintf("value-%d-%d", id, j))
						election.appendLogEntry(key, value)

						time.Sleep(time.Microsecond)
					}
				}(i)
			}

			// Test concurrent timer operations
			wg.Add(numGoroutines)
			for i := 0; i < numGoroutines; i++ {
				go func() {
					defer wg.Done()
					for j := 0; j < numOperations; j++ {
						election.resetElectionTimer()
						election.resetHeartbeatTimer()
						election.getHeartbeatChannel()

						time.Sleep(time.Microsecond)
					}
				}()
			}

			wg.Wait()
		})

		Convey("Forest concurrent operations should be race-free", func() {
			forest, err := NewForest(ForestConfig{})
			So(err, ShouldBeNil)
			defer forest.Close()

			// Add multiple trees to test concurrent access
			for i := 0; i < 3; i++ {
				// Forest now manages a single tree, no need to add additional trees
			}

			const numGoroutines = 10
			const numOperations = 100
			var wg sync.WaitGroup

			// Test concurrent inserts
			wg.Add(numGoroutines)
			for i := 0; i < numGoroutines; i++ {
				go func(id int) {
					defer wg.Done()
					for j := 0; j < numOperations; j++ {
						key := []byte(fmt.Sprintf("key-%d-%d", id, j))
						value := []byte(fmt.Sprintf("value-%d-%d", id, j))
						forest.Insert(key, value)
					}
				}(i)
			}

			// Test concurrent reads
			wg.Add(numGoroutines)
			for i := 0; i < numGoroutines; i++ {
				go func(id int) {
					defer wg.Done()
					for j := 0; j < numOperations; j++ {
						key := []byte(fmt.Sprintf("key-%d-%d", id, j))
						forest.Get(key)
						forest.Seek(key)
						forest.getTree()
					}
				}(i)
			}

			// Test concurrent tree additions
			wg.Add(2)
			for i := 0; i < 2; i++ {
				go func() {
					defer wg.Done()
					for j := 0; j < 10; j++ {
						// Forest now manages a single tree, no need to add additional trees
						time.Sleep(10 * time.Millisecond)
					}
				}()
			}

			wg.Wait()
		})

		Convey("Metrics concurrent operations should be race-free", func() {
			metrics := NewMetrics()

			const numGoroutines = 10
			const numOperations = 100
			var wg sync.WaitGroup

			// Test concurrent metric recording
			wg.Add(numGoroutines)
			for i := 0; i < numGoroutines; i++ {
				go func(id int) {
					defer wg.Done()
					for j := 0; j < numOperations; j++ {
						metrics.RecordInsert(time.Millisecond, 100)
						metrics.RecordLookup(time.Millisecond)
						metrics.RecordSync(time.Millisecond, 200)
						metrics.RecordConflict()
						metrics.UpdatePeerCount(int32(id))
						metrics.SetLeader(id%2 == 0)
						metrics.RecordVote(fmt.Sprintf("voter-%d", id))
						metrics.SetNodeRole(fmt.Sprintf("role-%d", id), float64(id))
					}
				}(i)
			}

			// Test concurrent metric reading
			wg.Add(numGoroutines)
			for i := 0; i < numGoroutines; i++ {
				go func() {
					defer wg.Done()
					for j := 0; j < numOperations; j++ {
						metrics.GetMetrics()
						time.Sleep(time.Microsecond)
					}
				}()
			}

			wg.Wait()
		})
	})
}

func TestMemoryLeaks(t *testing.T) {
	Convey("Memory leak detection tests", t, func() {

		Convey("MerkleTree should not leak memory with bounded cache", func() {
			merkle := NewMerkleTreeWithLimit(100) // Small limit to test eviction

			// Insert many entries to trigger cache eviction
			for i := 0; i < 1000; i++ {
				key := []byte(fmt.Sprintf("key-%d", i))
				value := []byte(fmt.Sprintf("value-%d", i))
				merkle.Insert(key, value)
				merkle.Rebuild()
			}

			leafCount, _, cacheSize := merkle.GetMemoryUsage()
			So(leafCount, ShouldEqual, 1000)
			So(cacheSize, ShouldBeLessThanOrEqualTo, 100) // Cache should be bounded
		})

		Convey("NetworkNode should manage peer connections within limits", func() {
			forest, err := NewForest(ForestConfig{})
			So(err, ShouldBeNil)
			defer forest.Close()

			config := NetworkConfig{
				ListenAddr: ":0",
				NodeID:     "test-node",
			}
			node, err := NewNetworkNode(config, forest)
			So(err, ShouldBeNil)
			defer node.Close()

			// Simulate adding many peers (more than the limit)
			node.peersMutex.Lock()
			for i := 0; i < 150; i++ { // More than maxPeers (100)
				addr := fmt.Sprintf("peer-%d", i)
				node.peers[addr] = &peer{addr: addr}
			}
			node.peersMutex.Unlock()

			// Trigger cleanup
			node.performMemoryCleanup()

			node.peersMutex.RLock()
			peerCount := len(node.peers)
			node.peersMutex.RUnlock()

			So(peerCount, ShouldBeLessThanOrEqualTo, node.maxPeers)
		})

		Convey("Election log should be bounded to prevent memory leaks", func() {
			config := ElectionConfig{
				ElectionTimeout:   100 * time.Millisecond,
				HeartbeatInterval: 50 * time.Millisecond,
				QuorumSize:        1,
			}

			forest, err := NewForest(ForestConfig{})
			So(err, ShouldBeNil)
			defer forest.Close()

			networkConfig := NetworkConfig{
				ListenAddr: ":0",
				NodeID:     "test-node",
			}
			node, err := NewNetworkNode(networkConfig, forest)
			So(err, ShouldBeNil)
			defer node.Close()

			election := NewElection(config, node)
			defer election.Close()

			// Add many log entries
			for i := 0; i < 2000; i++ {
				key := []byte(fmt.Sprintf("key-%d", i))
				value := []byte(fmt.Sprintf("value-%d", i))
				election.appendLogEntry(key, value)
			}

			// Trigger cleanup
			node.cleanupElectionLog()

			election.logMutex.RLock()
			logSize := len(election.log)
			election.logMutex.RUnlock()

			So(logSize, ShouldBeLessThanOrEqualTo, 1000) // Should be bounded
		})
	})
}

func TestStressConditions(t *testing.T) {
	Convey("Stress testing for race conditions and memory issues", t, func() {

		Convey("High concurrency stress test", func() {
			forest, err := NewForest(ForestConfig{})
			So(err, ShouldBeNil)
			defer forest.Close()

			// Forest already has a tree, no need to add additional trees

			const numGoroutines = 50
			const numOperations = 200
			var wg sync.WaitGroup

			// High concurrency insert/read operations
			for i := 0; i < numGoroutines; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for j := 0; j < numOperations; j++ {
						key := []byte(fmt.Sprintf("stress-key-%d-%d", id, j))
						value := []byte(fmt.Sprintf("stress-value-%d-%d", id, j))

						// Mix of operations
						switch j % 4 {
						case 0:
							forest.Insert(key, value)
						case 1:
							forest.Get(key)
						case 2:
							forest.Seek(key)
						case 3:
							if t := forest.getTree(); t != nil {
								t.AVG()
							}
						}

						// Force context switches
						if j%10 == 0 {
							runtime.Gosched()
						}
					}
				}(i)
			}

			wg.Wait()
		})

		Convey("Memory pressure test", func() {
			merkle := NewMerkleTree()

			// Create memory pressure with large data
			const numEntries = 10000
			for i := 0; i < numEntries; i++ {
				key := []byte(fmt.Sprintf("large-key-%d", i))
				value := make([]byte, 1024) // 1KB values
				for j := range value {
					value[j] = byte(i % 256)
				}
				merkle.Insert(key, value)

				// Rebuild periodically to test memory management
				if i%100 == 0 {
					merkle.Rebuild()
				}
			}

			// Verify memory usage is reasonable
			leafCount, _, cacheSize := merkle.GetMemoryUsage()
			So(leafCount, ShouldEqual, numEntries)
			So(cacheSize, ShouldBeLessThan, numEntries) // Cache should be smaller than total entries
		})

		Convey("Timer race condition stress test", func() {
			config := ElectionConfig{
				ElectionTimeout:   10 * time.Millisecond, // Very short timeouts
				HeartbeatInterval: 5 * time.Millisecond,
				QuorumSize:        1,
			}

			forest, err := NewForest(ForestConfig{})
			So(err, ShouldBeNil)
			defer forest.Close()

			networkConfig := NetworkConfig{
				ListenAddr: ":0",
				NodeID:     "stress-node",
			}
			node, err := NewNetworkNode(networkConfig, forest)
			So(err, ShouldBeNil)
			defer node.Close()

			election := NewElection(config, node)
			defer election.Close()

			const numGoroutines = 20
			const duration = 100 * time.Millisecond
			var wg sync.WaitGroup

			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()

			// Stress test timer operations
			for i := 0; i < numGoroutines; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						select {
						case <-ctx.Done():
							return
						default:
							election.resetElectionTimer()
							election.resetHeartbeatTimer()
							election.getHeartbeatChannel()
							time.Sleep(time.Microsecond)
						}
					}
				}()
			}

			wg.Wait()
		})
	})
}

func TestThreadSafety(t *testing.T) {
	Convey("Thread safety validation tests", t, func() {

		Convey("All public APIs should be thread-safe", func() {
			// Test Tree API thread safety
			tree, err := NewTree("")
			So(err, ShouldBeNil)
			defer tree.Close()

			var wg sync.WaitGroup
			const numGoroutines = 10
			const numOps = 50

			// Test all Tree methods concurrently
			for i := 0; i < numGoroutines; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for j := 0; j < numOps; j++ {
						key := []byte(fmt.Sprintf("api-key-%d-%d", id, j))
						value := []byte(fmt.Sprintf("api-value-%d-%d", id, j))

						tree.Insert(key, value)
						tree.Get(key)
						tree.Seek(key)
						tree.AVG()
						tree.GetLogState()
						tree.UpdateTerm(uint64(j))
					}
				}(i)
			}

			wg.Wait()
		})

		Convey("MerkleTree API should be thread-safe", func() {
			merkle := NewMerkleTree()

			var wg sync.WaitGroup
			const numGoroutines = 10
			const numOps = 50

			// Test all MerkleTree methods concurrently
			for i := 0; i < numGoroutines; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for j := 0; j < numOps; j++ {
						key := []byte(fmt.Sprintf("merkle-key-%d-%d", id, j))
						value := []byte(fmt.Sprintf("merkle-value-%d-%d", id, j))

						merkle.Insert(key, value)
						merkle.Verify(key, value)
						merkle.GetMemoryUsage()

						if j%10 == 0 {
							merkle.Rebuild()
							merkle.GetProof(key)
						}
					}
				}(i)
			}

			wg.Wait()
		})

		Convey("Forest API should be thread-safe", func() {
			forest, err := NewForest(ForestConfig{})
			So(err, ShouldBeNil)
			defer forest.Close()

			// Forest already has a tree, no need to add additional trees

			var wg sync.WaitGroup
			const numGoroutines = 10
			const numOps = 50

			// Test all Forest methods concurrently
			for i := 0; i < numGoroutines; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for j := 0; j < numOps; j++ {
						key := []byte(fmt.Sprintf("forest-key-%d-%d", id, j))
						value := []byte(fmt.Sprintf("forest-value-%d-%d", id, j))

						forest.Insert(key, value)
						forest.Get(key)
						forest.Seek(key)
						forest.getTree()

						// Forest now manages a single tree, no need to add additional trees
					}
				}(i)
			}

			wg.Wait()
		})
	})
}
