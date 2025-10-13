package dmt

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestNewPersistentStore(t *testing.T) {
	Convey("Given a temporary directory", t, func() {
		tmpDir := createUniqueTestDir("radix-test")
		defer cleanupTestDir(tmpDir)

		Convey("When creating a new persistent store", func() {
			store, err := NewPersistentStore(tmpDir)
			So(err, ShouldBeNil)
			defer store.Close()

			Convey("Then the store should be properly initialized", func() {
				So(store, ShouldNotBeNil)
				So(store.walFile, ShouldNotBeNil)
				So(store.walWriter, ShouldNotBeNil)
				So(store.snapCount, ShouldEqual, 1000)
			})

			Convey("And the WAL file should exist", func() {
				_, err := os.Stat(filepath.Join(tmpDir, "wal.log"))
				So(err, ShouldBeNil)
			})
		})
	})
}

func TestLogInsert(t *testing.T) {
	Convey("Given a new persistent store", t, func() {
		tmpDir := createUniqueTestDir("radix-test")
		defer cleanupTestDir(tmpDir)

		store, err := NewPersistentStore(tmpDir)
		So(err, ShouldBeNil)
		defer store.Close()

		Convey("When logging an insert operation", func() {
			key := []byte("test-key")
			value := []byte("test-value")
			term := uint64(1)
			index := uint64(1)

			err := store.LogInsert(key, value, term, index)
			So(err, ShouldBeNil)

			Convey("Then the state should be updated", func() {
				lastTerm, lastIndex := store.GetLastState()
				So(lastTerm, ShouldEqual, term)
				So(lastIndex, ShouldEqual, index)
			})
		})
	})
}

func TestLoadLastState(t *testing.T) {
	Convey("Given a persistent store with existing entries", t, func() {
		tmpDir := createUniqueTestDir("radix-test")
		defer cleanupTestDir(tmpDir)

		store, err := NewPersistentStore(tmpDir)
		So(err, ShouldBeNil)
		defer store.Close()

		// Add some entries
		entries := []struct {
			key   []byte
			value []byte
			term  uint64
			index uint64
		}{
			{[]byte("key1"), []byte("value1"), 1, 1},
			{[]byte("key2"), []byte("value2"), 1, 2},
			{[]byte("key3"), []byte("value3"), 2, 3},
		}

		for _, e := range entries {
			err := store.LogInsert(e.key, e.value, e.term, e.index)
			So(err, ShouldBeNil)
		}

		Convey("When creating a new store with the same WAL", func() {
			store.Close()
			newStore, err := NewPersistentStore(tmpDir)
			So(err, ShouldBeNil)
			defer newStore.Close()

			Convey("Then it should load the last state correctly", func() {
				term, index := newStore.GetLastState()
				So(term, ShouldEqual, entries[len(entries)-1].term)
				So(index, ShouldEqual, entries[len(entries)-1].index)
			})
		})
	})
}

func TestCreateSnapshot(t *testing.T) {
	Convey("Given a tree with entries", t, func() {
		tmpDir := createUniqueTestDir("radix-test")
		defer cleanupTestDir(tmpDir)

		tree, err := NewTree(tmpDir)
		So(err, ShouldBeNil)
		defer tree.Close()

		// Add entries to the tree
		for i := 1; i <= 10; i++ {
			key := fmt.Sprintf("key%d", i)
			value := fmt.Sprintf("value%d", i)
			tree.Insert([]byte(key), []byte(value))
		}

		Convey("When creating a snapshot", func() {
			err := tree.CreateSnapshot()
			So(err, ShouldBeNil)

			Convey("Then a snapshot file should exist", func() {
				files, err := os.ReadDir(filepath.Join(tmpDir, "snapshot"))
				So(err, ShouldBeNil)
				So(len(files), ShouldBeGreaterThan, 0)

				Convey("And the WAL should be truncated", func() {
					walInfo, err := os.Stat(filepath.Join(tmpDir, "wal.log"))
					So(err, ShouldBeNil)
					// WAL should now only contain the snapshot entry
					So(walInfo.Size(), ShouldBeLessThan, 100)
				})
			})
		})
	})
}

