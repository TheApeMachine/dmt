package dmt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// BenchmarkForestInsert benchmarks insert operations on Forest
func BenchmarkForestInsert(b *testing.B) {
	forest, cleanup := setupBenchmarkForest(b)
	defer cleanup()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("benchmark-key-%d", i))
		value := []byte(fmt.Sprintf("benchmark-value-%d", i))

		if err := forest.Insert(key, value); err != nil {
			b.Fatalf("Insert failed: %v", err)
		}
	}
}

// BenchmarkForestGet benchmarks get operations on Forest
func BenchmarkForestGet(b *testing.B) {
	forest, cleanup := setupBenchmarkForest(b)
	defer cleanup()

	// Pre-populate with data
	numItems := 10000
	for i := 0; i < numItems; i++ {
		key := []byte(fmt.Sprintf("benchmark-key-%d", i))
		value := []byte(fmt.Sprintf("benchmark-value-%d", i))
		forest.Insert(key, value)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("benchmark-key-%d", i%numItems))
		_, exists := forest.Get(key)
		if !exists {
			b.Fatalf("Key not found: %s", key)
		}
	}
}

// BenchmarkForestSeek benchmarks seek operations on Forest
func BenchmarkForestSeek(b *testing.B) {
	forest, cleanup := setupBenchmarkForest(b)
	defer cleanup()

	// Pre-populate with data
	numItems := 10000
	for i := 0; i < numItems; i++ {
		key := []byte(fmt.Sprintf("benchmark-key-%05d", i))
		value := []byte(fmt.Sprintf("benchmark-value-%d", i))
		forest.Insert(key, value)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		prefix := []byte(fmt.Sprintf("benchmark-key-%05d", i%numItems))
		_, exists := forest.Seek(prefix)
		if !exists {
			b.Fatalf("Seek failed for prefix: %s", prefix)
		}
	}
}

// BenchmarkTreeInsert benchmarks insert operations directly on Tree
func BenchmarkTreeInsert(b *testing.B) {
	tree, cleanup := setupBenchmarkTree(b)
	defer cleanup()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("tree-key-%d", i))
		value := []byte(fmt.Sprintf("tree-value-%d", i))

		tree.Insert(key, value)
	}
}

// BenchmarkTreeGet benchmarks get operations directly on Tree
func BenchmarkTreeGet(b *testing.B) {
	tree, cleanup := setupBenchmarkTree(b)
	defer cleanup()

	// Pre-populate with data
	numItems := 10000
	for i := 0; i < numItems; i++ {
		key := []byte(fmt.Sprintf("tree-key-%d", i))
		value := []byte(fmt.Sprintf("tree-value-%d", i))
		tree.Insert(key, value)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("tree-key-%d", i%numItems))
		_, exists := tree.Get(key)
		if !exists {
			b.Fatalf("Key not found: %s", key)
		}
	}
}

// BenchmarkMerkleTreeInsert benchmarks Merkle tree insert operations
func BenchmarkMerkleTreeInsert(b *testing.B) {
	merkle := NewMerkleTree()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("merkle-key-%d", i))
		value := []byte(fmt.Sprintf("merkle-value-%d", i))

		merkle.Insert(key, value)
	}
}

// BenchmarkMerkleTreeRebuild benchmarks Merkle tree rebuild operations
func BenchmarkMerkleTreeRebuild(b *testing.B) {
	merkle := NewMerkleTree()

	// Pre-populate with data
	numItems := 1000
	for i := 0; i < numItems; i++ {
		key := []byte(fmt.Sprintf("merkle-key-%d", i))
		value := []byte(fmt.Sprintf("merkle-value-%d", i))
		merkle.Insert(key, value)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		merkle.Rebuild()
	}
}

// BenchmarkMerkleTreeGetDiff benchmarks Merkle tree diff operations
func BenchmarkMerkleTreeGetDiff(b *testing.B) {
	merkle1 := NewMerkleTree()
	merkle2 := NewMerkleTree()

	// Pre-populate first tree
	numItems := 1000
	for i := 0; i < numItems; i++ {
		key := []byte(fmt.Sprintf("merkle-key-%d", i))
		value := []byte(fmt.Sprintf("merkle-value-%d", i))
		merkle1.Insert(key, value)
	}
	merkle1.Rebuild()

	// Pre-populate second tree with some overlap
	for i := numItems / 2; i < numItems+numItems/2; i++ {
		key := []byte(fmt.Sprintf("merkle-key-%d", i))
		value := []byte(fmt.Sprintf("merkle-value-%d", i))
		merkle2.Insert(key, value)
	}
	merkle2.Rebuild()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		diffs := merkle1.GetDiff(merkle2)
		_ = diffs // Use the result to prevent optimization
	}
}

