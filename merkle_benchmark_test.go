package dmt

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkConfig holds configuration for Merkle tree benchmarks
type BenchmarkConfig struct {
	TreeSize    int
	DiffRatio   float64 // Percentage of different entries between trees
	KeyPrefix   string
	ValuePrefix string
}

// DefaultBenchmarkConfig returns sensible defaults for benchmarking
func DefaultBenchmarkConfig() BenchmarkConfig {
	return BenchmarkConfig{
		TreeSize:    500,
		DiffRatio:   0.5, // 50% different
		KeyPrefix:   "key",
		ValuePrefix: "value",
	}
}

// setupMerkleTreePair creates two Merkle trees with controlled differences
func setupMerkleTreePair(config BenchmarkConfig) (*MerkleTree, *MerkleTree) {
	mt1 := NewMerkleTree()
	mt2 := NewMerkleTree()

	// Pre-allocate slices for better performance
	keys := make([][]byte, config.TreeSize)
	values := make([][]byte, config.TreeSize)

	for i := 0; i < config.TreeSize; i++ {
		keys[i] = make([]byte, 0, 32)
		values[i] = make([]byte, 0, 32)
		keys[i] = fmt.Appendf(keys[i], "%s%d", config.KeyPrefix, i)
		values[i] = fmt.Appendf(values[i], "%s%d", config.ValuePrefix, i)

		mt1.Insert(keys[i], values[i])

		// Insert into second tree based on diff ratio
		if float64(i%100)/100.0 >= config.DiffRatio {
			mt2.Insert(keys[i], values[i])
		}
	}

	mt1.Rebuild()
	mt2.Rebuild()

	return mt1, mt2
}

// BenchmarkMerkleTreeDiff benchmarks diff calculation performance
func BenchmarkMerkleTreeDiff(b *testing.B) {
	config := DefaultBenchmarkConfig()
	mt1, mt2 := setupMerkleTreePair(config)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		diffs := mt1.GetDiff(mt2)
		_ = diffs // Prevent compiler optimization
	}
}

// BenchmarkMerkleTreeDiffSizes benchmarks diff calculation at different scales
func BenchmarkMerkleTreeDiffSizes(b *testing.B) {
	sizes := []int{100, 500, 1000, 5000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size-%d", size), func(b *testing.B) {
			config := DefaultBenchmarkConfig()
			config.TreeSize = size
			mt1, mt2 := setupMerkleTreePair(config)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				diffs := mt1.GetDiff(mt2)
				_ = diffs
			}
		})
	}
}

// BenchmarkMerkleTreeDiffRatios benchmarks diff calculation with different difference ratios
func BenchmarkMerkleTreeDiffRatios(b *testing.B) {
	ratios := []float64{0.1, 0.3, 0.5, 0.7, 0.9}

	for _, ratio := range ratios {
		b.Run(fmt.Sprintf("DiffRatio-%.1f", ratio), func(b *testing.B) {
			config := DefaultBenchmarkConfig()
			config.DiffRatio = ratio
			mt1, mt2 := setupMerkleTreePair(config)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				diffs := mt1.GetDiff(mt2)
				_ = diffs
			}
		})
	}
}

// BenchmarkMerkleTreeConcurrentAccess benchmarks concurrent operations
func BenchmarkMerkleTreeConcurrentAccess(b *testing.B) {
	mt := NewMerkleTree()

	// Pre-allocate keys and values to avoid allocations during benchmark
	keys := make([][]byte, 100)
	values := make([][]byte, 100)

	// Pre-populate
	for i := 0; i < 100; i++ {
		keys[i] = make([]byte, 0, 16)
		values[i] = make([]byte, 0, 16)
		keys[i] = fmt.Appendf(keys[i], "key%d", i)
		values[i] = fmt.Appendf(values[i], "value%d", i)
		mt.Insert(keys[i], values[i])
	}
	mt.Rebuild()

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			// Use pre-allocated slices to avoid allocations
			key := keys[i%100]
			value := values[i%100]
			mt.Verify(key, value)
			i++
		}
	})
}

// BenchmarkMerkleTreeOperations benchmarks different Merkle tree operations
func BenchmarkMerkleTreeOperations(b *testing.B) {
	operations := []struct {
		name string
		fn   func(*testing.B, *MerkleTree, [][]byte, [][]byte)
	}{
		{"Insert", benchmarkMerkleInsert},
		{"Verify", benchmarkMerkleVerify},
		{"GetProof", benchmarkMerkleGetProof},
		{"VerifyProof", benchmarkMerkleVerifyProof},
		{"Rebuild", benchmarkMerkleRebuild},
	}

	for _, op := range operations {
		b.Run(op.name, func(b *testing.B) {
			mt := NewMerkleTree()
			keys, values := generateTestData(1000)

			// Pre-populate for operations that need existing data
			if op.name != "Insert" {
				for i := 0; i < len(keys); i++ {
					mt.Insert(keys[i], values[i])
				}
				mt.Rebuild()
			}

			b.ResetTimer()
			b.ReportAllocs()

			op.fn(b, mt, keys, values)
		})
	}
}

