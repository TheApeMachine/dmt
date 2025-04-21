package dmt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// MerkleNode represents a node in the Merkle tree
type MerkleNode struct {
	Hash  []byte
	Left  *MerkleNode
	Right *MerkleNode
	Key   []byte
	Value []byte
}

// MerkleTree manages a Merkle tree for efficient diff detection
type MerkleTree struct {
	Root     *MerkleNode
	leafMap  map[string]*MerkleNode // maps key hex to leaf node
	nodeMap  map[string]*MerkleNode // maps hash hex to internal nodes
	modified bool
}

// NewMerkleTree creates a new Merkle tree
func NewMerkleTree() *MerkleTree {
	return &MerkleTree{
		leafMap: make(map[string]*MerkleNode),
		nodeMap: make(map[string]*MerkleNode),
	}
}

// Insert adds or updates a key-value pair in the Merkle tree
func (mt *MerkleTree) Insert(key, value []byte) {
	// Create leaf node
	leaf := &MerkleNode{
		Key:   key,
		Value: value,
		Hash:  mt.hashKV(key, value),
	}

	// Store in leaf map
	keyHex := hex.EncodeToString(key)
	mt.leafMap[keyHex] = leaf
	mt.modified = true
}

// Rebuild reconstructs the Merkle tree from leaves
func (mt *MerkleTree) Rebuild() {
	if !mt.modified {
		return
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

		parent := &MerkleNode{
			Left:  left,
			Right: right,
			Hash:  mt.hashChildren(left, right),
		}

		// Store in node map
		hashHex := hex.EncodeToString(parent.Hash)
		mt.nodeMap[hashHex] = parent
		parents = append(parents, parent)
	}

	// Recursively build next level
	return mt.buildLevel(parents)
}

// GetDiff returns the differences between this tree and another
func (mt *MerkleTree) GetDiff(other *MerkleTree) []DiffEntry {
	if mt.Root == nil || other.Root == nil {
		return mt.fullDiff(other)
	}

	diffs := make([]DiffEntry, 0)
	mt.diffNode(mt.Root, other.Root, other, &diffs)
	return diffs
}

// DiffEntry represents a difference between trees
type DiffEntry struct {
	Key      []byte
	Value    []byte
	Modified bool // true if modified, false if new
}

// diffNode recursively finds differences between trees
func (mt *MerkleTree) diffNode(a, b *MerkleNode, other *MerkleTree, diffs *[]DiffEntry) {
	if bytes.Equal(a.Hash, b.Hash) {
		return // nodes are identical
	}

	// If leaf nodes differ, record the difference
	if a.Key != nil {
		keyHex := hex.EncodeToString(a.Key)
		if otherLeaf, exists := other.leafMap[keyHex]; exists {
			if !bytes.Equal(a.Value, otherLeaf.Value) {
				*diffs = append(*diffs, DiffEntry{
					Key:      a.Key,
					Value:    a.Value,
					Modified: true,
				})
			}
		} else {
			*diffs = append(*diffs, DiffEntry{
				Key:      a.Key,
				Value:    a.Value,
				Modified: false,
			})
		}
		return
	}

	// Recurse into children
	if a.Left != nil && b.Left != nil {
		mt.diffNode(a.Left, b.Left, other, diffs)
	}
	if a.Right != nil && b.Right != nil {
		mt.diffNode(a.Right, b.Right, other, diffs)
	}
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
	keyHex := hex.EncodeToString(key)
	if leaf, exists := mt.leafMap[keyHex]; exists {
		return bytes.Equal(leaf.Value, value)
	}
	return false
}

// GetProof generates a Merkle proof for a key
func (mt *MerkleTree) GetProof(key []byte) ([][]byte, error) {
	keyHex := hex.EncodeToString(key)
	leaf, exists := mt.leafMap[keyHex]
	if !exists {
		return nil, fmt.Errorf("key not found")
	}

	proof := make([][]byte, 0)
	current := leaf
	for current != mt.Root {
		parent := mt.findParent(current)
		if parent == nil {
			return nil, fmt.Errorf("invalid tree structure")
		}

		if parent.Left == current {
			if parent.Right != nil {
				proof = append(proof, parent.Right.Hash)
			}
		} else {
			proof = append(proof, parent.Left.Hash)
		}
		current = parent
	}

	return proof, nil
}

// VerifyProof verifies a Merkle proof
func (mt *MerkleTree) VerifyProof(key, value []byte, proof [][]byte) bool {
	hash := mt.hashKV(key, value)

	for _, sibling := range proof {
		if bytes.Compare(hash, sibling) < 0 {
			hash = mt.hashChildren(&MerkleNode{Hash: hash}, &MerkleNode{Hash: sibling})
		} else {
			hash = mt.hashChildren(&MerkleNode{Hash: sibling}, &MerkleNode{Hash: hash})
		}
	}

	return bytes.Equal(hash, mt.Root.Hash)
}

// findParent finds the parent node of a given node
func (mt *MerkleTree) findParent(node *MerkleNode) *MerkleNode {
	for _, parent := range mt.nodeMap {
		if parent.Left == node || parent.Right == node {
			return parent
		}
	}
	return nil
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