// BenchmarkPersistenceInsert benchmarks persistence operations
func BenchmarkPersistenceInsert(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), fmt.Sprintf("dmt-benchmark-%d", time.Now().UnixNano()))
	defer os.RemoveAll(tempDir)

	persist, err := NewPersistentStore(tempDir)
	if err != nil {
		b.Fatalf("Failed to create persistent store: %v", err)
	}
	defer persist.Close()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("persist-key-%d", i))
		value := []byte(fmt.Sprintf("persist-value-%d", i))

		if err := persist.LogInsert(key, value, 1, uint64(i+1)); err != nil {
			b.Fatalf("LogInsert failed: %v", err)
		}
	}
}

// BenchmarkConcurrentForestOperations benchmarks concurrent operations on Forest
func BenchmarkConcurrentForestOperations(b *testing.B) {
	forest, cleanup := setupBenchmarkForest(b)
	defer cleanup()

	// Pre-populate with some data
	numItems := 1000
	for i := 0; i < numItems; i++ {
		key := []byte(fmt.Sprintf("concurrent-key-%d", i))
		value := []byte(fmt.Sprintf("concurrent-value-%d", i))
		forest.Insert(key, value)
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 3 {
			case 0:
				// Insert operation
				key := []byte(fmt.Sprintf("concurrent-key-%d", i))
				value := []byte(fmt.Sprintf("concurrent-value-%d", i))
				forest.Insert(key, value)
			case 1:
				// Get operation
				key := []byte(fmt.Sprintf("concurrent-key-%d", i%numItems))
				forest.Get(key)
			case 2:
				// Seek operation
				key := []byte(fmt.Sprintf("concurrent-key-%d", i%numItems))
				forest.Seek(key)
			}
			i++
		}
	})
}

// BenchmarkMemoryUsage benchmarks memory usage patterns
func BenchmarkMemoryUsage(b *testing.B) {
	forest, cleanup := setupBenchmarkForest(b)
	defer cleanup()

	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("memory-key-%d", i))
		value := []byte(fmt.Sprintf("memory-value-%d", i))
		forest.Insert(key, value)

		// Periodic memory measurement
		if i%1000 == 0 {
			runtime.GC()
			runtime.ReadMemStats(&m2)
			b.ReportMetric(float64(m2.Alloc-m1.Alloc)/1024/1024, "MB")
		}
	}
}

// BenchmarkWithProfiling runs benchmarks with profiling enabled
func BenchmarkWithProfiling(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), fmt.Sprintf("dmt-profiling-benchmark-%d", time.Now().UnixNano()))
	defer os.RemoveAll(tempDir)

	config := ProfileConfig{
		CPUProfile:   true,
		MemProfile:   true,
		BlockProfile: true,
		ProfileDir:   tempDir,
		Duration:     time.Duration(b.N) * time.Microsecond,
	}

	profiler, err := NewPerformanceProfiler(config)
	if err != nil {
		b.Fatal(err)
	}
	profiler.StartProfiling(context.Background())
	defer func() {
		result, err := profiler.StopProfiling()
		if err == nil && !b.Failed() {
			b.Logf("Profiling completed. CPU profile: %s", result.CPUProfilePath)
			b.Logf("Memory profile: %s", result.MemProfilePath)
			if len(result.Bottlenecks) > 0 {
				b.Logf("Detected %d bottlenecks", len(result.Bottlenecks))
			}
		}
	}()

	forest, cleanup := setupBenchmarkForest(b)
	defer cleanup()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("profiled-key-%d", i))
		value := []byte(fmt.Sprintf("profiled-value-%d", i))

		profiler.ProfileOperation(context.Background(), "forest_insert", func() error {
			return forest.Insert(key, value)
		})
	}
}

