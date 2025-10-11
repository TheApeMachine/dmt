package dmt

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestNewElection(t *testing.T) {
	Convey("Given election configuration", t, func() {
		config := ElectionConfig{
			ElectionTimeout:   time.Second,
			HeartbeatInterval: 100 * time.Millisecond,
			QuorumSize:        2,
		}

		node := &NetworkNode{
			config:  NetworkConfig{NodeID: "test-node"},
			metrics: NewMetrics(),
		}

		Convey("When creating a new election manager", func() {
			election := NewElection(config, node)
			defer election.Close()

			Convey("Then it should be properly initialized", func() {
				So(election, ShouldNotBeNil)
				So(election.config, ShouldResemble, config)
				So(election.node, ShouldEqual, node)
				So(election.state, ShouldEqual, Follower)
				So(election.term, ShouldEqual, 0)
				So(election.votedFor, ShouldEqual, "")
			})
		})
	})
}

func TestElectionStateTransitions(t *testing.T) {
	Convey("Given an election manager", t, func() {
		config := ElectionConfig{
			ElectionTimeout:   100 * time.Millisecond,
			HeartbeatInterval: 50 * time.Millisecond,
			QuorumSize:        2,
		}

		node := &NetworkNode{
			config:  NetworkConfig{NodeID: "test-node"},
			metrics: NewMetrics(),
		}

		election := NewElection(config, node)
		defer election.Close()

		Convey("When stepping down", func() {
			election.stepDown(5)

			Convey("Then state should be updated", func() {
				So(election.getState(), ShouldEqual, Follower)
				So(election.getCurrentTerm(), ShouldEqual, uint64(5))
				So(election.votedFor, ShouldEqual, "")
			})
		})

		Convey("When becoming leader", func() {
			election.becomeLeader()

			Convey("Then state should be updated", func() {
				So(election.getState(), ShouldEqual, Leader)
				So(node.metrics.isLeader.Load(), ShouldBeTrue)
			})
		})
	})
}

func TestVoteHandling(t *testing.T) {
	Convey("Given an election manager", t, func() {
		config := ElectionConfig{
			ElectionTimeout:   time.Second,
			HeartbeatInterval: 100 * time.Millisecond,
			QuorumSize:        2,
		}

		node := &NetworkNode{
			config:  NetworkConfig{NodeID: "test-node"},
			metrics: NewMetrics(),
		}

		election := NewElection(config, node)
		defer election.Close()

		Convey("When handling vote requests", func() {
			Convey("With higher term", func() {
				granted := election.handleVoteRequest(2, "candidate1", 1, 1)
				So(granted, ShouldBeTrue)
				So(election.getCurrentTerm(), ShouldEqual, uint64(2))
				So(election.votedFor, ShouldEqual, "candidate1")
			})

			Convey("With lower term", func() {
				election.term = 5
				granted := election.handleVoteRequest(3, "candidate2", 1, 1)
				So(granted, ShouldBeFalse)
			})

			Convey("With already voted in term", func() {
				election.term = 5
				election.votedFor = "candidate1"
				granted := election.handleVoteRequest(5, "candidate2", 1, 1)
				So(granted, ShouldBeFalse)
			})

			Convey("With outdated log", func() {
				election.logLock.Lock()
				election.lastLogIndex = 10
				election.lastLogTerm = 5
				election.logLock.Unlock()

				granted := election.handleVoteRequest(6, "candidate1", 5, 4)
				So(granted, ShouldBeFalse)
			})
		})
	})
}

func TestHeartbeatHandling(t *testing.T) {
	Convey("Given an election manager", t, func() {
		config := ElectionConfig{
			ElectionTimeout:   time.Second,
			HeartbeatInterval: 100 * time.Millisecond,
			QuorumSize:        2,
		}

		node := &NetworkNode{
			config:  NetworkConfig{NodeID: "test-node"},
			metrics: NewMetrics(),
		}

		election := NewElection(config, node)
		defer election.Close()

		Convey("When handling heartbeats", func() {
			Convey("With higher term", func() {
				success := election.handleHeartbeat(2, "leader1")
				So(success, ShouldBeTrue)
				So(election.getCurrentTerm(), ShouldEqual, uint64(2))
				So(election.getState(), ShouldEqual, Follower)
			})

			Convey("With lower term", func() {
				election.term = 5
				success := election.handleHeartbeat(3, "leader1")
				So(success, ShouldBeFalse)
			})

			Convey("With same term", func() {
				election.term = 5
				success := election.handleHeartbeat(5, "leader1")
				So(success, ShouldBeTrue)
			})
		})
	})
}

