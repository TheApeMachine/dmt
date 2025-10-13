package dmt

import (
	"fmt"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// waitForPeerConnectivity waits until a node has established RPC connections
// to all configured peers or times out.
func waitForPeerConnectivity(node *NetworkNode, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		node.peersMutex.RLock()
		connected := len(node.peers)
		target := len(node.config.PeerAddrs)
		node.peersMutex.RUnlock()
		if connected >= target {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for peer connectivity")
}

// waitForLeaderElection waits until either node becomes leader and returns
// (leader, follower) or an error on timeout.
func waitForLeaderElection(n1, n2 *NetworkNode, timeout time.Duration) (*NetworkNode, *NetworkNode, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if n1.election != nil && n1.election.isLeader() {
			return n1, n2, nil
		}
		if n2.election != nil && n2.election.isLeader() {
			return n2, n1, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, nil, fmt.Errorf("no leader elected within %s", timeout)
}

func TestSimpleReplication(t *testing.T) {
	withNetworkTestLock(func() {
		Convey("Given a simple 2-node cluster", t, func() {
			// Create 2 nodes
			// Create nodes with proper forest setup
			config1 := ForestConfig{
				PersistDir: "/tmp/dmt-test-17001",
				Network: &NetworkConfig{
					ListenAddr:   ":17001",
					PeerAddrs:    []string{"localhost:17002"},
					NodeID:       "node-17001",
					SyncInterval: 100 * time.Millisecond,
				},
			}
			forest1, err := NewForest(config1)
			So(err, ShouldBeNil)
			node1 := forest1.network

			config2 := ForestConfig{
				PersistDir: "/tmp/dmt-test-17002",
				Network: &NetworkConfig{
					ListenAddr:   ":17002",
					PeerAddrs:    []string{"localhost:17001"},
					NodeID:       "node-17002",
					SyncInterval: 100 * time.Millisecond,
				},
			}
			forest2, err := NewForest(config2)
			So(err, ShouldBeNil)
			node2 := forest2.network

			defer func() {
				testCleanupWithGlobalReset(node1, node2, forest1, forest2)
			}()

			// Wait for both nodes to connect to their configured peers
			So(waitForPeerConnectivity(node1, 5*time.Second), ShouldBeNil)
			So(waitForPeerConnectivity(node2, 5*time.Second), ShouldBeNil)

			Convey("When we check initial state", func() {
				// Wait for leader election
				leader, follower, err := waitForLeaderElection(node1, node2, 10*time.Second)

				Convey("Then we should have a leader", func() {
					So(err, ShouldBeNil)
					So(leader, ShouldNotBeNil)
					So(follower, ShouldNotBeNil)
					So(leader.election.isLeader(), ShouldBeTrue)
					So(follower.election.isLeader(), ShouldBeFalse)

					Convey("When leader writes data", func() {
						key := []byte("test-key")
						value := []byte("test-value")

						err := leader.forest.Insert(key, value)
						if err != nil {
							fmt.Printf("Insert error: %v\n", err)
						}
						So(err, ShouldBeNil)

						// Wait a bit for apply process
						time.Sleep(200 * time.Millisecond)

						// Check leader has the data
						leaderValue, leaderExists := leader.forest.Get(key)

						// Debug information for leader
						fmt.Printf("After insert - Leader log index: %d, commit: %d, applied: %d\n",
							leader.election.getLastLogIndex(),
							leader.election.getCommitIndex(),
							leader.election.getLastApplied())
						fmt.Printf("Leader forest has tree: %v\n", leader.forest.tree != nil)
						fmt.Printf("Leader has data: %v, value: %v\n", leaderExists, leaderValue)

						So(leaderExists, ShouldBeTrue)
						So(leaderValue, ShouldResemble, value)

						// Wait for replication
						time.Sleep(1 * time.Second)

						Convey("Then follower should have the data", func() {
							followerValue, followerExists := follower.forest.Get(key)

							// Debug information
							fmt.Printf("Leader log index: %d, commit: %d, applied: %d\n",
								leader.election.getLastLogIndex(),
								leader.election.getCommitIndex(),
								leader.election.getLastApplied())
							fmt.Printf("Follower log index: %d, commit: %d, applied: %d\n",
								follower.election.getLastLogIndex(),
								follower.election.getCommitIndex(),
								follower.election.getLastApplied())
							fmt.Printf("Follower forest has tree: %v\n", follower.forest.tree != nil)
							fmt.Printf("Follower has data: %v, value: %v\n", followerExists, followerValue)

							So(followerExists, ShouldBeTrue)
							So(followerValue, ShouldResemble, value)
						})
					})
				})
			})
		})
	})
}