// BenchmarkScalability tests performance at different scales
func BenchmarkScalability(b *testing.B) {
	scales := []int{100, 1000, 10000, 100000}

	for _, scale := range scales {
		b.Run(fmt.Sprintf("Scale-%d", scale), func(b *testing.B) {
			forest, cleanup := setupBenchmarkForest(b)
			defer cleanup()

			// Pre-populate with data at scale
			for i := 0; i < scale; i++ {
				key := []byte(fmt.Sprintf("scale-key-%d", i))
				value := []byte(fmt.Sprintf("scale-value-%d", i))
				forest.Insert(key, value)
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				key := []byte(fmt.Sprintf("scale-key-%d", i%scale))
				_, exists := forest.Get(key)
				if !exists {
					b.Fatalf("Key not found at scale %d", scale)
				}
			}
		})
	}
}

// BenchmarkNetworkOperations benchmarks network-related operations
func BenchmarkNetworkOperations(b *testing.B) {
	forest1, cleanup1 := setupBenchmarkForest(b)
	defer cleanup1()
	forest2, cleanup2 := setupBenchmarkForest(b)
	defer cleanup2()

	// Pre-populate forests with different data
	for i := 0; i < 1000; i++ {
		key1 := []byte(fmt.Sprintf("net-key1-%d", i))
		value1 := []byte(fmt.Sprintf("net-value1-%d", i))
		forest1.Insert(key1, value1)

		if i%2 == 0 {
			key2 := []byte(fmt.Sprintf("net-key2-%d", i))
			value2 := []byte(fmt.Sprintf("net-value2-%d", i))
			forest2.Insert(key2, value2)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Simulate network sync operation by creating Merkle trees
		merkle1 := NewMerkleTree()
		merkle2 := NewMerkleTree()

		// Add some data to the trees
		key1 := []byte(fmt.Sprintf("sync-key1-%d", i))
		value1 := []byte(fmt.Sprintf("sync-value1-%d", i))
		merkle1.Insert(key1, value1)

		key2 := []byte(fmt.Sprintf("sync-key2-%d", i))
		value2 := []byte(fmt.Sprintf("sync-value2-%d", i))
		merkle2.Insert(key2, value2)

		merkle1.Rebuild()
		merkle2.Rebuild()

		diffs := merkle1.GetDiff(merkle2)
		_ = diffs // Use the result
	}
}

// BenchmarkConcurrentLoad tests performance under concurrent load
func BenchmarkConcurrentLoad(b *testing.B) {
	forest, cleanup := setupBenchmarkForest(b)
	defer cleanup()

	// Pre-populate
	for i := 0; i < 10000; i++ {
		key := []byte(fmt.Sprintf("concurrent-key-%d", i))
		value := []byte(fmt.Sprintf("concurrent-value-%d", i))
		forest.Insert(key, value)
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			switch i % 4 {
			case 0:
				// Insert
				key := []byte(fmt.Sprintf("load-key-%d", i))
				value := []byte(fmt.Sprintf("load-value-%d", i))
				forest.Insert(key, value)
			case 1:
				// Get
				key := []byte(fmt.Sprintf("concurrent-key-%d", i%10000))
				forest.Get(key)
			case 2:
				// Seek
				prefix := []byte(fmt.Sprintf("concurrent-key-%d", i%1000))
				forest.Seek(prefix)
			case 3:
				// Merkle operations
				merkle := NewMerkleTree()
				key := []byte(fmt.Sprintf("merkle-key-%d", i))
				value := []byte(fmt.Sprintf("merkle-value-%d", i))
				merkle.Insert(key, value)
				merkle.Rebuild()
			}
			i++
		}
	})
}

// BenchmarkMemoryEfficiency measures memory efficiency
func BenchmarkMemoryEfficiency(b *testing.B) {
	forest, cleanup := setupBenchmarkForest(b)
	defer cleanup()

	var initialMem, finalMem runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&initialMem)

	b.ResetTimer()

	// Insert data and measure memory growth
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("efficiency-key-%d", i))
		value := []byte(fmt.Sprintf("efficiency-value-%d", i))
		forest.Insert(key, value)
	}

	runtime.GC()
	runtime.ReadMemStats(&finalMem)

	// Report memory metrics
	memoryUsed := finalMem.Alloc - initialMem.Alloc
	b.ReportMetric(float64(memoryUsed)/float64(b.N), "bytes/op")
	b.ReportMetric(float64(memoryUsed)/1024/1024, "total-MB")
}

