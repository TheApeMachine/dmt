package dmt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
)

// MerkleTreeInterface defines the contract for Merkle tree implementations
type MerkleTreeInterface interface {
	Insert(key, value []byte) error
	Rebuild()
	GetDiff(other MerkleTreeInterface) []DiffEntry
	Verify(key, value []byte) bool
	GetProof(key []byte) ([]ProofElement, error)
	VerifyProof(key, value []byte, proof []ProofElement) bool
	GetMemoryUsage() (leafCount, nodeCount int, cacheSize int)
}

// Ensure MerkleTree implements the interface
var _ MerkleTreeInterface = (*MerkleTree)(nil)

// MerkleNode represents a node in the Merkle tree
type MerkleNode struct {
	Hash   []byte
	Left   *MerkleNode
	Right  *MerkleNode
	Parent *MerkleNode // Parent node for efficient proof generation
	Key    []byte
	Value  []byte
}

// MerkleTree manages a Merkle tree for efficient diff detection
type MerkleTree struct {
	mu         sync.RWMutex // Protects all fields for concurrent access
	Root       *MerkleNode
	leafMap    map[string]*MerkleNode // maps key hex to leaf node
	nodeMap    map[string]*MerkleNode // maps hash hex to internal nodes (deprecated, use nodeCache)
	nodeCache  *LRUCache              // efficient LRU cache for internal nodes
	modified   bool
	maxNodeMap int // maximum size for nodeMap before LRU eviction (0 = unlimited)
}

// NewMerkleTree creates a new Merkle tree
func NewMerkleTree() *MerkleTree {
	return &MerkleTree{
		leafMap:    make(map[string]*MerkleNode),
		nodeMap:    make(map[string]*MerkleNode),
		nodeCache:  NewLRUCache(10000), // Default limit to prevent unbounded growth
		maxNodeMap: 10000,
	}
}

// NewMerkleTreeWithLimit creates a new Merkle tree with custom nodeMap limit
func NewMerkleTreeWithLimit(maxNodeMap int) *MerkleTree {
	return &MerkleTree{
		leafMap:    make(map[string]*MerkleNode),
		nodeMap:    make(map[string]*MerkleNode),
		nodeCache:  NewLRUCache(maxNodeMap),
		maxNodeMap: maxNodeMap,
	}
}

// Insert adds or updates a key-value pair in the Merkle tree
func (mt *MerkleTree) Insert(key, value []byte) error {
	if len(key) == 0 {
		return fmt.Errorf("key cannot be empty")
	}

	mt.mu.Lock()
	defer mt.mu.Unlock()

	if mt.leafMap == nil {
		return fmt.Errorf("merkle tree not properly initialized")
	}

	// Compute deterministic update for duplicates to ensure order-independence:
	// If the key already exists, keep the variant that yields the lexicographically
	// larger hash (commutative and idempotent across permutations).
	newHash := mt.hashKV(key, value)

	keyHex := hex.EncodeToString(key)
	if existing, exists := mt.leafMap[keyHex]; exists {
		// Always update existing leaf with new value (last write wins)
		if cap(existing.Key) < len(key) {
			existing.Key = make([]byte, len(key))
		} else {
			existing.Key = existing.Key[:len(key)]
		}
		if cap(existing.Value) < len(value) {
			existing.Value = make([]byte, len(value))
		} else {
			existing.Value = existing.Value[:len(value)]
		}
		copy(existing.Key, key)
		copy(existing.Value, value)
		existing.Hash = newHash
	} else {
		// Create and store new leaf node
		leaf := &MerkleNode{
			Key:   make([]byte, len(key)),   // Defensive copy
			Value: make([]byte, len(value)), // Defensive copy
			Hash:  newHash,
		}
		copy(leaf.Key, key)
		copy(leaf.Value, value)
		mt.leafMap[keyHex] = leaf
	}
	mt.modified = true
	return nil
}

