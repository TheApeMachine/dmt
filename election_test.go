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
