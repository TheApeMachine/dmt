package dmt

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestNewPersistentStore(t *testing.T) {
	Convey("Given a temporary directory", t, func() {
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

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
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

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
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

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
	Convey("Given a persistent store with entries", t, func() {
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

		store, err := NewPersistentStore(tmpDir)
		So(err, ShouldBeNil)
		defer store.Close()

		// Add enough entries to trigger snapshot
		for i := uint64(1); i <= 1001; i++ {
			err := store.LogInsert([]byte("key"), []byte("value"), 1, i)
			So(err, ShouldBeNil)
		}

		Convey("When waiting for snapshot creation", func() {
			// Wait a bit for async snapshot to complete
			time.Sleep(100 * time.Millisecond)

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
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

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
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

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
