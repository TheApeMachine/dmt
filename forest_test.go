package dmt

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestNewForest(t *testing.T) {
	Convey("Given a forest configuration", t, func() {
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

		config := ForestConfig{
			PersistDir: tmpDir,
		}

		Convey("When creating a new forest", func() {
			forest, err := NewForest(config)
			So(err, ShouldBeNil)
			defer forest.Close()

			Convey("Then it should be properly initialized", func() {
				So(forest, ShouldNotBeNil)
				So(forest.trees, ShouldNotBeNil)
				So(forest.updates, ShouldNotBeNil)
				So(forest.ctx, ShouldNotBeNil)
				So(forest.cancel, ShouldNotBeNil)
			})

			Convey("And it should have one initial tree", func() {
				So(len(forest.trees), ShouldEqual, 1)
				So(forest.trees[0], ShouldNotBeNil)
			})
		})
	})
}

func TestForestOperations(t *testing.T) {
	Convey("Given a new forest", t, func() {
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

		forest, err := NewForest(ForestConfig{PersistDir: tmpDir})
		So(err, ShouldBeNil)
		defer forest.Close()

		Convey("When performing insert operations", func() {
			forest.Insert([]byte("key1"), []byte("value1"))
			forest.Insert([]byte("key2"), []byte("value2"))

			Convey("Then the data should be retrievable", func() {
				value1, exists := forest.Get([]byte("key1"))
				So(exists, ShouldBeTrue)
				So(value1, ShouldResemble, []byte("value1"))

				value2, exists := forest.Get([]byte("key2"))
				So(exists, ShouldBeTrue)
				So(value2, ShouldResemble, []byte("value2"))
			})

			Convey("And seek operations should work", func() {
				value, exists := forest.Seek([]byte("key"))
				So(exists, ShouldBeTrue)
				So(value, ShouldResemble, []byte("value1")) // Should find first key alphabetically
			})
		})
	})
}

func TestForestSynchronization(t *testing.T) {
	Convey("Given a forest with multiple trees", t, func() {
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

		forest, err := NewForest(ForestConfig{PersistDir: tmpDir})
		So(err, ShouldBeNil)
		defer forest.Close()

		// Add a second tree
		tree2, err := NewTree("")
		So(err, ShouldBeNil)
		forest.AddTree(tree2)

		Convey("When inserting data", func() {
			forest.Insert([]byte("sync-key"), []byte("sync-value"))

			// Wait for sync to complete
			time.Sleep(100 * time.Millisecond)

			Convey("Then all trees should have the data", func() {
				for _, tree := range forest.trees {
					value, exists := tree.Get([]byte("sync-key"))
					So(exists, ShouldBeTrue)
					So(value, ShouldResemble, []byte("sync-value"))
				}
			})
		})
	})
}

func TestForestPerformance(t *testing.T) {
	Convey("Given a forest with multiple trees", t, func() {
		forest, err := NewForest(ForestConfig{})
		So(err, ShouldBeNil)
		defer forest.Close()

		// Add trees with different simulated performance characteristics
		tree1, err := NewTree("")
		So(err, ShouldBeNil)
		tree2, err := NewTree("")
		So(err, ShouldBeNil)

		forest.AddTree(tree1)
		forest.AddTree(tree2)

		Convey("When getting the fastest tree", func() {
			fastestTree := forest.getFastestTree()

			Convey("Then it should return a valid tree", func() {
				So(fastestTree, ShouldNotBeNil)
				So(fastestTree.AVG(), ShouldBeGreaterThanOrEqualTo, 0)
			})
		})
	})
}

func TestForestNetworking(t *testing.T) {
	Convey("Given a forest with network configuration", t, func() {
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

		config := ForestConfig{
			PersistDir: tmpDir,
			Network: &NetworkConfig{
				ListenAddr: ":0", // Use random port
				NodeID:     "test-node",
			},
		}

		forest, err := NewForest(config)
		So(err, ShouldBeNil)
		defer forest.Close()

		Convey("Then the network node should be initialized", func() {
			So(forest.network, ShouldNotBeNil)
			So(forest.network.config.NodeID, ShouldEqual, "test-node")
		})

		Convey("When inserting data with networking enabled", func() {
			forest.Insert([]byte("network-key"), []byte("network-value"))

			Convey("Then the network node should broadcast the insert", func() {
				// Verify local insertion worked
				value, exists := forest.Get([]byte("network-key"))
				So(exists, ShouldBeTrue)
				So(value, ShouldResemble, []byte("network-value"))

				// Network metrics should be updated
				metrics := forest.network.GetMetrics()
				networkStats := metrics["network"].(map[string]interface{})
				So(networkStats["bytes_tx"], ShouldBeGreaterThan, 0)
			})
		})
	})
}

func TestForestClose(t *testing.T) {
	Convey("Given a forest with multiple components", t, func() {
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

		config := ForestConfig{
			PersistDir: tmpDir,
			Network: &NetworkConfig{
				ListenAddr: ":0",
				NodeID:     "test-node",
			},
		}

		forest, err := NewForest(config)
		So(err, ShouldBeNil)

		// Add additional tree
		tree2, err := NewTree("")
		So(err, ShouldBeNil)
		forest.AddTree(tree2)

		Convey("When closing the forest", func() {
			forest.Close()

			Convey("Then the context should be cancelled", func() {
				select {
				case <-forest.ctx.Done():
					// Context was cancelled as expected
					So(true, ShouldBeTrue)
				default:
					So(false, ShouldBeTrue, "Context was not cancelled")
				}
			})
		})
	})
}
