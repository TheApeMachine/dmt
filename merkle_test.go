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

func TestMerkleTreeAsymmetricDiff(t *testing.T) {
	Convey("Given trees with asymmetric structures", t, func() {
		mt1 := NewMerkleTree()
		mt2 := NewMerkleTree()

		// Create asymmetric trees - mt1 has more entries on one side
		mt1.Insert([]byte("a"), []byte("value_a"))
		mt1.Insert([]byte("b"), []byte("value_b"))
		mt1.Insert([]byte("c"), []byte("value_c"))
		mt1.Insert([]byte("d"), []byte("value_d"))
		mt1.Rebuild()

		// mt2 has only some entries
		mt2.Insert([]byte("a"), []byte("value_a"))
		mt2.Insert([]byte("c"), []byte("value_c"))
		mt2.Rebuild()

		Convey("When getting differences", func() {
			diffs := mt1.GetDiff(mt2)

			Convey("Then it should capture all missing entries", func() {
				So(len(diffs), ShouldEqual, 2) // b and d are missing from mt2

				foundB := false
				foundD := false
				for _, diff := range diffs {
					if bytes.Equal(diff.Key, []byte("b")) {
						foundB = true
						So(bytes.Equal(diff.Value, []byte("value_b")), ShouldBeTrue)
						So(diff.Modified, ShouldBeFalse) // New entry
					}
					if bytes.Equal(diff.Key, []byte("d")) {
						foundD = true
						So(bytes.Equal(diff.Value, []byte("value_d")), ShouldBeTrue)
						So(diff.Modified, ShouldBeFalse) // New entry
					}
				}
				So(foundB, ShouldBeTrue)
				So(foundD, ShouldBeTrue)
			})
		})

		Convey("When comparing empty tree with populated tree", func() {
			emptyTree := NewMerkleTree()
			emptyTree.Rebuild()

			diffs := mt1.GetDiff(emptyTree)

			Convey("Then it should return all entries from populated tree", func() {
				So(len(diffs), ShouldEqual, 4)

				keys := make(map[string]bool)
				for _, diff := range diffs {
					keys[string(diff.Key)] = true
					So(diff.Modified, ShouldBeFalse) // All are new entries
				}
				So(keys["a"], ShouldBeTrue)
				So(keys["b"], ShouldBeTrue)
				So(keys["c"], ShouldBeTrue)
				So(keys["d"], ShouldBeTrue)
			})
		})
	})
}

func TestMerkleTreeDirectionalProofs(t *testing.T) {
	Convey("Given a Merkle tree with directional proof support", t, func() {
		mt := NewMerkleTree()
		mt.Insert([]byte("key1"), []byte("value1"))
		mt.Insert([]byte("key2"), []byte("value2"))
		mt.Insert([]byte("key3"), []byte("value3"))
		mt.Insert([]byte("key4"), []byte("value4"))
		mt.Rebuild()

		Convey("When generating directional proofs", func() {
			proof, err := mt.GetProof([]byte("key2"))

			Convey("Then it should succeed with directional information", func() {
				So(err, ShouldBeNil)
				So(proof, ShouldNotBeNil)
				So(len(proof), ShouldBeGreaterThan, 0)

				// Verify each proof element has directional information
				for _, element := range proof {
					So(element.Hash, ShouldNotBeNil)
					So(len(element.Hash), ShouldEqual, 32) // SHA-256 hash length
					// IsLeft can be true or false, both are valid
				}
			})

			Convey("And the directional proof should be verifiable", func() {
				valid := mt.VerifyProof([]byte("key2"), []byte("value2"), proof)
				So(valid, ShouldBeTrue)
			})

			Convey("And the directional proof should fail for wrong values", func() {
				valid := mt.VerifyProof([]byte("key2"), []byte("wrong"), proof)
				So(valid, ShouldBeFalse)
			})
		})

		Convey("When using legacy proof methods", func() {
			legacyProof, err := mt.GetProofLegacy([]byte("key2"))

			Convey("Then legacy proofs should still work", func() {
				So(err, ShouldBeNil)
				So(legacyProof, ShouldNotBeNil)

				valid := mt.VerifyProofLegacy([]byte("key2"), []byte("value2"), legacyProof)
				So(valid, ShouldBeTrue)
			})
		})
	})
}

