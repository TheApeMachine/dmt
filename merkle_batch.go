package dmt

import (
	"context"
	"fmt"
	"sync"
)

// BatchOperation represents a single operation in a batch
type BatchOperation struct {
	Type  string // "insert", "delete"
	Key   []byte
	Value []byte
}

// BatchResult contains the result of a batch operation
type BatchResult struct {
	Success bool
	Error   error
	Index   int // Index in the original batch
}

// MerkleTreeBatch provides batch operations for better performance
type MerkleTreeBatch struct {
	tree       *MerkleTree
	operations []BatchOperation
	mu         sync.Mutex
}

// NewMerkleTreeBatch creates a new batch for the given tree
func NewMerkleTreeBatch(tree *MerkleTree) *MerkleTreeBatch {
	return &MerkleTreeBatch{
		tree:       tree,
		operations: make([]BatchOperation, 0, 100), // Pre-allocate for common case
	}
}

// AddInsert adds an insert operation to the batch
func (b *MerkleTreeBatch) AddInsert(key, value []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.operations = append(b.operations, BatchOperation{
		Type:  "insert",
		Key:   append([]byte(nil), key...),   // Copy to avoid aliasing
		Value: append([]byte(nil), value...), // Copy to avoid aliasing
	})
}

// Execute executes all operations in the batch
func (b *MerkleTreeBatch) Execute(ctx context.Context) ([]BatchResult, error) {
	b.mu.Lock()
	operations := make([]BatchOperation, len(b.operations))
	copy(operations, b.operations)
	b.mu.Unlock()

	results := make([]BatchResult, len(operations))

	// Execute all operations while holding the tree lock once
	b.tree.mu.Lock()
	defer b.tree.mu.Unlock()

	for i, op := range operations {
		select {
		case <-ctx.Done():
			return results[:i], ctx.Err()
		default:
		}

		switch op.Type {
		case "insert":
			err := b.executeInsert(op.Key, op.Value)
			results[i] = BatchResult{
				Success: err == nil,
				Error:   err,
				Index:   i,
			}
		default:
			results[i] = BatchResult{
				Success: false,
				Error:   fmt.Errorf("unknown operation type: %s", op.Type),
				Index:   i,
			}
		}
	}

	// Rebuild once after all operations
	if b.tree.modified {
		b.rebuildUnsafe()
	}

	return results, nil
}

// executeInsert performs insert without locking (assumes caller holds lock)
func (b *MerkleTreeBatch) executeInsert(key, value []byte) error {
	if len(key) == 0 {
		return fmt.Errorf("key cannot be empty")
	}

	if b.tree.leafMap == nil {
		return fmt.Errorf("merkle tree not properly initialized")
	}

	// Create leaf node
	leaf := &MerkleNode{
		Key:   append([]byte(nil), key...),
		Value: append([]byte(nil), value...),
		Hash:  b.tree.hashKV(key, value),
	}

	// Store in leaf map
	keyHex := string(key) // Avoid hex encoding for better performance
	b.tree.leafMap[keyHex] = leaf
	b.tree.modified = true
	return nil
}

// rebuildUnsafe rebuilds without locking (assumes caller holds lock)
func (b *MerkleTreeBatch) rebuildUnsafe() {
	if !b.tree.modified {
		return
	}

	// Clear caches
	b.tree.nodeMap = make(map[string]*MerkleNode)
	if b.tree.nodeCache != nil {
		b.tree.nodeCache.Clear()
	}

	// Build tree (simplified version - full implementation would be in merkle.go)
	leaves := make([]*MerkleNode, 0, len(b.tree.leafMap))
	for _, leaf := range b.tree.leafMap {
		leaves = append(leaves, leaf)
	}

	b.tree.Root = b.tree.buildLevel(leaves)
	b.tree.modified = false
}

// Clear clears all operations from the batch
func (b *MerkleTreeBatch) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.operations = b.operations[:0]
}

// Size returns the number of operations in the batch
func (b *MerkleTreeBatch) Size() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.operations)
}