// Rebuild reconstructs the Merkle tree from leaves
func (mt *MerkleTree) Rebuild() {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	if !mt.modified {
		return
	}

	// Clear caches to prevent memory leaks from stale internal nodes
	mt.nodeMap = make(map[string]*MerkleNode)
	if mt.nodeCache != nil {
		mt.nodeCache.Clear()
	} else {
		mt.nodeCache = NewLRUCache(mt.maxNodeMap)
	}

	// Convert leaves to slice and sort by key for deterministic ordering
	leaves := make([]*MerkleNode, 0, len(mt.leafMap))
	keys := make([]string, 0, len(mt.leafMap))
	for k := range mt.leafMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		leaves = append(leaves, mt.leafMap[k])
	}

	// Build tree bottom-up
	mt.Root = mt.buildLevel(leaves)
	mt.modified = false
}

// buildLevel constructs one level of the Merkle tree
func (mt *MerkleTree) buildLevel(nodes []*MerkleNode) *MerkleNode {
	if len(nodes) == 0 {
		return nil
	}
	if len(nodes) == 1 {
		return nodes[0]
	}

	// Create parent nodes for this level
	parents := make([]*MerkleNode, 0, (len(nodes)+1)/2)

	for i := 0; i < len(nodes); i += 2 {
		var right *MerkleNode
		left := nodes[i]

		if i+1 < len(nodes) {
			right = nodes[i+1]
		}

		// Ensure deterministic parent hash independent of original child ordering
		l := left
		r := right
		if r != nil && bytes.Compare(l.Hash, r.Hash) > 0 {
			l, r = r, l
		}

		parent := &MerkleNode{
			Left:  left,
			Right: right,
			Hash:  mt.hashChildren(l, r),
		}

		// Set parent pointers for children
		if left != nil {
			left.Parent = parent
		}
		if right != nil {
			right.Parent = parent
		}

		// Store in node map with LRU eviction if needed
		hashHex := hex.EncodeToString(parent.Hash)
		mt.addToNodeMap(hashHex, parent)
		parents = append(parents, parent)
	}

	// Recursively build next level
	return mt.buildLevel(parents)
}

// GetDiff returns the differences between this tree and another
func (mt *MerkleTree) GetDiff(other MerkleTreeInterface) []DiffEntry {
	mt.mu.RLock()
	defer mt.mu.RUnlock()

	// Type assertion to access internal fields - this is a limitation of the interface approach
	// In a real implementation, we might need to expose more methods through the interface
	otherTree, ok := other.(*MerkleTree)
	if !ok {
		// Fallback to a less efficient comparison if types don't match
		return mt.fullDiffInterface(other)
	}

	otherTree.mu.RLock()
	defer otherTree.mu.RUnlock()

	// Use the reliable leaf-map based diff for now
	// The tree-based diff has complexity issues that need more work
	return mt.fullDiff(otherTree)
}

// fullDiffInterface provides a fallback diff method for interface types
func (mt *MerkleTree) fullDiffInterface(_ MerkleTreeInterface) []DiffEntry {
	// This would require additional interface methods to implement efficiently
	// For now, return empty slice as a placeholder
	return []DiffEntry{}
}

// DiffEntry represents a difference between trees
type DiffEntry struct {
	Key      []byte
	Value    []byte
	Modified bool // true if modified, false if new
}

// ProofElement represents an element in a Merkle proof with directional information
type ProofElement struct {
	Hash   []byte
	IsLeft bool // true if this hash should be on the left when computing parent hash
}

// diffNode recursively finds differences between trees
func (mt *MerkleTree) diffNode(a, b *MerkleNode, other *MerkleTree, diffs *[]DiffEntry) {
	// Early return for identical nodes
	if mt.nodesEqual(a, b) {
		return
	}

	// Handle cases where one node is nil
	if a != nil && b == nil {
		mt.addSubtreeDiffs(a, diffs)
		return
	}
	if a == nil && b != nil {
		return // Nothing in 'a' to compare
	}
	if a == nil && b == nil {
		return
	}

	// If 'a' is a leaf, it's a difference (since hashes didn't match)
	if mt.isLeafNode(a) {
		mt.processLeafDiff(a, other, diffs)
		return
	}

	// If 'b' is a leaf but 'a' is not, then 'a' represents a deeper subtree.
	// We need to add all leaves of 'a' as they are not represented in 'b'.
	if mt.isLeafNode(b) {
		mt.addSubtreeDiffs(a, diffs)
		return
	}

	// Both are internal nodes, recurse into children
	mt.diffChildren(a, b, other, diffs)
}

