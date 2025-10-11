package dmt

import (
	"fmt"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestSimpleReplication(t *testing.T) {
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
			node1.Close()
			node2.Close()
			time.Sleep(100 * time.Millisecond)
		}()

		// Wait for connection
		time.Sleep(500 * time.Millisecond)

		Convey("When we check initial state", func() {
			// Check which node becomes leader
			var leader, follower *NetworkNode

			for i := 0; i < 50; i++ { // Wait up to 5 seconds
				if node1.election.isLeader() {
					leader = node1
					follower = node2
					break
				} else if node2.election.isLeader() {
					leader = node2
					follower = node1
					break
				}
				time.Sleep(100 * time.Millisecond)
			}

			Convey("Then we should have a leader", func() {
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
}
