package dmt

import (
	"bytes"
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// PropertyTestConfig holds configuration for property-based tests
type PropertyTestConfig struct {
	Iterations   int
	MaxKeySize   int
	MaxValueSize int
	MaxTreeSize  int
	RandomSeed   int64
}

// DefaultPropertyTestConfig returns default configuration for property tests
func DefaultPropertyTestConfig() PropertyTestConfig {
	return PropertyTestConfig{
		Iterations:   100,
		MaxKeySize:   64,
		MaxValueSize: 256,
		MaxTreeSize:  1000,
		RandomSeed:   time.Now().UnixNano(),
	}
}

func TestMerkleTreeProperties(t *testing.T) {
	Convey("Merkle Tree Property-Based Tests", t, func() {
		config := DefaultPropertyTestConfig()
		rng := rand.New(rand.NewSource(config.RandomSeed))

		Convey("Property: Insert then Get should return the same value", func() {
			for i := 0; i < config.Iterations; i++ {
				mt := NewMerkleTree()

				key := generateRandomBytes(rng, config.MaxKeySize)
				value := generateRandomBytes(rng, config.MaxValueSize)

				err := mt.Insert(key, value)
				So(err, ShouldBeNil)

				mt.Rebuild()

				exists := mt.Verify(key, value)
				So(exists, ShouldBeTrue)
			}
		})

		Convey("Property: Merkle root should be deterministic", func() {
			for i := 0; i < config.Iterations; i++ {
				mt1 := NewMerkleTree()
				mt2 := NewMerkleTree()

				// Generate random data
				numEntries := rng.Intn(config.MaxTreeSize) + 1
				entries := make([]struct{ key, value []byte }, numEntries)

				for j := 0; j < numEntries; j++ {
					entries[j].key = generateRandomBytes(rng, config.MaxKeySize)
					entries[j].value = generateRandomBytes(rng, config.MaxValueSize)
				}

				// Insert in same order
				for _, entry := range entries {
					mt1.Insert(entry.key, entry.value)
					mt2.Insert(entry.key, entry.value)
				}

				mt1.Rebuild()
				mt2.Rebuild()

				// Roots should be identical
				So(bytes.Equal(mt1.Root.Hash, mt2.Root.Hash), ShouldBeTrue)
			}
		})

		Convey("Property: Merkle root should be order-independent", func() {
			for i := 0; i < config.Iterations; i++ {
				mt1 := NewMerkleTree()
				mt2 := NewMerkleTree()

				// Generate random data with unique keys
				numEntries := rng.Intn(config.MaxTreeSize) + 1
				entries := make([]struct{ key, value []byte }, numEntries)
				keySet := make(map[string]bool)

				for j := 0; j < numEntries; j++ {
					// Ensure unique keys
					var key []byte
					for {
						key = generateRandomBytes(rng, config.MaxKeySize)
						keyStr := string(key)
						if !keySet[keyStr] {
							keySet[keyStr] = true
							break
						}
					}
					entries[j].key = key
					entries[j].value = generateRandomBytes(rng, config.MaxValueSize)
				}

				// Insert in original order
				for _, entry := range entries {
					mt1.Insert(entry.key, entry.value)
				}

				// Shuffle and insert in different order
				shuffled := make([]struct{ key, value []byte }, len(entries))
				copy(shuffled, entries)
				rng.Shuffle(len(shuffled), func(i, j int) {
					shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
				})

				for _, entry := range shuffled {
					mt2.Insert(entry.key, entry.value)
				}

				mt1.Rebuild()
				mt2.Rebuild()

				// Roots should be identical regardless of insertion order
				So(bytes.Equal(mt1.Root.Hash, mt2.Root.Hash), ShouldBeTrue)
			}
		})

		Convey("Property: Proof verification should always work for existing keys", func() {
			for i := 0; i < config.Iterations; i++ {
				mt := NewMerkleTree()

				// Generate random data
				numEntries := rng.Intn(config.MaxTreeSize) + 1
				entries := make([]struct{ key, value []byte }, numEntries)

				for j := 0; j < numEntries; j++ {
					entries[j].key = generateRandomBytes(rng, config.MaxKeySize)
					entries[j].value = generateRandomBytes(rng, config.MaxValueSize)
					mt.Insert(entries[j].key, entries[j].value)
				}

				mt.Rebuild()

				// Test proof for random entry
				if len(entries) > 0 {
					idx := rng.Intn(len(entries))
					entry := entries[idx]

					proof, err := mt.GetProof(entry.key)
					So(err, ShouldBeNil)

					valid := mt.VerifyProof(entry.key, entry.value, proof)
					So(valid, ShouldBeTrue)
				}
			}
		})

		Convey("Property: Diff should be symmetric for identical trees", func() {
			for i := 0; i < config.Iterations; i++ {
				mt1 := NewMerkleTree()
				mt2 := NewMerkleTree()

				// Generate random data and insert into both trees
				numEntries := rng.Intn(config.MaxTreeSize) + 1
				for j := 0; j < numEntries; j++ {
					key := generateRandomBytes(rng, config.MaxKeySize)
					value := generateRandomBytes(rng, config.MaxValueSize)
					mt1.Insert(key, value)
					mt2.Insert(key, value)
				}

				mt1.Rebuild()
				mt2.Rebuild()

				// Diff should be empty for identical trees
				diff1 := mt1.GetDiff(mt2)
				diff2 := mt2.GetDiff(mt1)

				So(len(diff1), ShouldEqual, 0)
				So(len(diff2), ShouldEqual, 0)
			}
		})

		Convey("Property: Batch operations should be equivalent to individual operations", func() {
			for i := 0; i < config.Iterations; i++ {
				mt1 := NewMerkleTree()
				mt2 := NewMerkleTree()
				batch := NewMerkleTreeBatch(mt2)

				// Generate random operations
				numOps := rng.Intn(100) + 1
				operations := make([]struct{ key, value []byte }, numOps)

				for j := 0; j < numOps; j++ {
					operations[j].key = generateRandomBytes(rng, config.MaxKeySize)
					operations[j].value = generateRandomBytes(rng, config.MaxValueSize)
				}

				// Apply individually to first tree
				for _, op := range operations {
					mt1.Insert(op.key, op.value)
				}
				mt1.Rebuild()

				// Apply as batch to second tree
				for _, op := range operations {
					batch.AddInsert(op.key, op.value)
				}
				batch.Execute(context.Background())

				// Trees should have identical roots
				So(bytes.Equal(mt1.Root.Hash, mt2.Root.Hash), ShouldBeTrue)
			}
		})
	})
}

func TestObservableMerkleTreeProperties(t *testing.T) {
	Convey("Observable Merkle Tree Property-Based Tests", t, func() {
		config := DefaultPropertyTestConfig()
		rng := rand.New(rand.NewSource(config.RandomSeed))

		Convey("Property: Observers should be notified of all changes", func() {
			for i := 0; i < config.Iterations; i++ {
				omt := NewObservableMerkleTree()
				observer := &TestObserver{}
				omt.AddObserver(observer)

				numOps := rng.Intn(10) + 1
				for j := 0; j < numOps; j++ {
					key := generateRandomBytes(rng, config.MaxKeySize)
					value := generateRandomBytes(rng, config.MaxValueSize)
					omt.Insert(key, value)
				}

				omt.Rebuild()

				// Wait for async notifications
				time.Sleep(10 * time.Millisecond)

				// Should have received notifications for all inserts + rebuild
				So(observer.EventCount(), ShouldEqual, numOps+1)
			}
		})
	})
}

// TestObserver implements MerkleTreeObserver for testing
type TestObserver struct {
	events []MerkleTreeEvent
	mu     sync.RWMutex
}

func (to *TestObserver) OnTreeEvent(ctx context.Context, event MerkleTreeEvent) error {
	to.mu.Lock()
	defer to.mu.Unlock()
	to.events = append(to.events, event)
	return nil
}

func (to *TestObserver) EventCount() int {
	to.mu.RLock()
	defer to.mu.RUnlock()
	return len(to.events)
}

func (to *TestObserver) GetEvents() []MerkleTreeEvent {
	to.mu.RLock()
	defer to.mu.RUnlock()
	events := make([]MerkleTreeEvent, len(to.events))
	copy(events, to.events)
	return events
}

// generateRandomBytes generates random bytes of random length up to maxSize
func generateRandomBytes(rng *rand.Rand, maxSize int) []byte {
	size := rng.Intn(maxSize) + 1
	bytes := make([]byte, size)
	rng.Read(bytes)
	return bytes
}

// Fuzzing test for Merkle tree operations
func FuzzMerkleTreeOperations(f *testing.F) {
	// Add seed corpus
	f.Add([]byte("key1"), []byte("value1"))
	f.Add([]byte(""), []byte("value2"))
	f.Add([]byte("key3"), []byte(""))

	f.Fuzz(func(t *testing.T, key, value []byte) {
		mt := NewMerkleTree()

		// Test insert
		err := mt.Insert(key, value)
		if len(key) == 0 {
			// Should fail for empty keys
			if err == nil {
				t.Errorf("Expected error for empty key, got nil")
			}
			return
		}

		if err != nil {
			t.Errorf("Unexpected error for valid key: %v", err)
			return
		}

		// Test rebuild
		mt.Rebuild()

		// Test verify
		exists := mt.Verify(key, value)
		if !exists {
			t.Errorf("Key should exist after insert and rebuild")
		}

		// Test proof generation and verification
		proof, err := mt.GetProof(key)
		if err != nil {
			t.Errorf("Failed to generate proof: %v", err)
			return
		}

		valid := mt.VerifyProof(key, value, proof)
		if !valid {
			t.Errorf("Proof verification failed for valid key-value pair")
		}
	})
}
