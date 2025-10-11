package dmt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// IntegrationTestCluster represents a test cluster of nodes for integration testing
type IntegrationTestCluster struct {
	nodes   []*IntegrationTestNode
	tempDir string
}

// IntegrationTestNode represents a single node in the test cluster
type IntegrationTestNode struct {
	id      string
	forest  *Forest
	network *NetworkNode
	port    int
}

// NewIntegrationTestCluster creates a new test cluster with the specified number of nodes
func NewIntegrationTestCluster(nodeCount int) (*IntegrationTestCluster, error) {
	tempDir := filepath.Join(os.TempDir(), fmt.Sprintf("dmt-integration-test-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	cluster := &IntegrationTestCluster{
		nodes:   make([]*IntegrationTestNode, 0, nodeCount),
		tempDir: tempDir,
	}

	// Create nodes with sequential ports starting from a base port
	basePort := 16380
	peerAddrs := make([]string, nodeCount)
	for i := 0; i < nodeCount; i++ {
		peerAddrs[i] = fmt.Sprintf("127.0.0.1:%d", basePort+i)
	}

	// Create each node
	for i := 0; i < nodeCount; i++ {
		nodeID := fmt.Sprintf("node-%d", i)
		nodePort := basePort + i
		nodePersistDir := filepath.Join(tempDir, nodeID)

		// Create peer list excluding self
		nodePeers := make([]string, 0, nodeCount-1)
		for j, addr := range peerAddrs {
			if j != i {
				nodePeers = append(nodePeers, addr)
			}
		}

		config := ForestConfig{
			PersistDir: nodePersistDir,
			NodeID:     nodeID,
			Network: &NetworkConfig{
				ListenAddr:   fmt.Sprintf(":%d", nodePort),
				PeerAddrs:    nodePeers,
				NodeID:       nodeID,
				SyncInterval: 100 * time.Millisecond,
			},
		}

		forest, err := NewForest(config)
		if err != nil {
			cluster.Close()
			return nil, fmt.Errorf("failed to create node %s: %w", nodeID, err)
		}

		node := &IntegrationTestNode{
			id:      nodeID,
			forest:  forest,
			network: forest.network,
			port:    nodePort,
		}

		cluster.nodes = append(cluster.nodes, node)
	}

	return cluster, nil
}

// Close shuts down all nodes in the cluster and cleans up resources
func (tc *IntegrationTestCluster) Close() {
	for _, node := range tc.nodes {
		if node.forest != nil {
			node.forest.Close()
		}
	}
	if tc.tempDir != "" {
		os.RemoveAll(tc.tempDir)
	}
}

// WaitForLeaderElection waits for a leader to be elected in the cluster
func (tc *IntegrationTestCluster) WaitForLeaderElection(timeout time.Duration) (*IntegrationTestNode, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for leader election")
		case <-ticker.C:
			for _, node := range tc.nodes {
				if node.network != nil && node.network.election != nil && node.network.election.isLeader() {
					return node, nil
				}
			}
		}
	}
}

// GetLeader returns the current leader node, if any
func (tc *IntegrationTestCluster) GetLeader() *IntegrationTestNode {
	for _, node := range tc.nodes {
		if node.network != nil && node.network.election != nil && node.network.election.isLeader() {
			return node
		}
	}
	return nil
}

// WaitForDataConsistency waits for all nodes to have consistent data
func (tc *IntegrationTestCluster) WaitForDataConsistency(key []byte, expectedValue []byte, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for data consistency")
		case <-ticker.C:
			consistent := true
			for _, node := range tc.nodes {
				value, exists := node.forest.Get(key)
				if !exists || string(value) != string(expectedValue) {
					consistent = false
					break
				}
			}
			if consistent {
				return nil
			}
		}
	}
}

// PartitionNode simulates a network partition by closing a node's network connections
func (tc *IntegrationTestCluster) PartitionNode(nodeID string) error {
	for _, node := range tc.nodes {
		if node.id == nodeID {
			if node.network != nil {
				return node.network.Close()
			}
			return fmt.Errorf("node %s has no network", nodeID)
		}
	}
	return fmt.Errorf("node %s not found", nodeID)
}