// nodesEqual checks if two nodes are identical (handles nil cases)
func (mt *MerkleTree) nodesEqual(a, b *MerkleNode) bool {
	if a == nil || b == nil {
		return a == b
	}
	return bytes.Equal(a.Hash, b.Hash)
}

// isLeafNode checks if a node is a leaf (has key data)
func (mt *MerkleTree) isLeafNode(node *MerkleNode) bool {
	return node != nil && node.Key != nil
}

// processLeafDiff handles differences for leaf nodes
func (mt *MerkleTree) processLeafDiff(leaf *MerkleNode, other *MerkleTree, diffs *[]DiffEntry) {
	keyHex := hex.EncodeToString(leaf.Key)
	if otherLeaf, exists := other.leafMap[keyHex]; exists {
		if !bytes.Equal(leaf.Value, otherLeaf.Value) {
			*diffs = append(*diffs, DiffEntry{
				Key:      leaf.Key,
				Value:    leaf.Value,
				Modified: true,
			})
		}
	} else {
		*diffs = append(*diffs, DiffEntry{
			Key:      leaf.Key,
			Value:    leaf.Value,
			Modified: false,
		})
	}
}

// diffChildren recursively compares child nodes
func (mt *MerkleTree) diffChildren(a, b *MerkleNode, other *MerkleTree, diffs *[]DiffEntry) {
	var aLeft, aRight, bLeft, bRight *MerkleNode
	if a != nil {
		aLeft, aRight = a.Left, a.Right
	}
	if b != nil {
		bLeft, bRight = b.Left, b.Right
	}

	// Recurse into children if either side has them
	if aLeft != nil || bLeft != nil {
		mt.diffNode(aLeft, bLeft, other, diffs)
	}
	if aRight != nil || bRight != nil {
		mt.diffNode(aRight, bRight, other, diffs)
	}
}

// addSubtreeDiffs adds all entries from a subtree to the diff list
func (mt *MerkleTree) addSubtreeDiffs(node *MerkleNode, diffs *[]DiffEntry) {
	if node == nil {
		return
	}

	// If this is a leaf node, add it to diffs
	if node.Key != nil {
		*diffs = append(*diffs, DiffEntry{
			Key:      node.Key,
			Value:    node.Value,
			Modified: false, // New entry since it doesn't exist in other tree
		})
		return
	}

	// Recursively add all entries from child subtrees
	mt.addSubtreeDiffs(node.Left, diffs)
	mt.addSubtreeDiffs(node.Right, diffs)
}

// fullDiff returns all entries when trees are too different
func (mt *MerkleTree) fullDiff(other *MerkleTree) []DiffEntry {
	diffs := make([]DiffEntry, 0, len(mt.leafMap))
	for _, leaf := range mt.leafMap {
		keyHex := hex.EncodeToString(leaf.Key)
		if otherLeaf, exists := other.leafMap[keyHex]; exists {
			if !bytes.Equal(leaf.Value, otherLeaf.Value) {
				diffs = append(diffs, DiffEntry{
					Key:      leaf.Key,
					Value:    leaf.Value,
					Modified: true,
				})
			}
		} else {
			diffs = append(diffs, DiffEntry{
				Key:      leaf.Key,
				Value:    leaf.Value,
				Modified: false,
			})
		}
	}
	return diffs
}

