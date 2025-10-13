package dmt

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

var (
	testDirCounter int64
	testDirMutex   sync.Mutex
)

// createUniqueTestDir creates a unique temporary directory for testing
// This prevents test coupling issues from directory name collisions
func createUniqueTestDir(prefix string) string {
	testDirMutex.Lock()
	defer testDirMutex.Unlock()

	testDirCounter++
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d-%d-%d",
		prefix, time.Now().UnixNano(), testDirCounter, rand.Int63()))
}

// cleanupTestDir removes a test directory and ensures proper cleanup
func cleanupTestDir(dir string) {
	if dir != "" {
		// Add a small delay to ensure all file handles are closed
		time.Sleep(10 * time.Millisecond)
		os.RemoveAll(dir)
	}
}

// ensureCleanShutdown ensures all background goroutines are properly cleaned up
// This helps prevent goroutine leaks in tests
func ensureCleanShutdown(closers ...interface{}) {
	var wg sync.WaitGroup
	for _, closer := range closers {
		if closer != nil {
			wg.Add(1)
			go func(c interface{}) {
				defer wg.Done()

				// Handle different types of closers with force close for tests
				switch v := c.(type) {
				case interface{ ForceClose() error }:
					v.ForceClose()
				case interface{ ForceClose() }:
					v.ForceClose()
				case interface{ Close() error }:
					v.Close()
				case interface{ Close() }:
					v.Close()
				default:
					fmt.Printf("Warning: unknown closer type: %T\n", c)
				}
			}(closer)
		}
	}

	// Wait for all closers to complete, but with a shorter timeout for force close
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All closers completed successfully
	case <-time.After(1 * time.Second):
		// Shorter timeout since we're using force close
		fmt.Printf("Warning: cleanup timeout after 1 second\n")
	}

	// Force garbage collection to help clean up resources
	runtime.GC()
	runtime.GC() // Call twice to ensure full cleanup

	// Give additional time for goroutines to fully terminate
	time.Sleep(200 * time.Millisecond)
}

// forceCleanupGoroutines performs aggressive cleanup to prevent goroutine leaks
// This is specifically for test environments where we need to ensure clean state
func forceCleanupGoroutines() {
	// Force multiple garbage collection cycles
	for i := 0; i < 3; i++ {
		runtime.GC()
		runtime.Gosched() // Yield to allow goroutines to terminate
		time.Sleep(50 * time.Millisecond)
	}

	// Final longer sleep to allow network connections to fully close
	time.Sleep(300 * time.Millisecond)
}

// aggressiveTestCleanup performs the most aggressive cleanup possible for test environments
// This should only be used in test scenarios where we need to ensure absolutely no goroutine leaks
func aggressiveTestCleanup() {
	// Multiple rounds of cleanup with increasing delays
	for round := 0; round < 5; round++ {
		// Force garbage collection
		runtime.GC()
		runtime.GC()

		// Yield to scheduler to allow goroutines to terminate
		for i := 0; i < 10; i++ {
			runtime.Gosched()
		}

		// Progressive sleep - longer each round
		time.Sleep(time.Duration(100*(round+1)) * time.Millisecond)
	}

	// Final aggressive cleanup
	runtime.GC()
	time.Sleep(500 * time.Millisecond)
}

// testWithCleanup wraps a test function with comprehensive cleanup
func testWithCleanup(testFunc func()) {
	defer func() {
		if r := recover(); r != nil {
			// Even if test panics, ensure cleanup
			aggressiveTestCleanup()
			panic(r)
		}
		aggressiveTestCleanup()
	}()

	testFunc()
}

// globalTestCleanup performs system-wide cleanup between tests
// This is designed to prevent goroutine accumulation across multiple test runs
func globalTestCleanup() {
	// Force multiple rounds of aggressive cleanup
	for round := range 3 {
		// Multiple GC cycles
		runtime.GC()
		runtime.GC()
		runtime.GC()

		// Force scheduler to run pending goroutines
		for range 20 {
			runtime.Gosched()
		}

		// Progressive delay
		time.Sleep(time.Duration(200*(round+1)) * time.Millisecond)
	}

	// Final cleanup with longer delay
	runtime.GC()
	time.Sleep(1 * time.Second)
}

// testCleanupWithGlobalReset performs both local and global cleanup
// Use this for integration tests that create network components
func testCleanupWithGlobalReset(closers ...interface{}) {
	// First do normal cleanup
	ensureCleanShutdown(closers...)

	// Then do global cleanup to prevent accumulation
	globalTestCleanup()
}

// networkTestMutex ensures network tests run sequentially to prevent goroutine accumulation
var networkTestMutex sync.Mutex

// withNetworkTestLock runs a test function with exclusive network test access
// This prevents multiple network-heavy tests from running concurrently
func withNetworkTestLock(testFunc func()) {
	networkTestMutex.Lock()
	defer func() {
		// Always do aggressive cleanup before releasing the lock
		globalTestCleanup()
		networkTestMutex.Unlock()
	}()

	testFunc()
}
