/*
package dmt implements a wrapper around an immutable radix tree data structure.
A radix tree (also known as a radix trie or compact prefix tree) is a space-optimized
tree structure that is particularly efficient for string or byte slice keys. It compresses
common prefixes to save space and enables fast lookups, insertions, and prefix-based searches.
*/
package dmt

import (
	"bytes"
	"container/ring"
	"sync"
	"time"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
)

/*
Tree wraps an immutable radix tree implementation from hashicorp/go-immutable-radix.
It stores byte slices as both keys and values, providing efficient prefix-based operations.
The immutable nature ensures thread-safety and enables persistent data structures.
*/
type Tree struct {
	root     *iradix.Tree[[]byte]
	updated  bool
	perfs    *ring.Ring
	persist  *PersistentStore
	term     uint64
	logIndex uint64
	mu       sync.RWMutex
}

/*
NewTree creates and returns a new empty Tree instance.
The underlying radix tree is initialized with no entries.
*/
func NewTree(persistDir string) (*Tree, error) {
	var persist *PersistentStore
	var err error
	var term, index uint64

	if persistDir != "" {
		persist, err = NewPersistentStore(persistDir)
		if err != nil {
			return nil, err
		}
		term, index = persist.GetLastState()
	}

	return &Tree{
		root:     iradix.New[[]byte](),
		perfs:    ring.New(10),
		persist:  persist,
		term:     term,
		logIndex: index,
	}, nil
}

/*
Seek performs a prefix-based search in the tree, finding the first value whose key
is greater than or equal to the provided key in lexicographical order.
Returns the value and true if found, or nil and false if no such key exists.
*/
func (tree *Tree) Seek(key []byte) ([]byte, bool) {
	t := time.Now()

	it := tree.root.Root().Iterator()
	it.SeekLowerBound(key)

	for k, v, ok := it.Next(); ok; k, v, ok = it.Next() {
		if bytes.Compare(k, key) >= 0 {
			return v, true
		}
	}

	tree.perfs.Value = time.Since(t).Nanoseconds()
	tree.perfs = tree.perfs.Next()

	return nil, false
}

/*
Insert adds or updates a key-value pair in the tree.
Due to the immutable nature of the tree, this operation creates a new version
of the tree rather than modifying the existing one.
Returns the updated tree and a boolean indicating if the tree was modified.
*/
func (tree *Tree) Insert(key []byte, value []byte) (*Tree, bool) {
	tree.mu.Lock()
	defer tree.mu.Unlock()

	t := time.Now()
	tree.root, _, tree.updated = tree.root.Insert(key, value)

	if tree.updated {
		tree.logIndex++
		// Log to WAL if persistence is enabled
		if tree.persist != nil {
			if err := tree.persist.LogInsert(key, value, tree.term, tree.logIndex); err != nil {
				// Log error but don't fail the operation
				// TODO: Add proper error handling/logging
				_ = err
			}
		}
	}

	tree.perfs.Value = time.Since(t).Nanoseconds()
	tree.perfs = tree.perfs.Next()
	return tree, tree.updated
}

/*
Get retrieves the value associated with the given key.
Returns the value and true if the key exists, or nil and false if it doesn't.
*/
func (tree *Tree) Get(key []byte) ([]byte, bool) {
	t := time.Now()
	v, ok := tree.root.Get(key)
	tree.perfs.Value = time.Since(t).Nanoseconds()
	tree.perfs = tree.perfs.Next()
	return v, ok
}

/*
AVG returns the average performance of the tree in nanoseconds.
*/
func (tree *Tree) AVG() int64 {
	var sum int64

	tree.perfs.Do(func(v any) {
		sum += v.(int64)
	})

	return sum / int64(tree.perfs.Len())
}

/*
Close closes the tree and persists any remaining data.
*/
func (tree *Tree) Close() error {
	if tree.persist != nil {
		return tree.persist.Close()
	}
	return nil
}

// UpdateTerm updates the current term number
func (tree *Tree) UpdateTerm(term uint64) {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	tree.term = term
}

// GetLogState returns the current term and log index
func (tree *Tree) GetLogState() (term, index uint64) {
	tree.mu.RLock()
	defer tree.mu.RUnlock()
	return tree.term, tree.logIndex
}