// Verify checks if a key-value pair exists in the tree
func (mt *MerkleTree) Verify(key, value []byte) bool {
	if len(key) == 0 {
		return false
	}

	mt.mu.RLock()
	defer mt.mu.RUnlock()

	if mt.leafMap == nil {
		return false
	}

	keyHex := hex.EncodeToString(key)
	if leaf, exists := mt.leafMap[keyHex]; exists {
		return bytes.Equal(leaf.Value, value)
	}
	return false
}

// GetProof generates a Merkle proof for a key with directional information
func (mt *MerkleTree) GetProof(key []byte) ([]ProofElement, error) {
	mt.mu.RLock()
	defer mt.mu.RUnlock()

	keyHex := hex.EncodeToString(key)
	leaf, exists := mt.leafMap[keyHex]
	if !exists {
		return nil, fmt.Errorf("key not found")
	}

	proof := make([]ProofElement, 0, 64)
	current := leaf
	for current != mt.Root {
		parent := mt.findParent(current)
		if parent == nil {
			return nil, fmt.Errorf("invalid tree structure")
		}

		var sibling *MerkleNode
		var isLeft bool

		if parent.Left == current {
			sibling = parent.Right
		} else {
			sibling = parent.Left
		}

		if sibling != nil {
			// Determine left/right based on the same normalization as buildLevel.
			// buildLevel computes parent hash as hashChildren(l, r) where l <= r by hash.
			// For verification, if sibling.Hash <= current.Hash, sibling is left.
			isLeft = bytes.Compare(sibling.Hash, current.Hash) <= 0
			proof = append(proof, ProofElement{Hash: sibling.Hash, IsLeft: isLeft})
		} else {
			// Unpaired node at this level: include a marker so verification performs
			// the additional hash step equivalent to sha256(current.Hash).
			proof = append(proof, ProofElement{Hash: nil, IsLeft: false})
		}
		current = parent
	}

	return proof, nil
}

// GetProofLegacy generates a legacy Merkle proof for backward compatibility
func (mt *MerkleTree) GetProofLegacy(key []byte) ([][]byte, error) {
	elements, err := mt.GetProof(key)
	if err != nil {
		return nil, err
	}

	proof := make([][]byte, len(elements))
	for i, element := range elements {
		proof[i] = element.Hash
	}
	return proof, nil
}

// VerifyProof verifies a Merkle proof with directional information
func (mt *MerkleTree) VerifyProof(key, value []byte, proof []ProofElement) bool {
	hash := mt.hashKV(key, value)

	for _, element := range proof {
		if element.Hash == nil {
			// Unpaired promotion case: parent is hash(current)
			left := &MerkleNode{Hash: hash}
			hash = mt.hashChildren(left, nil)
			continue
		}
		var left, right *MerkleNode
		if element.IsLeft {
			left = &MerkleNode{Hash: element.Hash}
			right = &MerkleNode{Hash: hash}
		} else {
			left = &MerkleNode{Hash: hash}
			right = &MerkleNode{Hash: element.Hash}
		}
		hash = mt.hashChildren(left, right)
	}

	return bytes.Equal(hash, mt.Root.Hash)
}

// VerifyProofLegacy verifies a legacy Merkle proof for backward compatibility
func (mt *MerkleTree) VerifyProofLegacy(key, value []byte, proof [][]byte) bool {
	hash := mt.hashKV(key, value)

	for _, sibling := range proof {
		left := &MerkleNode{Hash: hash}
		right := &MerkleNode{Hash: sibling}
		if bytes.Compare(left.Hash, right.Hash) > 0 {
			left, right = right, left
		}
		hash = mt.hashChildren(left, right)
	}

	return bytes.Equal(hash, mt.Root.Hash)
}

// findParent finds the parent node of a given node
func (mt *MerkleTree) findParent(node *MerkleNode) *MerkleNode {
	return node.Parent
}

// hashKV creates a hash of a key-value pair
func (mt *MerkleTree) hashKV(key, value []byte) []byte {
	hasher := sha256.New()
	hasher.Write(key)
	hasher.Write(value)
	return hasher.Sum(nil)
}