func TestTruncateWAL(t *testing.T) {
	Convey("Given a persistent store with a large WAL", t, func() {
		tmpDir := createUniqueTestDir("radix-test")
		defer cleanupTestDir(tmpDir)

		store, err := NewPersistentStore(tmpDir)
		So(err, ShouldBeNil)
		defer store.Close()

		// Add many entries
		for i := uint64(1); i <= 100; i++ {
			err := store.LogInsert([]byte("key"), []byte("value"), 1, i)
			So(err, ShouldBeNil)
		}

		originalSize, err := os.Stat(filepath.Join(tmpDir, "wal.log"))
		So(err, ShouldBeNil)

		Convey("When truncating the WAL", func() {
			err := store.truncateWAL()
			So(err, ShouldBeNil)

			Convey("Then the WAL should be smaller", func() {
				newSize, err := os.Stat(filepath.Join(tmpDir, "wal.log"))
				So(err, ShouldBeNil)
				So(newSize.Size(), ShouldBeLessThan, originalSize.Size())
			})

			Convey("And the state should be preserved", func() {
				term, index := store.GetLastState()
				So(term, ShouldEqual, uint64(1))
				So(index, ShouldEqual, uint64(100))
			})
		})
	})
}

func TestGetLastState(t *testing.T) {
	Convey("Given a persistent store", t, func() {
		tmpDir := createUniqueTestDir("radix-test")
		defer cleanupTestDir(tmpDir)

		store, err := NewPersistentStore(tmpDir)
		So(err, ShouldBeNil)
		defer store.Close()

		Convey("When getting initial state", func() {
			term, index := store.GetLastState()
			So(term, ShouldEqual, uint64(0))
			So(index, ShouldEqual, uint64(0))
		})

		Convey("When getting state after updates", func() {
			err := store.LogInsert([]byte("key"), []byte("value"), 2, 5)
			So(err, ShouldBeNil)

			term, index := store.GetLastState()
			So(term, ShouldEqual, uint64(2))
			So(index, ShouldEqual, uint64(5))
		})
	})
}

func TestWALReplayCorrectness(t *testing.T) {
	Convey("Given a persistent store with multiple entries", t, func() {
		tmpDir := createUniqueTestDir("radix-test")
		defer cleanupTestDir(tmpDir)

		store, err := NewPersistentStore(tmpDir)
		So(err, ShouldBeNil)

		// Add test entries
		testEntries := []struct {
			key   []byte
			value []byte
			term  uint64
			index uint64
		}{
			{[]byte("key1"), []byte("value1"), 1, 1},
			{[]byte("key2"), []byte("value2"), 1, 2},
			{[]byte("key3"), []byte("value3"), 2, 3},
			{[]byte("key1"), []byte("updated1"), 2, 4}, // Update existing key
		}

		for _, e := range testEntries {
			err := store.LogInsert(e.key, e.value, e.term, e.index)
			So(err, ShouldBeNil)
		}

		store.Close()

		Convey("When simulating a crash and recovery", func() {
			// Create new tree with same persist directory
			tree, err := NewTree(tmpDir)
			So(err, ShouldBeNil)
			defer tree.Close()

			Convey("Then the tree should contain all entries with correct values", func() {
				// Check that all entries are recovered correctly
				value, ok := tree.Get([]byte("key1"))
				So(ok, ShouldBeTrue)
				So(string(value), ShouldEqual, "updated1") // Should have updated value

				value, ok = tree.Get([]byte("key2"))
				So(ok, ShouldBeTrue)
				So(string(value), ShouldEqual, "value2")

				value, ok = tree.Get([]byte("key3"))
				So(ok, ShouldBeTrue)
				So(string(value), ShouldEqual, "value3")

				// Check term and index are recovered
				term, index := tree.GetLogState()
				So(term, ShouldEqual, uint64(2))
				So(index, ShouldEqual, uint64(4))
			})
		})
	})
}

func TestSnapshotCreationAndRecovery(t *testing.T) {
	Convey("Given a tree with multiple entries", t, func() {
		tmpDir := createUniqueTestDir("radix-test")
		defer cleanupTestDir(tmpDir)

		tree, err := NewTree(tmpDir)
		So(err, ShouldBeNil)

		// Add test data
		testData := map[string]string{
			"key1": "value1",
			"key2": "value2",
			"key3": "value3",
			"key4": "value4",
		}

		for key, value := range testData {
			tree.Insert([]byte(key), []byte(value))
		}

		// Update term
		tree.UpdateTerm(5)

		Convey("When creating a snapshot", func() {
			err := tree.CreateSnapshot()
			So(err, ShouldBeNil)

			tree.Close()

			Convey("Then recovery should restore complete tree state", func() {
				newTree, err := NewTree(tmpDir)
				So(err, ShouldBeNil)
				defer newTree.Close()

				// Verify all data is recovered
				for key, expectedValue := range testData {
					value, ok := newTree.Get([]byte(key))
					So(ok, ShouldBeTrue)
					So(string(value), ShouldEqual, expectedValue)
				}

				// Verify term is recovered
				term, _ := newTree.GetLogState()
				So(term, ShouldEqual, uint64(5))
			})
		})
	})
}