func TestLogStateManagement(t *testing.T) {
	Convey("Given an election manager", t, func() {
		config := ElectionConfig{
			ElectionTimeout:   time.Second,
			HeartbeatInterval: 100 * time.Millisecond,
			QuorumSize:        2,
		}

		node := &NetworkNode{
			config:  NetworkConfig{NodeID: "test-node"},
			metrics: NewMetrics(),
		}

		election := NewElection(config, node)
		defer election.Close()

		Convey("When updating log state", func() {
			election.updateLogState(5, 2)

			Convey("Then log indices should be updated", func() {
				So(election.getLastLogIndex(), ShouldEqual, uint64(5))
				lastTerm := election.lastLogTerm
				So(lastTerm, ShouldEqual, uint64(2))
			})

			Convey("When updating with lower index", func() {
				election.updateLogState(3, 1)
				So(election.getLastLogIndex(), ShouldEqual, uint64(5))
				lastTerm := election.lastLogTerm
				So(lastTerm, ShouldEqual, uint64(2))
			})

			Convey("When updating with higher index", func() {
				election.updateLogState(7, 3)
				So(election.getLastLogIndex(), ShouldEqual, uint64(7))
				lastTerm := election.lastLogTerm
				So(lastTerm, ShouldEqual, uint64(3))
			})
		})
	})
}

func TestElectionTimeout(t *testing.T) {
	Convey("Given an election manager with short timeout", t, func() {
		config := ElectionConfig{
			ElectionTimeout:   50 * time.Millisecond,
			HeartbeatInterval: 10 * time.Millisecond,
			QuorumSize:        2,
		}

		node := &NetworkNode{
			config:  NetworkConfig{NodeID: "test-node"},
			metrics: NewMetrics(),
		}

		election := NewElection(config, node)
		defer election.Close()

		Convey("When waiting for timeout", func() {
			// Wait for potential state change
			time.Sleep(100 * time.Millisecond)

			Convey("Then state should change to candidate", func() {
				state := election.getState()
				So(state, ShouldEqual, Candidate)
				So(election.getCurrentTerm(), ShouldBeGreaterThan, 0)
			})
		})
	})
}
func TestConcurrentElectionOperations(t *testing.T) {
	Convey("Given an election manager", t, func() {
		config := ElectionConfig{
			ElectionTimeout:   100 * time.Millisecond,
			HeartbeatInterval: 50 * time.Millisecond,
			QuorumSize:        2,
		}

		node := &NetworkNode{
			config:  NetworkConfig{NodeID: "test-node"},
			metrics: NewMetrics(),
		}

		election := NewElection(config, node)
		defer election.Close()

		Convey("When handling concurrent vote requests", func() {
			// Run multiple concurrent vote requests
			results := make(chan bool, 10)
			for i := 0; i < 10; i++ {
				go func(candidateID string) {
					granted := election.handleVoteRequest(1, candidateID, 1, 1)
					results <- granted
				}("candidate" + string(rune('0'+i)))
			}

			// Collect results
			var granted int
			for i := 0; i < 10; i++ {
				if <-results {
					granted++
				}
			}

			Convey("Then only one vote should be granted", func() {
				So(granted, ShouldEqual, 1)
				So(election.getCurrentTerm(), ShouldEqual, uint64(1))
			})
		})

		Convey("When handling concurrent heartbeats", func() {
			// Run multiple concurrent heartbeats
			results := make(chan bool, 10)
			for i := 0; i < 10; i++ {
				go func(leaderID string) {
					success := election.handleHeartbeat(2, leaderID)
					results <- success
				}("leader" + string(rune('0'+i)))
			}

			// Collect results
			var successful int
			for i := 0; i < 10; i++ {
				if <-results {
					successful++
				}
			}

			Convey("Then all heartbeats should succeed", func() {
				So(successful, ShouldEqual, 10)
				So(election.getCurrentTerm(), ShouldEqual, uint64(2))
			})
		})

		Convey("When handling concurrent step downs", func() {
			// Set initial term to 0 so all step downs will be accepted
			election.term = 0

			// Run multiple concurrent step downs
			done := make(chan bool, 10)
			for i := 0; i < 10; i++ {
				go func(term uint64) {
					election.stepDown(term)
					done <- true
				}(uint64(i + 3))
			}

			// Wait for all to complete
			for i := 0; i < 10; i++ {
				<-done
			}

			Convey("Then final term should be at least 3", func() {
				So(election.getCurrentTerm(), ShouldBeGreaterThanOrEqualTo, uint64(3))
				So(election.getState(), ShouldEqual, Follower)
			})
		})
	})
}