// BenchmarkPersistencePerformance benchmarks persistence operations
func BenchmarkPersistencePerformance(b *testing.B) {
	tempDir := filepath.Join(os.TempDir(), fmt.Sprintf("dmt-persist-benchmark-%d", time.Now().UnixNano()))
	defer os.RemoveAll(tempDir)

	persist, err := NewPersistentStore(tempDir)
	if err != nil {
		b.Fatalf("Failed to create persistent store: %v", err)
	}
	defer persist.Close()

	b.ResetTimer()
	b.ReportAllocs()

	b.Run("WAL-Write", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			key := []byte(fmt.Sprintf("wal-key-%d", i))
			value := []byte(fmt.Sprintf("wal-value-%d", i))
			persist.LogInsert(key, value, 1, uint64(i+1))
		}
	})

	b.Run("Snapshot-Create", func(b *testing.B) {
		tree, cleanup := setupBenchmarkTree(b)
		defer cleanup()

		// Pre-populate tree
		for i := 0; i < 1000; i++ {
			key := []byte(fmt.Sprintf("snap-key-%d", i))
			value := []byte(fmt.Sprintf("snap-value-%d", i))
			tree.Insert(key, value)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tree.CreateSnapshot()
		}
	})
}

// BenchmarkOptimizedOperations benchmarks optimized versions of operations
func BenchmarkOptimizedOperations(b *testing.B) {
	b.Run("BatchInsert", func(b *testing.B) {
		forest, cleanup := setupBenchmarkForest(b)
		defer cleanup()

		// Prepare batch data
		batchSize := 100
		keys := make([][]byte, batchSize)
		values := make([][]byte, batchSize)

		for i := 0; i < batchSize; i++ {
			keys[i] = []byte(fmt.Sprintf("batch-key-%d", i))
			values[i] = []byte(fmt.Sprintf("batch-value-%d", i))
		}

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			// Simulate batch insert
			for j := 0; j < batchSize; j++ {
				forest.Insert(keys[j], values[j])
			}
		}
	})

	b.Run("PrefixScan", func(b *testing.B) {
		forest, cleanup := setupBenchmarkForest(b)
		defer cleanup()

		// Pre-populate with hierarchical data
		for i := 0; i < 10000; i++ {
			key := []byte(fmt.Sprintf("prefix/level1/level2/key-%05d", i))
			value := []byte(fmt.Sprintf("value-%d", i))
			forest.Insert(key, value)
		}

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			prefix := []byte("prefix/level1/")
			forest.Seek(prefix)
		}
	})
}

// setupBenchmarkForest creates a Forest for benchmarking
func setupBenchmarkForest(b *testing.B) (*Forest, func()) {
	tempDir := filepath.Join(os.TempDir(), fmt.Sprintf("dmt-benchmark-%d", time.Now().UnixNano()))

	config := ForestConfig{
		PersistDir: tempDir,
		NodeID:     "benchmark-node",
	}

	forest, err := NewForest(config)
	if err != nil {
		b.Fatalf("Failed to create forest: %v", err)
	}

	cleanup := func() {
		forest.Close()
		os.RemoveAll(tempDir)
	}

	return forest, cleanup
}

// setupBenchmarkTree creates a Tree for benchmarking
func setupBenchmarkTree(b *testing.B) (*Tree, func()) {
	tempDir := filepath.Join(os.TempDir(), fmt.Sprintf("dmt-tree-benchmark-%d", time.Now().UnixNano()))

	tree, err := NewTree(tempDir)
	if err != nil {
		b.Fatalf("Failed to create tree: %v", err)
	}

	cleanup := func() {
		tree.Close()
		os.RemoveAll(tempDir)
	}

	return tree, cleanup
}

// BenchmarkSuite runs a comprehensive benchmark suite
func BenchmarkSuite(b *testing.B) {
	benchmarks := []struct {
		name string
		fn   func(*testing.B)
	}{
		{"ForestInsert", BenchmarkForestInsert},
		{"ForestGet", BenchmarkForestGet},
		{"ForestSeek", BenchmarkForestSeek},
		{"TreeInsert", BenchmarkTreeInsert},
		{"TreeGet", BenchmarkTreeGet},
		{"MerkleInsert", BenchmarkMerkleTreeInsert},
		{"MerkleRebuild", BenchmarkMerkleTreeRebuild},
		{"MerkleDiff", BenchmarkMerkleTreeGetDiff},
		{"PersistenceInsert", BenchmarkPersistenceInsert},
		{"ConcurrentOps", BenchmarkConcurrentForestOperations},
		{"MemoryUsage", BenchmarkMemoryUsage},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, bm.fn)
	}
}