func TestSnapshotWithVariousTreeSizes(t *testing.T) {
	Convey("Given trees of various sizes", t, func() {
		sizes := []int{0, 1, 10, 100}

		for _, size := range sizes {
			Convey(fmt.Sprintf("When testing with %d entries", size), func() {
				tmpDir := createUniqueTestDir(fmt.Sprintf("radix-test-%d", size))
				defer cleanupTestDir(tmpDir)

				tree, err := NewTree(tmpDir)
				So(err, ShouldBeNil)

				// Add entries
				for i := 0; i < size; i++ {
					key := fmt.Sprintf("key%d", i)
					value := fmt.Sprintf("value%d", i)
					tree.Insert([]byte(key), []byte(value))
				}

				// Create snapshot
				err = tree.CreateSnapshot()
				So(err, ShouldBeNil)

				tree.Close()

				// Recover and verify
				newTree, err := NewTree(tmpDir)
				So(err, ShouldBeNil)
				defer newTree.Close()

				// Verify all entries are recovered
				for i := 0; i < size; i++ {
					key := fmt.Sprintf("key%d", i)
					expectedValue := fmt.Sprintf("value%d", i)
					value, ok := newTree.Get([]byte(key))
					So(ok, ShouldBeTrue)
					So(string(value), ShouldEqual, expectedValue)
				}
			})
		}
	})
}

func TestTermPersistenceAcrossRestarts(t *testing.T) {
	Convey("Given a tree with term updates", t, func() {
		tmpDir := createUniqueTestDir("radix-test")
		defer cleanupTestDir(tmpDir)

		tree, err := NewTree(tmpDir)
		So(err, ShouldBeNil)

		// Add some data
		tree.Insert([]byte("key1"), []byte("value1"))

		// Update term multiple times without new data
		tree.UpdateTerm(2)
		tree.UpdateTerm(3)
		tree.UpdateTerm(5)

		tree.Close()

		Convey("When restarting", func() {
			newTree, err := NewTree(tmpDir)
			So(err, ShouldBeNil)
			defer newTree.Close()

			Convey("Then the latest term should be preserved", func() {
				term, index := newTree.GetLogState()
				So(term, ShouldEqual, uint64(5))
				So(index, ShouldEqual, uint64(1)) // Only one data entry

				// Data should still be there
				value, ok := newTree.Get([]byte("key1"))
				So(ok, ShouldBeTrue)
				So(string(value), ShouldEqual, "value1")
			})
		})
	})
}

func TestMixedSnapshotAndWALRecovery(t *testing.T) {
	Convey("Given a tree with snapshot and subsequent WAL entries", t, func() {
		tmpDir := createUniqueTestDir("radix-test")
		defer cleanupTestDir(tmpDir)

		tree, err := NewTree(tmpDir)
		So(err, ShouldBeNil)

		// Add initial data
		tree.Insert([]byte("snap1"), []byte("snapvalue1"))
		tree.Insert([]byte("snap2"), []byte("snapvalue2"))

		// Create snapshot
		err = tree.CreateSnapshot()
		So(err, ShouldBeNil)

		// Add more data after snapshot
		tree.Insert([]byte("wal1"), []byte("walvalue1"))
		tree.Insert([]byte("wal2"), []byte("walvalue2"))
		tree.UpdateTerm(10)

		tree.Close()

		Convey("When recovering", func() {
			newTree, err := NewTree(tmpDir)
			So(err, ShouldBeNil)
			defer newTree.Close()

			Convey("Then both snapshot and WAL data should be recovered", func() {
				// Snapshot data
				value, ok := newTree.Get([]byte("snap1"))
				So(ok, ShouldBeTrue)
				So(string(value), ShouldEqual, "snapvalue1")

				value, ok = newTree.Get([]byte("snap2"))
				So(ok, ShouldBeTrue)
				So(string(value), ShouldEqual, "snapvalue2")

				// WAL data
				value, ok = newTree.Get([]byte("wal1"))
				So(ok, ShouldBeTrue)
				So(string(value), ShouldEqual, "walvalue1")

				value, ok = newTree.Get([]byte("wal2"))
				So(ok, ShouldBeTrue)
				So(string(value), ShouldEqual, "walvalue2")

				// Term should be latest
				term, _ := newTree.GetLogState()
				So(term, ShouldEqual, uint64(10))
			})
		})
	})
}