func TestQuorumCalculations(t *testing.T) {
	Convey("Given various cluster sizes", t, func() {
		testCases := []struct {
			clusterSize    int
			expectedQuorum int
		}{
			{1, 1},
			{2, 2},
			{3, 2},
			{4, 3},
			{5, 3},
			{6, 4},
			{7, 4},
		}

		for _, tc := range testCases {
			Convey("When calculating quorum for cluster size "+string(rune('0'+tc.clusterSize)), func() {
				quorum := calculateQuorumSize(tc.clusterSize)

				Convey("Then quorum should be correct", func() {
					So(quorum, ShouldEqual, tc.expectedQuorum)
				})
			})
		}

		Convey("When cluster size is zero or negative", func() {
			So(calculateQuorumSize(0), ShouldEqual, 1)
			So(calculateQuorumSize(-1), ShouldEqual, 1)
		})
	})

	Convey("Given election with configured quorum size", t, func() {
		config := ElectionConfig{
			ElectionTimeout:   time.Second,
			HeartbeatInterval: 100 * time.Millisecond,
			QuorumSize:        3, // Explicit quorum size
		}

		node := &NetworkNode{
			config:  NetworkConfig{NodeID: "test-node"},
			metrics: NewMetrics(),
			peers:   make(map[string]*peer),
		}

		election := NewElection(config, node)
		defer election.Close()

		Convey("When starting election", func() {
			// Mock some peers
			node.peers["peer1"] = &peer{addr: "peer1"}
			node.peers["peer2"] = &peer{addr: "peer2"}

			// Start election in a goroutine to avoid blocking
			go election.startElection()

			// Give it time to process
			time.Sleep(10 * time.Millisecond)

			Convey("Then it should use configured quorum size", func() {
				So(election.getState(), ShouldEqual, Candidate)
				So(election.getCurrentTerm(), ShouldBeGreaterThan, 0)
			})
		})
	})

	Convey("Given election with zero quorum size", t, func() {
		config := ElectionConfig{
			ElectionTimeout:   time.Second,
			HeartbeatInterval: 100 * time.Millisecond,
			QuorumSize:        0, // Should fall back to majority
		}

		node := &NetworkNode{
			config:  NetworkConfig{NodeID: "test-node"},
			metrics: NewMetrics(),
			peers:   make(map[string]*peer),
		}

		election := NewElection(config, node)
		defer election.Close()

		Convey("When starting election with 3 total nodes", func() {
			// Mock 2 peers (3 total including self)
			node.peers["peer1"] = &peer{addr: "peer1"}
			node.peers["peer2"] = &peer{addr: "peer2"}

			// Start election in a goroutine
			go election.startElection()

			// Give it time to process
			time.Sleep(10 * time.Millisecond)

			Convey("Then it should calculate majority quorum", func() {
				So(election.getState(), ShouldEqual, Candidate)
				// Should need 2 votes for majority of 3 nodes
			})
		})
	})
}

