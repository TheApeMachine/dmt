package dmt

import (
	"net"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestNewNetworkNode(t *testing.T) {
	Convey("Given network configuration", t, func() {
		config := NetworkConfig{
			ListenAddr:   ":0", // Random port
			NodeID:       "test-node",
			SyncInterval: time.Second,
		}

		forest, err := NewForest(ForestConfig{})
		So(err, ShouldBeNil)
		defer forest.Close()

		Convey("When creating a new network node", func() {
			node, err := NewNetworkNode(config, forest)
			So(err, ShouldBeNil)
			defer node.Close()

			Convey("Then it should be properly initialized", func() {
				So(node, ShouldNotBeNil)
				So(node.config, ShouldResemble, config)
				So(node.forest, ShouldEqual, forest)
				So(node.peers, ShouldNotBeNil)
				So(node.merkleTree, ShouldNotBeNil)
				So(node.metrics, ShouldNotBeNil)
				So(node.listener, ShouldNotBeNil)
			})

			Convey("And it should be listening on a port", func() {
				addr := node.listener.Addr().(*net.TCPAddr)
				So(addr.Port, ShouldBeGreaterThan, 0)
			})
		})
	})
}

func TestPeerConnections(t *testing.T) {
	Convey("Given two network nodes", t, func() {
		// Create first node
		forest1, err := NewForest(ForestConfig{})
		So(err, ShouldBeNil)
		defer forest1.Close()

		node1, err := NewNetworkNode(NetworkConfig{
			ListenAddr:   ":0",
			NodeID:       "node1",
			SyncInterval: time.Second,
		}, forest1)
		So(err, ShouldBeNil)
		defer node1.Close()

		addr1 := node1.listener.Addr().String()

		// Create second node that connects to first
		forest2, err := NewForest(ForestConfig{})
		So(err, ShouldBeNil)
		defer forest2.Close()

		node2, err := NewNetworkNode(NetworkConfig{
			ListenAddr:   ":0",
			NodeID:       "node2",
			PeerAddrs:    []string{addr1},
			SyncInterval: time.Second,
		}, forest2)
		So(err, ShouldBeNil)
		defer node2.Close()

		Convey("When waiting for connection establishment", func() {
			// Wait for connection to be established
			time.Sleep(2 * time.Second)

			Convey("Then peers should be connected", func() {
				node1.peersMutex.RLock()
				node2.peersMutex.RLock()
				defer node1.peersMutex.RUnlock()
				defer node2.peersMutex.RUnlock()

				// At least one node should see the other as peer
				totalPeers := len(node1.peers) + len(node2.peers)
				So(totalPeers, ShouldBeGreaterThan, 0)
			})
		})
	})
}

func TestNetworkSync(t *testing.T) {
	Convey("Given connected network nodes", t, func() {
		// Create first node
		forest1, err := NewForest(ForestConfig{})
		So(err, ShouldBeNil)
		defer forest1.Close()

		node1, err := NewNetworkNode(NetworkConfig{
			ListenAddr:   ":0",
			NodeID:       "node1",
			SyncInterval: 100 * time.Millisecond,
		}, forest1)
		So(err, ShouldBeNil)
		defer node1.Close()

		addr1 := node1.listener.Addr().String()

		// Create second node
		forest2, err := NewForest(ForestConfig{})
		So(err, ShouldBeNil)
		defer forest2.Close()

		node2, err := NewNetworkNode(NetworkConfig{
			ListenAddr:   ":0",
			NodeID:       "node2",
			PeerAddrs:    []string{addr1},
			SyncInterval: 100 * time.Millisecond,
		}, forest2)
		So(err, ShouldBeNil)
		defer node2.Close()

		Convey("When inserting data on first node", func() {
			node1.merkleTree.Insert([]byte("key1"), []byte("value1"))
			node1.merkleTree.Rebuild()

			// Wait for sync
			time.Sleep(300 * time.Millisecond)

			Convey("Then data should be synced to second node", func() {
				exists := node2.merkleTree.Verify([]byte("key1"), []byte("value1"))
				So(exists, ShouldBeTrue)
			})
		})
	})
}

func TestNetworkInsert(t *testing.T) {
	Convey("Given a network node", t, func() {
		forest, err := NewForest(ForestConfig{})
		So(err, ShouldBeNil)
		defer forest.Close()

		node, err := NewNetworkNode(NetworkConfig{
			ListenAddr:   ":0",
			NodeID:       "test-node",
			SyncInterval: time.Second,
		}, forest)
		So(err, ShouldBeNil)
		defer node.Close()

		Convey("When inserting data directly", func() {
			node.merkleTree.Insert([]byte("key"), []byte("value"))
			node.merkleTree.Rebuild()

			Convey("Then it should be verifiable", func() {
				exists := node.merkleTree.Verify([]byte("key"), []byte("value"))
				So(exists, ShouldBeTrue)
			})
		})
	})
}

func TestNetworkMetrics(t *testing.T) {
	Convey("Given a network node", t, func() {
		forest, err := NewForest(ForestConfig{})
		So(err, ShouldBeNil)
		defer forest.Close()

		node, err := NewNetworkNode(NetworkConfig{
			ListenAddr:   ":0",
			NodeID:       "test-node",
			SyncInterval: time.Second,
		}, forest)
		So(err, ShouldBeNil)
		defer node.Close()

		Convey("When getting metrics", func() {
			metrics := node.GetMetrics()

			Convey("Then metrics should be available", func() {
				So(metrics, ShouldNotBeNil)
				So(metrics["network"], ShouldNotBeNil)
				networkStats := metrics["network"].(map[string]interface{})
				So(networkStats["peer_count"], ShouldNotBeNil)
			})
		})
	})
}

func TestNetworkClose(t *testing.T) {
	Convey("Given a network node with peers", t, func() {
		forest, err := NewForest(ForestConfig{})
		So(err, ShouldBeNil)
		defer forest.Close()

		node, err := NewNetworkNode(NetworkConfig{
			ListenAddr:   ":0",
			NodeID:       "test-node",
			SyncInterval: time.Second,
		}, forest)
		So(err, ShouldBeNil)

		Convey("When closing the node", func() {
			err := node.Close()

			Convey("Then it should close successfully", func() {
				So(err, ShouldBeNil)

				// Verify listener is closed
				_, err := net.Dial("tcp", node.listener.Addr().String())
				So(err, ShouldNotBeNil)

				// Verify context is cancelled
				select {
				case <-node.ctx.Done():
					So(true, ShouldBeTrue)
				default:
					So(false, ShouldBeTrue, "Context not cancelled")
				}
			})
		})
	})
}

func TestNetworkBroadcast(t *testing.T) {
	Convey("Given a network node with peers", t, func() {
		forest, err := NewForest(ForestConfig{})
		So(err, ShouldBeNil)
		defer forest.Close()

		node, err := NewNetworkNode(NetworkConfig{
			ListenAddr:   ":0",
			NodeID:       "test-node",
			SyncInterval: time.Second,
		}, forest)
		So(err, ShouldBeNil)
		defer node.Close()

		Convey("When broadcasting an insert", func() {
			node.BroadcastInsert([]byte("broadcast-key"), []byte("broadcast-value"))

			Convey("Then metrics should be updated", func() {
				metrics := node.GetMetrics()
				networkStats := metrics["network"].(map[string]interface{})
				So(networkStats["bytes_tx"], ShouldBeGreaterThan, 0)
			})
		})
	})
}