func TestDistributedConsensus(t *testing.T) {
	Convey("Given a 3-node cluster", t, func() {
		cluster, err := NewIntegrationTestCluster(3)
		So(err, ShouldBeNil)
		defer cluster.Close()

		Convey("When the cluster starts", func() {
			// Wait for leader election
			leader, err := cluster.WaitForLeaderElection(5 * time.Second)
			So(err, ShouldBeNil)
			So(leader, ShouldNotBeNil)

			Convey("Then a leader should be elected", func() {
				So(leader.network.election.isLeader(), ShouldBeTrue)
			})

			Convey("When inserting data through the leader", func() {
				key := []byte("test-key")
				value := []byte("test-value")

				err := leader.forest.Insert(key, value)
				So(err, ShouldBeNil)

				Convey("Then all nodes should eventually have the data", func() {
					err := cluster.WaitForDataConsistency(key, value, 3*time.Second)
					So(err, ShouldBeNil)

					// Verify each node has the data
					for _, node := range cluster.nodes {
						nodeValue, exists := node.forest.Get(key)
						So(exists, ShouldBeTrue)
						So(string(nodeValue), ShouldEqual, string(value))
					}
				})
			})

			Convey("When inserting data through a follower", func() {
				// Find a follower node
				var follower *IntegrationTestNode
				for _, node := range cluster.nodes {
					if !node.network.election.isLeader() {
						follower = node
						break
					}
				}
				So(follower, ShouldNotBeNil)

				key := []byte("follower-key")
				value := []byte("follower-value")

				err := follower.forest.Insert(key, value)

				Convey("Then the insert should be rejected", func() {
					So(err, ShouldNotBeNil)
					So(err.Error(), ShouldContainSubstring, "not leader")
				})
			})
		})
	})
}

func TestNetworkPartitions(t *testing.T) {
	Convey("Given a 3-node cluster", t, func() {
		cluster, err := NewIntegrationTestCluster(3)
		So(err, ShouldBeNil)
		defer cluster.Close()

		// Wait for initial leader election
		leader, err := cluster.WaitForLeaderElection(5 * time.Second)
		So(err, ShouldBeNil)

		Convey("When the leader is partitioned", func() {
			// Insert initial data
			key1 := []byte("before-partition")
			value1 := []byte("initial-data")
			err := leader.forest.Insert(key1, value1)
			So(err, ShouldBeNil)

			// Wait for replication
			err = cluster.WaitForDataConsistency(key1, value1, 2*time.Second)
			So(err, ShouldBeNil)

			// Partition the leader
			err = cluster.PartitionNode(leader.id)
			So(err, ShouldBeNil)

			// Wait for new leader election
			time.Sleep(1 * time.Second)
			newLeader := cluster.GetLeader()

			Convey("Then a new leader should be elected", func() {
				So(newLeader, ShouldNotBeNil)
				So(newLeader.id, ShouldNotEqual, leader.id)

				Convey("And the new leader should accept writes", func() {
					key2 := []byte("after-partition")
					value2 := []byte("new-leader-data")

					err := newLeader.forest.Insert(key2, value2)
					So(err, ShouldBeNil)

					// Verify data exists on remaining nodes
					for _, node := range cluster.nodes {
						if node.id != leader.id { // Skip partitioned node
							nodeValue, exists := node.forest.Get(key2)
							So(exists, ShouldBeTrue)
							So(string(nodeValue), ShouldEqual, string(value2))
						}
					}
				})
			})
		})
	})
}

func TestNodeFailureRecovery(t *testing.T) {
	Convey("Given a 3-node cluster", t, func() {
		cluster, err := NewIntegrationTestCluster(3)
		So(err, ShouldBeNil)
		defer cluster.Close()

		// Wait for leader election
		leader, err := cluster.WaitForLeaderElection(5 * time.Second)
		So(err, ShouldBeNil)

		Convey("When inserting data before failure", func() {
			key := []byte("persistent-key")
			value := []byte("persistent-value")

			err := leader.forest.Insert(key, value)
			So(err, ShouldBeNil)

			// Wait for replication
			err = cluster.WaitForDataConsistency(key, value, 2*time.Second)
			So(err, ShouldBeNil)

			Convey("And a follower node fails and recovers", func() {
				// Find a follower
				var follower *IntegrationTestNode
				for _, node := range cluster.nodes {
					if !node.network.election.isLeader() {
						follower = node
						break
					}
				}
				So(follower, ShouldNotBeNil)

				// Simulate node failure by closing it
				followerID := follower.id
				followerPort := follower.port
				follower.forest.Close()

				// Insert more data while node is down
				key2 := []byte("during-failure")
				value2 := []byte("missed-data")
				err := leader.forest.Insert(key2, value2)
				So(err, ShouldBeNil)

				// Simulate node recovery by recreating it
				nodePersistDir := filepath.Join(cluster.tempDir, followerID)
				nodePeers := make([]string, 0)
				for _, node := range cluster.nodes {
					if node.id != followerID {
						nodePeers = append(nodePeers, fmt.Sprintf("127.0.0.1:%d", node.port))
					}
				}

				config := ForestConfig{
					PersistDir: nodePersistDir,
					NodeID:     followerID,
					Network: &NetworkConfig{
						ListenAddr:   fmt.Sprintf(":%d", followerPort),
						PeerAddrs:    nodePeers,
						NodeID:       followerID,
						SyncInterval: 100 * time.Millisecond,
					},
				}

				recoveredForest, err := NewForest(config)
				So(err, ShouldBeNil)

				// Update the cluster with recovered node
				for i, node := range cluster.nodes {
					if node.id == followerID {
						cluster.nodes[i] = &IntegrationTestNode{
							id:      followerID,
							forest:  recoveredForest,
							network: recoveredForest.network,
							port:    followerPort,
						}
						break
					}
				}

				Convey("Then the recovered node should have all data", func() {
					// Wait for sync
					time.Sleep(2 * time.Second)

					// Check original data
					value1, exists1 := recoveredForest.Get(key)
					So(exists1, ShouldBeTrue)
					So(string(value1), ShouldEqual, string(value))

					// Check data inserted during failure
					value2, exists2 := recoveredForest.Get(key2)
					So(exists2, ShouldBeTrue)
					So(string(value2), ShouldEqual, string(value2))
				})
			})
		})
	})
}