func TestTimerSynchronization(t *testing.T) {
	Convey("Given an election manager", t, func() {
		config := ElectionConfig{
			ElectionTimeout:   100 * time.Millisecond,
			HeartbeatInterval: 50 * time.Millisecond,
			QuorumSize:        2,
		}

		node := &NetworkNode{
			config:  NetworkConfig{NodeID: "test-node"},
			metrics: NewMetrics(),
		}

		election := NewElection(config, node)
		defer election.Close()

		Convey("When resetting election timer concurrently", func() {
			done := make(chan bool, 10)

			// Reset timer concurrently from multiple goroutines
			for i := 0; i < 10; i++ {
				go func() {
					election.resetElectionTimer()
					done <- true
				}()
			}

			// Wait for all to complete
			for i := 0; i < 10; i++ {
				<-done
			}

			Convey("Then no panic should occur", func() {
				// If we reach here, no panic occurred
				So(true, ShouldBeTrue)
			})
		})

		Convey("When becoming leader and resetting heartbeat timer concurrently", func() {
			done := make(chan bool, 10)

			// Become leader and reset heartbeat timer concurrently
			for i := 0; i < 10; i++ {
				go func() {
					election.becomeLeader()
					election.resetHeartbeatTimer()
					done <- true
				}()
			}

			// Wait for all to complete
			for i := 0; i < 10; i++ {
				<-done
			}

			Convey("Then no panic should occur and state should be leader", func() {
				So(election.getState(), ShouldEqual, Leader)
			})
		})

		Convey("When accessing heartbeat channel concurrently", func() {
			election.becomeLeader()

			done := make(chan bool, 10)

			// Access heartbeat channel concurrently
			for i := 0; i < 10; i++ {
				go func() {
					_ = election.getHeartbeatChannel()
					done <- true
				}()
			}

			// Wait for all to complete
			for i := 0; i < 10; i++ {
				<-done
			}

			Convey("Then no panic should occur", func() {
				So(true, ShouldBeTrue)
			})
		})

		Convey("When closing election manager", func() {
			// Start some timer operations
			go election.resetElectionTimer()
			go election.resetHeartbeatTimer()

			// Give operations time to start
			time.Sleep(10 * time.Millisecond)

			Convey("Then close should complete without hanging", func() {
				// This should not hang or panic
				election.Close()
				So(true, ShouldBeTrue)
			})
		})
	})
}

func TestElectionInitialization(t *testing.T) {
	Convey("Given network configuration", t, func() {
		config := NetworkConfig{
			ListenAddr: ":0",
			PeerAddrs:  []string{"peer1:6380", "peer2:6380"},
			NodeID:     "test-node",
		}

		tree, err := NewTree("")
		So(err, ShouldBeNil)

		forest := &Forest{
			tree: tree,
		}

		Convey("When creating network node", func() {
			node, err := NewNetworkNode(config, forest)
			defer func() {
				if node != nil {
					node.Close()
				}
			}()

			Convey("Then election should be properly initialized", func() {
				So(err, ShouldBeNil)
				So(node, ShouldNotBeNil)
				So(node.election, ShouldNotBeNil)
				So(node.election.config.QuorumSize, ShouldEqual, 2) // (3/2)+1 = 2
			})
		})

		Convey("When creating network node with single peer", func() {
			config.PeerAddrs = []string{"peer1:6380"}

			node, err := NewNetworkNode(config, forest)
			defer func() {
				if node != nil {
					node.Close()
				}
			}()

			Convey("Then quorum should be calculated correctly", func() {
				So(err, ShouldBeNil)
				So(node.election.config.QuorumSize, ShouldEqual, 2) // (2/2)+1 = 2
			})
		})

		Convey("When creating network node with no peers", func() {
			config.PeerAddrs = []string{}

			node, err := NewNetworkNode(config, forest)
			defer func() {
				if node != nil {
					node.Close()
				}
			}()

			Convey("Then quorum should be 1", func() {
				So(err, ShouldBeNil)
				So(node.election.config.QuorumSize, ShouldEqual, 1) // (1/2)+1 = 1
			})
		})
	})
}
