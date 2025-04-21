package dmt

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestNewMetrics(t *testing.T) {
	Convey("Given a new metrics instance", t, func() {
		metrics := NewMetrics()

		Convey("Then it should be properly initialized", func() {
			So(metrics, ShouldNotBeNil)
			So(metrics.insertLatency, ShouldNotBeNil)
			So(metrics.lookupLatency, ShouldNotBeNil)
			So(metrics.syncLatency, ShouldNotBeNil)
			So(metrics.networkLatency, ShouldNotBeNil)
		})

		Convey("And counters should be initialized to zero", func() {
			So(metrics.insertCount.Load(), ShouldEqual, 0)
			So(metrics.lookupCount.Load(), ShouldEqual, 0)
			So(metrics.syncCount.Load(), ShouldEqual, 0)
			So(metrics.conflictCount.Load(), ShouldEqual, 0)
		})
	})
}

func TestLatencyTracker(t *testing.T) {
	Convey("Given a new latency tracker", t, func() {
		tracker := NewLatencyTracker(100)

		Convey("When recording latencies", func() {
			tracker.RecordLatency(10 * time.Millisecond)
			tracker.RecordLatency(20 * time.Millisecond)
			tracker.RecordLatency(30 * time.Millisecond)

			Convey("Then average should be calculated correctly", func() {
				avg := tracker.AverageLatency()
				So(avg, ShouldEqual, 20*time.Millisecond)
			})
		})

		Convey("When no latencies are recorded", func() {
			avg := tracker.AverageLatency()
			So(avg, ShouldEqual, 0)
		})

		Convey("When window wraps around", func() {
			// Fill window
			for i := 0; i < 100; i++ {
				tracker.RecordLatency(10 * time.Millisecond)
			}
			// Add one more to wrap
			tracker.RecordLatency(20 * time.Millisecond)

			avg := tracker.AverageLatency()
			So(avg > 10*time.Millisecond, ShouldBeTrue)
			So(avg < 20*time.Millisecond, ShouldBeTrue)
		})
	})
}

func TestMetricsRecording(t *testing.T) {
	Convey("Given a metrics instance", t, func() {
		metrics := NewMetrics()

		Convey("When recording insert operations", func() {
			metrics.RecordInsert(10*time.Millisecond, 100)

			Convey("Then counters should be updated", func() {
				So(metrics.insertCount.Load(), ShouldEqual, 1)
				So(metrics.bytesTransmitted.Load(), ShouldEqual, 100)
				So(metrics.insertLatency.AverageLatency(), ShouldEqual, 10*time.Millisecond)
			})
		})

		Convey("When recording lookup operations", func() {
			metrics.RecordLookup(5 * time.Millisecond)

			Convey("Then counters should be updated", func() {
				So(metrics.lookupCount.Load(), ShouldEqual, 1)
				So(metrics.lookupLatency.AverageLatency(), ShouldEqual, 5*time.Millisecond)
			})
		})

		Convey("When recording sync operations", func() {
			metrics.RecordSync(15*time.Millisecond, 200)

			Convey("Then counters should be updated", func() {
				So(metrics.syncCount.Load(), ShouldEqual, 1)
				So(metrics.bytesReceived.Load(), ShouldEqual, 200)
				So(metrics.syncLatency.AverageLatency(), ShouldEqual, 15*time.Millisecond)
			})
		})

		Convey("When recording conflicts", func() {
			metrics.RecordConflict()

			Convey("Then conflict counter should be updated", func() {
				So(metrics.conflictCount.Load(), ShouldEqual, 1)
			})
		})
	})
}

func TestMetricsNetworkStats(t *testing.T) {
	Convey("Given a metrics instance", t, func() {
		metrics := NewMetrics()

		Convey("When updating peer count", func() {
			metrics.UpdatePeerCount(5)

			Convey("Then peer count should be updated", func() {
				So(metrics.peerCount.Load(), ShouldEqual, 5)
			})
		})

		Convey("When setting node role", func() {
			metrics.SetNodeRole("leader", 1.0)

			Convey("Then role should be updated", func() {
				So(metrics.nodeRole, ShouldEqual, "leader")
				So(metrics.nodeWeight, ShouldEqual, 1.0)
			})
		})

		Convey("When setting leader status", func() {
			metrics.SetLeader(true)

			Convey("Then leader status should be updated", func() {
				So(metrics.isLeader.Load(), ShouldBeTrue)
			})
		})
	})
}

func TestMetricsElectionStats(t *testing.T) {
	Convey("Given a metrics instance", t, func() {
		metrics := NewMetrics()

		Convey("When recording votes", func() {
			metrics.RecordVote("node1")
			metrics.RecordVote("node2")

			Convey("Then vote counters should be updated", func() {
				So(metrics.votesReceived.Load(), ShouldEqual, 2)
				So(metrics.lastVoter, ShouldEqual, "node2")
			})
		})
	})
}

func TestMetricsSnapshot(t *testing.T) {
	Convey("Given a metrics instance with recorded data", t, func() {
		metrics := NewMetrics()

		// Record various metrics
		metrics.RecordInsert(10*time.Millisecond, 100)
		metrics.RecordLookup(5 * time.Millisecond)
		metrics.RecordSync(15*time.Millisecond, 200)
		metrics.RecordConflict()
		metrics.UpdatePeerCount(5)
		metrics.SetNodeRole("follower", 0.5)
		metrics.SetLeader(false)
		metrics.RecordVote("node1")

		Convey("When getting metrics snapshot", func() {
			snapshot := metrics.GetMetrics()

			Convey("Then all metrics should be included", func() {
				operations := snapshot["operations"].(map[string]uint64)
				So(operations["insert"], ShouldEqual, 1)
				So(operations["lookup"], ShouldEqual, 1)
				So(operations["sync"], ShouldEqual, 1)
				So(operations["conflict"], ShouldEqual, 1)

				network := snapshot["network"].(map[string]interface{})
				So(network["bytes_tx"], ShouldEqual, uint64(100))
				So(network["bytes_rx"], ShouldEqual, uint64(200))
				So(network["peer_count"], ShouldEqual, int32(5))

				node := snapshot["node"].(map[string]interface{})
				So(node["is_leader"], ShouldBeFalse)
				So(node["role"], ShouldEqual, "follower")
				So(node["weight"], ShouldEqual, 0.5)

				election := snapshot["election"].(map[string]interface{})
				So(election["votes_received"], ShouldEqual, uint64(1))
				So(election["last_voter"], ShouldEqual, "node1")
			})
		})
	})
}