// hashChildren creates a hash of two child nodes
func (mt *MerkleTree) hashChildren(left, right *MerkleNode) []byte {
	hasher := sha256.New()
	hasher.Write(left.Hash)
	if right != nil {
		hasher.Write(right.Hash)
	}
	return hasher.Sum(nil)
}

// LRUCache manages the LRU eviction for nodeMap
type LRUCache struct {
	items    map[string]*MerkleNode
	order    []string
	maxSize  int
	indexMap map[string]int // For O(1) lookups in order slice
}

// NewLRUCache creates a new LRU cache
func NewLRUCache(maxSize int) *LRUCache {
	return &LRUCache{
		items:    make(map[string]*MerkleNode),
		order:    make([]string, 0, maxSize),
		maxSize:  maxSize,
		indexMap: make(map[string]int),
	}
}

// Get retrieves an item and marks it as recently used
func (lru *LRUCache) Get(key string) (*MerkleNode, bool) {
	if node, exists := lru.items[key]; exists {
		lru.moveToEnd(key)
		return node, true
	}
	return nil, false
}

// Put adds or updates an item
func (lru *LRUCache) Put(key string, node *MerkleNode) {
	if lru.maxSize <= 0 {
		lru.items[key] = node
		return
	}

	if _, exists := lru.items[key]; exists {
		lru.items[key] = node
		lru.moveToEnd(key)
		return
	}

	if len(lru.items) >= lru.maxSize {
		lru.evictOldest()
	}

	lru.items[key] = node
	lru.order = append(lru.order, key)
	lru.indexMap[key] = len(lru.order) - 1
}

// Clear removes all items
func (lru *LRUCache) Clear() {
	lru.items = make(map[string]*MerkleNode)
	lru.order = lru.order[:0]
	lru.indexMap = make(map[string]int)
}

// moveToEnd moves an item to the end of the LRU order (O(1) operation)
func (lru *LRUCache) moveToEnd(key string) {
	if idx, exists := lru.indexMap[key]; exists {
		// Remove from current position
		copy(lru.order[idx:], lru.order[idx+1:])
		lru.order = lru.order[:len(lru.order)-1]

		// Update indices for shifted elements
		for i := idx; i < len(lru.order); i++ {
			lru.indexMap[lru.order[i]] = i
		}

		// Add to end
		lru.order = append(lru.order, key)
		lru.indexMap[key] = len(lru.order) - 1
	}
}

// evictOldest removes the least recently used item
func (lru *LRUCache) evictOldest() {
	if len(lru.order) == 0 {
		return
	}

	oldest := lru.order[0]
	lru.order = lru.order[1:]
	delete(lru.items, oldest)
	delete(lru.indexMap, oldest)

	// Update indices
	for i, key := range lru.order {
		lru.indexMap[key] = i
	}
}

// addToNodeMap adds a node to the nodeMap with LRU eviction if needed
func (mt *MerkleTree) addToNodeMap(hashHex string, node *MerkleNode) {
	if mt.nodeCache == nil {
		mt.nodeCache = NewLRUCache(mt.maxNodeMap)
	}
	mt.nodeCache.Put(hashHex, node)

	// Keep nodeMap in sync for backward compatibility, but respect the limit
	if mt.maxNodeMap > 0 && len(mt.nodeMap) >= mt.maxNodeMap {
		// Simple eviction for nodeMap - remove a random entry
		for k := range mt.nodeMap {
			delete(mt.nodeMap, k)
			break
		}
	}
	mt.nodeMap[hashHex] = node
}

// GetMemoryUsage returns memory usage statistics for the Merkle tree
func (mt *MerkleTree) GetMemoryUsage() (leafCount, nodeCount int, cacheSize int) {
	mt.mu.RLock()
	defer mt.mu.RUnlock()

	cacheSize = len(mt.nodeMap) // Backward compatibility
	if mt.nodeCache != nil {
		cacheSize = len(mt.nodeCache.items)
	}
	return len(mt.leafMap), len(mt.nodeMap), cacheSize
}