func TestConcurrentOperations(t *testing.T) {
	Convey("Given a 3-node cluster", t, func() {
		cluster, err := NewIntegrationTestCluster(3)
		So(err, ShouldBeNil)
		defer cluster.Close()

		// Wait for leader election
		leader, err := cluster.WaitForLeaderElection(5 * time.Second)
		So(err, ShouldBeNil)

		Convey("When performing concurrent operations", func() {
			const numOperations = 50
			var wg sync.WaitGroup
			errors := make(chan error, numOperations)

			// Perform concurrent inserts
			for i := 0; i < numOperations; i++ {
				wg.Add(1)
				go func(index int) {
					defer wg.Done()
					key := []byte(fmt.Sprintf("concurrent-key-%d", index))
					value := []byte(fmt.Sprintf("concurrent-value-%d", index))

					if err := leader.forest.Insert(key, value); err != nil {
						errors <- err
					}
				}(i)
			}

			wg.Wait()
			close(errors)

			Convey("Then all operations should succeed", func() {
				errorCount := 0
				for err := range errors {
					if err != nil {
						t.Logf("Concurrent operation error: %v", err)
						errorCount++
					}
				}
				So(errorCount, ShouldEqual, 0)

				Convey("And all data should be consistent across nodes", func() {
					// Wait for all data to replicate
					time.Sleep(2 * time.Second)

					// Verify each key exists on all nodes
					for i := 0; i < numOperations; i++ {
						key := []byte(fmt.Sprintf("concurrent-key-%d", i))
						expectedValue := []byte(fmt.Sprintf("concurrent-value-%d", i))

						for _, node := range cluster.nodes {
							value, exists := node.forest.Get(key)
							So(exists, ShouldBeTrue, fmt.Sprintf("Key %s missing on node %s", string(key), node.id))
							So(string(value), ShouldEqual, string(expectedValue), fmt.Sprintf("Value mismatch for key %s on node %s", string(key), node.id))
						}
					}
				})
			})
		})
	})
}

func TestDataPersistence(t *testing.T) {
	Convey("Given a single node with persistence", t, func() {
		tempDir := filepath.Join(os.TempDir(), fmt.Sprintf("dmt-persistence-test-%d", time.Now().UnixNano()))
		defer os.RemoveAll(tempDir)

		config := ForestConfig{
			PersistDir: tempDir,
			NodeID:     "persistence-test",
		}

		forest, err := NewForest(config)
		So(err, ShouldBeNil)

		Convey("When inserting data", func() {
			key := []byte("persistent-key")
			value := []byte("persistent-value")

			err := forest.Insert(key, value)
			So(err, ShouldBeNil)

			// Verify data exists
			retrievedValue, exists := forest.Get(key)
			So(exists, ShouldBeTrue)
			So(string(retrievedValue), ShouldEqual, string(value))

			// Close the forest
			forest.Close()

			Convey("Then data should persist after restart", func() {
				// Recreate forest with same persistence directory
				newForest, err := NewForest(config)
				So(err, ShouldBeNil)
				defer newForest.Close()

				// Verify data still exists
				retrievedValue, exists := newForest.Get(key)
				So(exists, ShouldBeTrue)
				So(string(retrievedValue), ShouldEqual, string(value))
			})
		})
	})
}
