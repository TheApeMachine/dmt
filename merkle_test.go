package dmt

import (
	"bytes"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestNewMerkleTree(t *testing.T) {
	Convey("Given a new Merkle tree", t, func() {
		mt := NewMerkleTree()

		Convey("Then it should be properly initialized", func() {
			So(mt, ShouldNotBeNil)
			So(mt.leafMap, ShouldNotBeNil)
			So(mt.nodeMap, ShouldNotBeNil)
			So(mt.Root, ShouldBeNil)
			So(mt.modified, ShouldBeFalse)
		})
	})
}

func TestMerkleTreeInsertAndRebuild(t *testing.T) {
	Convey("Given a Merkle tree", t, func() {
		mt := NewMerkleTree()

		Convey("When inserting key-value pairs", func() {
			mt.Insert([]byte("key1"), []byte("value1"))
			mt.Insert([]byte("key2"), []byte("value2"))
			mt.Insert([]byte("key3"), []byte("value3"))

			Convey("Then the tree should be marked as modified", func() {
				So(mt.modified, ShouldBeTrue)
			})

			Convey("When rebuilding the tree", func() {
				mt.Rebuild()

				Convey("Then the tree should have a valid root", func() {
					So(mt.Root, ShouldNotBeNil)
					So(mt.Root.Hash, ShouldNotBeNil)
					So(len(mt.Root.Hash), ShouldEqual, 32) // SHA-256 hash length
				})

				Convey("And the modified flag should be reset", func() {
					So(mt.modified, ShouldBeFalse)
				})
			})
		})
	})
}

func TestMerkleTreeDiff(t *testing.T) {
	Convey("Given two Merkle trees", t, func() {
		mt1 := NewMerkleTree()
		mt2 := NewMerkleTree()

		// Populate first tree
		mt1.Insert([]byte("key1"), []byte("value1"))
		mt1.Insert([]byte("key2"), []byte("value2"))
		mt1.Insert([]byte("key3"), []byte("value3"))
		mt1.Rebuild()

		// Populate second tree with some differences
		mt2.Insert([]byte("key1"), []byte("value1"))
		mt2.Insert([]byte("key2"), []byte("different"))
		mt2.Insert([]byte("key4"), []byte("value4"))
		mt2.Rebuild()

		Convey("When getting differences", func() {
			diffs := mt1.GetDiff(mt2)

			Convey("Then it should identify all differences", func() {
				So(len(diffs), ShouldEqual, 2) // key2 modified and key3 new

				// Verify the differences
				foundModified := false
				foundNew := false
				for _, diff := range diffs {
					if bytes.Equal(diff.Key, []byte("key2")) {
						foundModified = true
						So(diff.Modified, ShouldBeTrue)
					}
					if bytes.Equal(diff.Key, []byte("key3")) {
						foundNew = true
						So(diff.Modified, ShouldBeFalse)
					}
				}
				So(foundModified, ShouldBeTrue)
				So(foundNew, ShouldBeTrue)
			})
		})
	})
}

func TestMerkleTreeVerify(t *testing.T) {
	Convey("Given a Merkle tree with data", t, func() {
		mt := NewMerkleTree()
		mt.Insert([]byte("key1"), []byte("value1"))
		mt.Rebuild()

		Convey("When verifying existing data", func() {
			exists := mt.Verify([]byte("key1"), []byte("value1"))
			So(exists, ShouldBeTrue)
		})

		Convey("When verifying non-existent data", func() {
			exists := mt.Verify([]byte("nonexistent"), []byte("value"))
			So(exists, ShouldBeFalse)
		})

		Convey("When verifying with wrong value", func() {
			exists := mt.Verify([]byte("key1"), []byte("wrong"))
			So(exists, ShouldBeFalse)
		})
	})
}

func TestMerkleProof(t *testing.T) {
	Convey("Given a Merkle tree with multiple entries", t, func() {
		mt := NewMerkleTree()
		mt.Insert([]byte("key1"), []byte("value1"))
		mt.Insert([]byte("key2"), []byte("value2"))
		mt.Insert([]byte("key3"), []byte("value3"))
		mt.Insert([]byte("key4"), []byte("value4"))
		mt.Rebuild()

		Convey("When generating a proof", func() {
			proof, err := mt.GetProof([]byte("key2"))

			Convey("Then it should succeed", func() {
				So(err, ShouldBeNil)
				So(proof, ShouldNotBeNil)
				So(len(proof), ShouldBeGreaterThan, 0)
			})

			Convey("And the proof should be verifiable", func() {
				valid := mt.VerifyProof([]byte("key2"), []byte("value2"), proof)
				So(valid, ShouldBeTrue)
			})

			Convey("And the proof should fail for wrong values", func() {
				valid := mt.VerifyProof([]byte("key2"), []byte("wrong"), proof)
				So(valid, ShouldBeFalse)
			})
		})

		Convey("When generating a proof for non-existent key", func() {
			proof, err := mt.GetProof([]byte("nonexistent"))

			Convey("Then it should fail", func() {
				So(err, ShouldNotBeNil)
				So(proof, ShouldBeNil)
			})
		})
	})
}

func TestMerkleTreeDeterministic(t *testing.T) {
	Convey("Given two identical sets of data", t, func() {
		mt1 := NewMerkleTree()
		mt2 := NewMerkleTree()

		// Insert same data in different order
		mt1.Insert([]byte("key1"), []byte("value1"))
		mt1.Insert([]byte("key2"), []byte("value2"))
		mt1.Insert([]byte("key3"), []byte("value3"))

		mt2.Insert([]byte("key3"), []byte("value3"))
		mt2.Insert([]byte("key1"), []byte("value1"))
		mt2.Insert([]byte("key2"), []byte("value2"))

		Convey("When rebuilding both trees", func() {
			mt1.Rebuild()
			mt2.Rebuild()

			Convey("Then they should have identical root hashes", func() {
				So(bytes.Equal(mt1.Root.Hash, mt2.Root.Hash), ShouldBeTrue)
			})
		})
	})
}
