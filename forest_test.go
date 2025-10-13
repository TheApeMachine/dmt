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
				So(forest.updates, ShouldNotBeNil)
				So(forest.ctx, ShouldNotBeNil)
				So(forest.cancel, ShouldNotBeNil)
			})

			Convey("And it should have one tree", func() {
				tree := forest.GetTree()
				So(tree, ShouldNotBeNil)
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

func TestForestSingleTree(t *testing.T) {
	Convey("Given a forest with a single tree", t, func() {
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

		forest, err := NewForest(ForestConfig{PersistDir: tmpDir})
		So(err, ShouldBeNil)
		defer forest.Close()

		Convey("When inserting data", func() {
			forest.Insert([]byte("single-key"), []byte("single-value"))

			Convey("Then the tree should have the data", func() {
				value, exists := forest.Get([]byte("single-key"))
				So(exists, ShouldBeTrue)
				So(value, ShouldResemble, []byte("single-value"))

				// Verify the underlying tree also has the data
				tree := forest.GetTree()
				So(tree, ShouldNotBeNil)
				treeValue, treeExists := tree.Get([]byte("single-key"))
				So(treeExists, ShouldBeTrue)
				So(treeValue, ShouldResemble, []byte("single-value"))
			})
		})
	})
}

func TestForestTreeAccess(t *testing.T) {
	Convey("Given a forest with a single tree", t, func() {
		forest, err := NewForest(ForestConfig{})
		So(err, ShouldBeNil)
		defer forest.Close()

		Convey("When getting the tree", func() {
			tree := forest.GetTree()

			Convey("Then it should return a valid tree", func() {
				So(tree, ShouldNotBeNil)
				So(tree.AVG(), ShouldBeGreaterThanOrEqualTo, 0)
			})

			Convey("And the internal getTree method should work", func() {
				internalTree := forest.getTree()
				So(internalTree, ShouldNotBeNil)
				So(internalTree, ShouldEqual, tree)
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
			// Wait for the single node to become leader
			time.Sleep(500 * time.Millisecond)

			err := forest.Insert([]byte("network-key"), []byte("network-value"))
			So(err, ShouldBeNil)

			Convey("Then the data should be stored locally", func() {
				// Wait for apply loop to process the committed entry
				time.Sleep(100 * time.Millisecond)

				// Verify local insertion worked
				value, exists := forest.Get([]byte("network-key"))
				So(exists, ShouldBeTrue)
				So(value, ShouldResemble, []byte("network-value"))

				// For single-node cluster, no network traffic is expected
				// Network metrics would only be updated in multi-node clusters
				metrics := forest.network.GetMetrics()
				So(metrics, ShouldNotBeNil)
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

		// Forest now manages a single tree, no need to add additional trees

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