func TestMerkleTreeMemoryManagement(t *testing.T) {
	Convey("Given a Merkle tree with memory management", t, func() {
		mt := NewMerkleTreeWithLimit(5) // Small limit for testing

		Convey("When inserting many entries", func() {
			// Insert enough entries to trigger LRU eviction
			for i := 0; i < 10; i++ {
				key := []byte{byte(i)}
				value := []byte{byte(i + 100)}
				mt.Insert(key, value)
			}
			mt.Rebuild()

			Convey("Then nodeMap should be bounded", func() {
				leafCount, nodeCount, lruSize := mt.GetMemoryUsage()
				So(leafCount, ShouldEqual, 10)
				So(nodeCount, ShouldBeLessThanOrEqualTo, 5) // Should be limited
				So(lruSize, ShouldBeLessThanOrEqualTo, 5)
			})
		})

		Convey("When rebuilding multiple times", func() {
			mt.Insert([]byte("key1"), []byte("value1"))
			mt.Insert([]byte("key2"), []byte("value2"))

			// First rebuild
			mt.Rebuild()

			// Add more data and rebuild again
			mt.Insert([]byte("key3"), []byte("value3"))
			mt.Rebuild()
			_, nodeCount2, _ := mt.GetMemoryUsage()

			Convey("Then nodeMap should be cleared on rebuild", func() {
				// After rebuild, old internal nodes should be cleared
				So(nodeCount2, ShouldBeGreaterThan, 0)
				// The exact count depends on tree structure, but it should be reasonable
				So(nodeCount2, ShouldBeLessThanOrEqualTo, 5)
			})
		})

		Convey("When using unlimited nodeMap", func() {
			unlimitedTree := NewMerkleTreeWithLimit(0) // Unlimited

			for i := 0; i < 20; i++ {
				key := []byte{byte(i)}
				value := []byte{byte(i + 100)}
				unlimitedTree.Insert(key, value)
			}
			unlimitedTree.Rebuild()

			Convey("Then nodeMap can grow without bounds", func() {
				_, nodeCount, _ := unlimitedTree.GetMemoryUsage()
				So(nodeCount, ShouldBeGreaterThan, 5) // Should exceed the limited version
			})
		})
	})
}

func TestMerkleTreeSynchronizationScenarios(t *testing.T) {
	Convey("Given various synchronization scenarios", t, func() {

		Convey("When trees have completely different data", func() {
			mt1 := NewMerkleTree()
			mt2 := NewMerkleTree()

			// Completely different data sets
			mt1.Insert([]byte("set1_key1"), []byte("value1"))
			mt1.Insert([]byte("set1_key2"), []byte("value2"))
			mt1.Rebuild()

			mt2.Insert([]byte("set2_key1"), []byte("value1"))
			mt2.Insert([]byte("set2_key2"), []byte("value2"))
			mt2.Rebuild()

			diffs := mt1.GetDiff(mt2)

			Convey("Then all entries should be in diff", func() {
				So(len(diffs), ShouldEqual, 2)
				for _, diff := range diffs {
					So(diff.Modified, ShouldBeFalse) // All are new to the other tree
				}
			})
		})

		Convey("When trees have partial overlap", func() {
			mt1 := NewMerkleTree()
			mt2 := NewMerkleTree()

			// Shared data
			mt1.Insert([]byte("shared1"), []byte("value1"))
			mt1.Insert([]byte("shared2"), []byte("value2"))
			mt2.Insert([]byte("shared1"), []byte("value1"))
			mt2.Insert([]byte("shared2"), []byte("value2"))

			// Unique to mt1
			mt1.Insert([]byte("unique1"), []byte("unique_value1"))

			// Modified in mt2
			mt2.Insert([]byte("shared2"), []byte("modified_value2"))

			// Unique to mt2 (won't show in mt1.GetDiff(mt2))
			mt2.Insert([]byte("unique2"), []byte("unique_value2"))

			mt1.Rebuild()
			mt2.Rebuild()

			diffs := mt1.GetDiff(mt2)

			Convey("Then only differences should be reported", func() {
				So(len(diffs), ShouldEqual, 2) // unique1 and modified shared2

				foundUnique := false
				foundModified := false
				for _, diff := range diffs {
					if bytes.Equal(diff.Key, []byte("unique1")) {
						foundUnique = true
						So(diff.Modified, ShouldBeFalse)
					}
					if bytes.Equal(diff.Key, []byte("shared2")) {
						foundModified = true
						So(diff.Modified, ShouldBeTrue)
					}
				}
				So(foundUnique, ShouldBeTrue)
				So(foundModified, ShouldBeTrue)
			})
		})

		Convey("When one tree is a subset of another", func() {
			mt1 := NewMerkleTree()
			mt2 := NewMerkleTree()

			// mt2 is a subset of mt1
			mt1.Insert([]byte("key1"), []byte("value1"))
			mt1.Insert([]byte("key2"), []byte("value2"))
			mt1.Insert([]byte("key3"), []byte("value3"))
			mt1.Insert([]byte("key4"), []byte("value4"))

			mt2.Insert([]byte("key1"), []byte("value1"))
			mt2.Insert([]byte("key3"), []byte("value3"))

			mt1.Rebuild()
			mt2.Rebuild()

			diffs := mt1.GetDiff(mt2)

			Convey("Then missing entries should be identified", func() {
				So(len(diffs), ShouldEqual, 2) // key2 and key4 missing from mt2

				keys := make(map[string]bool)
				for _, diff := range diffs {
					keys[string(diff.Key)] = true
					So(diff.Modified, ShouldBeFalse) // All are new entries
				}
				So(keys["key2"], ShouldBeTrue)
				So(keys["key4"], ShouldBeTrue)
			})
		})
	})
}