// BenchmarkMerkleTreeMemoryUsage benchmarks memory usage patterns
func BenchmarkMerkleTreeMemoryUsage(b *testing.B) {
	sizes := []int{100, 1000, 10000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size-%d", size), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				mt := NewMerkleTreeWithLimit(size / 10) // Test with memory limits
				keys, values := generateTestData(size)

				for j := 0; j < len(keys); j++ {
					mt.Insert(keys[j], values[j])
				}
				mt.Rebuild()

				// Force memory usage calculation
				leafCount, nodeCount, cacheSize := mt.GetMemoryUsage()
				_ = leafCount + nodeCount + cacheSize
			}
		})
	}
}

// Helper functions for benchmarking different operations
func benchmarkMerkleInsert(b *testing.B, mt *MerkleTree, keys, values [][]byte) {
	for i := 0; i < b.N; i++ {
		idx := i % len(keys)
		mt.Insert(keys[idx], values[idx])
	}
}

func benchmarkMerkleVerify(b *testing.B, mt *MerkleTree, keys, values [][]byte) {
	for i := 0; i < b.N; i++ {
		idx := i % len(keys)
		mt.Verify(keys[idx], values[idx])
	}
}

func benchmarkMerkleGetProof(b *testing.B, mt *MerkleTree, keys, values [][]byte) {
	for i := 0; i < b.N; i++ {
		idx := i % len(keys)
		proof, _ := mt.GetProof(keys[idx])
		_ = proof
	}
}

func benchmarkMerkleVerifyProof(b *testing.B, mt *MerkleTree, keys, values [][]byte) {
	// Pre-generate proofs
	proofs := make([][]ProofElement, len(keys))
	for i := range keys {
		proofs[i], _ = mt.GetProof(keys[i])
	}

	for i := 0; i < b.N; i++ {
		idx := i % len(keys)
		if len(proofs[idx]) > 0 {
			mt.VerifyProof(keys[idx], values[idx], proofs[idx])
		}
	}
}

func benchmarkMerkleRebuild(b *testing.B, mt *MerkleTree, keys, values [][]byte) {
	for i := 0; i < b.N; i++ {
		mt.Rebuild()
	}
}

// generateTestData creates test keys and values for benchmarking
func generateTestData(count int) ([][]byte, [][]byte) {
	keys := make([][]byte, count)
	values := make([][]byte, count)

	for i := 0; i < count; i++ {
		keys[i] = make([]byte, 0, 32)
		values[i] = make([]byte, 0, 32)
		keys[i] = fmt.Appendf(keys[i], "benchmark-key-%d", i)
		values[i] = fmt.Appendf(values[i], "benchmark-value-%d", i)
	}

	return keys, values
}

// BenchmarkMerkleTreeBatch benchmarks batch operations
func BenchmarkMerkleTreeBatch(b *testing.B) {
	mt := NewMerkleTree()
	batch := NewMerkleTreeBatch(mt)

	// Pre-generate test data
	keys, values := generateTestData(1000)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		batch.Clear()

		// Add operations to batch
		for j := 0; j < 100; j++ {
			idx := (i*100 + j) % len(keys)
			batch.AddInsert(keys[idx], values[idx])
		}

		// Execute batch
		results, err := batch.Execute(context.Background())
		if err != nil {
			b.Fatalf("Batch execution failed: %v", err)
		}
		_ = results
	}
}

// BenchmarkMerkleTreeComparison compares individual vs batch operations
func BenchmarkMerkleTreeComparison(b *testing.B) {
	keys, values := generateTestData(100)

	b.Run("Individual", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			mt := NewMerkleTree()
			for j := range keys {
				mt.Insert(keys[j], values[j])
			}
			mt.Rebuild()
		}
	})

	b.Run("Batch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			mt := NewMerkleTree()
			batch := NewMerkleTreeBatch(mt)

			for j := range keys {
				batch.AddInsert(keys[j], values[j])
			}

			batch.Execute(context.Background())
		}
	})
}

// BenchmarkMerkleTreeConcurrentBatch benchmarks concurrent batch operations
func BenchmarkMerkleTreeConcurrentBatch(b *testing.B) {
	mt := NewMerkleTree()
	keys, values := generateTestData(1000)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		batch := NewMerkleTreeBatch(mt)
		i := 0

		for pb.Next() {
			batch.Clear()

			// Add 10 operations per batch
			for j := 0; j < 10; j++ {
				idx := (i*10 + j) % len(keys)
				batch.AddInsert(keys[idx], values[idx])
			}

			batch.Execute(context.Background())
			i++
		}
	})
}

// BenchmarkMerkleTreeStressTest performs stress testing
func BenchmarkMerkleTreeStressTest(b *testing.B) {
	sizes := []int{1000, 10000, 100000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("StressTest-%d", size), func(b *testing.B) {
			mt := NewMerkleTree()
			keys, values := generateTestData(size)

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				// Insert all data
				for j := range keys {
					mt.Insert(keys[j], values[j])
				}

				// Rebuild
				mt.Rebuild()

				// Verify some entries
				for j := 0; j < 100 && j < len(keys); j++ {
					mt.Verify(keys[j], values[j])
				}

				// Get memory usage
				leafCount, nodeCount, cacheSize := mt.GetMemoryUsage()
				_ = leafCount + nodeCount + cacheSize
			}
		})
	}
}
