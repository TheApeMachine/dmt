package dmt

import (
	"context"
	"fmt"
	"sync"
	"time"
)

/*
Forest manages a collection of Tree instances, providing intelligent routing of operations
to the most performant tree based on running performance metrics. It maintains data
consistency across all trees while optimizing read operations by selecting the fastest
responding tree.
*/
type Forest struct {
	trees []*Tree
	mu    sync.RWMutex
	// Channel to signal new updates that need synchronization
	updates chan struct{}
	// Context for controlling background sync
	ctx    context.Context
	cancel context.CancelFunc
	// Network node for distributed operation
	network *NetworkNode
}

// ForestConfig holds configuration for creating a new Forest
type ForestConfig struct {
	// Directory for persistence
	PersistDir string
	// Network configuration
	Network *NetworkConfig
}

/*
NewForest creates and returns a new empty Forest instance with background
synchronization enabled. The forest starts with no trees and trees can be
added using the AddTree method. A background goroutine is started to handle
tree synchronization.
*/
func NewForest(config ForestConfig) (*Forest, error) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &Forest{
		updates: make(chan struct{}, 1), // Buffered channel to prevent blocking
		ctx:     ctx,
		cancel:  cancel,
	}

	// Create initial tree with persistence if directory provided
	if config.PersistDir != "" {
		tree, err := NewTree(config.PersistDir)
		if err != nil {
			cancel()
			return nil, err
		}
		f.AddTree(tree)
	}

	// Initialize network node if network config provided
	if config.Network != nil {
		network, err := NewNetworkNode(*config.Network, f)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to create network node: %w", err)
		}
		f.network = network
	}

	go f.syncLoop()
	return f, nil
}

/*
Close stops the background synchronization goroutine and cleans up resources.
Should be called when the Forest is no longer needed.
*/
func (f *Forest) Close() {
	if f.cancel != nil {
		f.cancel()
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// Close network node if it exists
	if f.network != nil {
		f.network.Close()
	}

	// Close all trees
	for _, tree := range f.trees {
		if err := tree.Close(); err != nil {
			// TODO: Add proper error handling/logging
			_ = err
		}
	}
}

/*
syncLoop runs in the background and handles synchronization of trees.
It is triggered either by updates or periodically to ensure consistency.
*/
func (f *Forest) syncLoop() {
	ticker := time.NewTicker(5 * time.Second) // Periodic sync every 5 seconds
	defer ticker.Stop()

	for {
		select {
		case <-f.ctx.Done():
			return
		case <-f.updates: // Triggered by new updates
			f.synchronizeTrees()
		case <-ticker.C: // Periodic sync
			f.synchronizeTrees()
		}
	}
}

/*
synchronizeTrees ensures all trees have consistent data by comparing and
updating them based on the most up-to-date tree.
*/
func (forest *Forest) synchronizeTrees() {
	forest.mu.Lock()
	defer forest.mu.Unlock()

	if len(forest.trees) <= 1 {
		return
	}

	// Use the first tree as reference
	reference := forest.trees[0]

	// Build Merkle tree for reference
	refMerkle := NewMerkleTree()
	it := reference.root.Root().Iterator()
	for key, value, ok := it.Next(); ok; key, value, ok = it.Next() {
		refMerkle.Insert(key, value)
	}
	refMerkle.Rebuild()

	// Sync other trees using Merkle diffs
	for _, tree := range forest.trees[1:] {
		// Build Merkle tree for target
		targetMerkle := NewMerkleTree()
		it := tree.root.Root().Iterator()
		for key, value, ok := it.Next(); ok; key, value, ok = it.Next() {
			targetMerkle.Insert(key, value)
		}
		targetMerkle.Rebuild()

		// Get diff and apply changes
		diffs := refMerkle.GetDiff(targetMerkle)
		for _, diff := range diffs {
			tree.Insert(diff.Key, diff.Value)
		}
	}
}

/*
AddTree incorporates a new Tree instance into the forest.
Each added tree will be maintained with identical data but may have different
performance characteristics based on its specific implementation or state.
*/
func (forest *Forest) AddTree(tree *Tree) {
	forest.mu.Lock()
	forest.trees = append(forest.trees, tree)
	forest.mu.Unlock()
	// Trigger synchronization for the new tree
	select {
	case forest.updates <- struct{}{}:
	default:
	}
}

/*
getFastestTree returns the tree with the lowest average performance time.
It analyzes the running performance metrics of each tree to determine which
one is currently responding most quickly to operations. Returns nil if the
forest contains no trees.
*/
func (forest *Forest) getFastestTree() *Tree {
	forest.mu.RLock()
	defer forest.mu.RUnlock()

	if len(forest.trees) == 0 {
		return nil
	}

	fastestTree := forest.trees[0]
	fastestAvg := fastestTree.AVG()

	for _, tree := range forest.trees[1:] {
		if avg := tree.AVG(); avg < fastestAvg {
			fastestTree = tree
			fastestAvg = avg
		}
	}

	return fastestTree
}

/*
Get retrieves a value from the forest using the most performant tree.
It automatically selects the tree with the best average response time to
handle the request. Returns the value and true if the key exists, or nil
and false if it doesn't or if the forest is empty.
*/
func (forest *Forest) Get(key []byte) ([]byte, bool) {
	fastestTree := forest.getFastestTree()
	if fastestTree == nil {
		return nil, false
	}
	return fastestTree.Get(key)
}

/*
Seek performs a prefix-based search using the most performant tree in the forest.
It finds the first value whose key is greater than or equal to the provided key
in lexicographical order. Returns the value and true if found, or nil and false
if no such key exists or if the forest is empty.
*/
func (forest *Forest) Seek(key []byte) ([]byte, bool) {
	fastestTree := forest.getFastestTree()
	if fastestTree == nil {
		return nil, false
	}
	return fastestTree.Seek(key)
}

/*
Insert adds or updates a key-value pair across all trees in the forest.
To maintain data consistency, the operation is performed on every tree,
ensuring that subsequent read operations will find the same data regardless
of which tree they query. This method prioritizes consistency over performance.
*/
func (forest *Forest) Insert(key []byte, value []byte) {
	forest.mu.Lock()
	defer forest.mu.Unlock()

	// Update all local trees immediately
	for _, tree := range forest.trees {
		tree.Insert(key, value)
	}

	// Broadcast to other nodes if networked
	if forest.network != nil {
		forest.network.BroadcastInsert(key, value)
	}
}
