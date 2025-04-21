package dmt

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

func TestNewTree(t *testing.T) {
	Convey("Given a new tree", t, func() {
		tree, err := NewTree("")
		So(err, ShouldBeNil)

		Convey("When a new tree is created", func() {
			So(tree, ShouldNotBeNil)
		})
	})
}

func TestSeek(t *testing.T) {
	Convey("Given a new tree", t, func() {
		tree, err := NewTree("")
		So(err, ShouldBeNil)

		Convey("When a seek is performed", func() {
			value, ok := tree.Seek([]byte("test"))
			So(ok, ShouldBeTrue)
			So(value, ShouldEqual, []byte("test"))
		})
	})
}

func TestInsert(t *testing.T) {
	Convey("Given a new tree", t, func() {
		tree, err := NewTree("")
		So(err, ShouldBeNil)

		Convey("When an insert is performed", func() {
			newTree, ok := tree.Insert([]byte("test"), []byte("test"))
			So(ok, ShouldBeTrue)
			So(newTree, ShouldNotBeNil)

			// Verify the insert worked
			value, exists := newTree.Get([]byte("test"))
			So(exists, ShouldBeTrue)
			So(value, ShouldResemble, []byte("test"))
		})
	})
}

func TestGet(t *testing.T) {
	Convey("Given a new tree", t, func() {
		tree, err := NewTree("")
		So(err, ShouldBeNil)

		Convey("When a get is performed", func() {
			value, ok := tree.Get([]byte("test"))
			So(ok, ShouldBeTrue)
			So(value, ShouldEqual, []byte("test"))
		})
	})
}

func TestAVG(t *testing.T) {
	Convey("Given a new tree", t, func() {
		tree, err := NewTree("")
		So(err, ShouldBeNil)

		Convey("When a avg is performed", func() {
			avg := tree.AVG()
			So(avg, ShouldBeGreaterThan, 0)
		})
	})
}

func TestClose(t *testing.T) {
	Convey("Given a new tree", t, func() {
		tree, err := NewTree("")
		So(err, ShouldBeNil)

		Convey("When a close is performed", func() {
			err := tree.Close()
			So(err, ShouldBeNil)
		})
	})
}

func TestUpdateTerm(t *testing.T) {
	Convey("Given a new tree", t, func() {
		tree, err := NewTree("")
		So(err, ShouldBeNil)

		Convey("When a update term is performed", func() {
			tree.UpdateTerm(1)
			So(tree.term, ShouldEqual, 1)
		})
	})
}

func TestGetLogState(t *testing.T) {
	Convey("Given a new tree", t, func() {
		tree, err := NewTree("")
		So(err, ShouldBeNil)

		Convey("When a get log state is performed", func() {
			term, index := tree.GetLogState()
			So(term, ShouldEqual, 0)
			So(index, ShouldEqual, 0)
		})
	})
}

func TestTreeWithPersistence(t *testing.T) {
	Convey("Given a temporary directory", t, func() {
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

		Convey("When creating a tree with persistence", func() {
			tree, err := NewTree(tmpDir)
			So(err, ShouldBeNil)
			defer tree.Close()

			Convey("Then the persistence store should be initialized", func() {
				So(tree.persist, ShouldNotBeNil)
			})

			Convey("And when inserting data", func() {
				newTree, ok := tree.Insert([]byte("test-key"), []byte("test-value"))
				So(ok, ShouldBeTrue)
				So(newTree, ShouldNotBeNil)

				Convey("The data should be persisted", func() {
					// Create new tree instance with same persistence
					tree2, err := NewTree(tmpDir)
					So(err, ShouldBeNil)
					defer tree2.Close()

					// Verify term and index were loaded
					term, index := tree2.GetLogState()
					So(term, ShouldEqual, tree.term)
					So(index, ShouldEqual, tree.logIndex)

					// Verify data was recovered
					value, exists := tree2.Get([]byte("test-key"))
					So(exists, ShouldBeTrue)
					So(value, ShouldResemble, []byte("test-value"))
				})
			})
		})
	})
}

func TestTreeStateRecovery(t *testing.T) {
	Convey("Given a tree with existing WAL", t, func() {
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

		// Create and populate first tree
		tree1, err := NewTree(tmpDir)
		So(err, ShouldBeNil)

		entries := []struct {
			key   string
			value string
			term  uint64
		}{
			{"key1", "value1", 1},
			{"key2", "value2", 1},
			{"key3", "value3", 2},
		}

		for _, e := range entries {
			tree1.UpdateTerm(e.term)
			tree1, _ = tree1.Insert([]byte(e.key), []byte(e.value))
		}
		tree1.Close()

		Convey("When creating a new tree instance", func() {
			tree2, err := NewTree(tmpDir)
			So(err, ShouldBeNil)
			defer tree2.Close()

			Convey("Then it should recover the correct state", func() {
				term, index := tree2.GetLogState()
				So(term, ShouldEqual, entries[len(entries)-1].term)
				So(index, ShouldEqual, uint64(len(entries)))

				Convey("And all data should be accessible", func() {
					for _, e := range entries {
						value, exists := tree2.Get([]byte(e.key))
						So(exists, ShouldBeTrue)
						So(value, ShouldResemble, []byte(e.value))
					}
				})
			})
		})
	})
}

func TestTreeTermUpdate(t *testing.T) {
	Convey("Given a persistent tree", t, func() {
		tmpDir := filepath.Join(os.TempDir(), "radix-test-"+time.Now().Format("20060102150405"))
		defer os.RemoveAll(tmpDir)

		tree, err := NewTree(tmpDir)
		So(err, ShouldBeNil)
		defer tree.Close()

		Convey("When updating the term", func() {
			tree.UpdateTerm(5)

			Convey("Then the term should be persisted", func() {
				term, _ := tree.GetLogState()
				So(term, ShouldEqual, uint64(5))

				// Verify term survives restart
				tree.Close()
				newTree, err := NewTree(tmpDir)
				So(err, ShouldBeNil)
				defer newTree.Close()

				term, _ = newTree.GetLogState()
				So(term, ShouldEqual, uint64(5))
			})
		})
	})
}
