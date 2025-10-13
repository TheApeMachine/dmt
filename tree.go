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
	logger   Logger
}

/*
NewTree creates a new radix tree with optional persistence.
*/
func NewTree(persistDir string) (*Tree, error) {
	var persist *PersistentStore
	var err error
	var term, index uint64

	logger := DefaultLogger("tree")
	logger.Info("Creating new tree", "persist_dir", persistDir)

	root := iradix.New[[]byte]()

	if persistDir != "" {
		persist, err = NewPersistentStore(persistDir)
		if err != nil {
			logger.Error("Failed to create persistent store", "persist_dir", persistDir, "error", err)
			return nil, err
		}

		logger.Debug("Created persistent store", "persist_dir", persistDir)

		// First, try to load from snapshot
		snapshotData, snapTerm, snapIndex, err := persist.LoadSnapshot()
		if err != nil {
			logger.Error("Failed to load snapshot", "error", err)
			return nil, err
		}

		// If snapshot exists, restore from it
		if snapshotData != nil {
			entryCount := 0
			for key, value := range snapshotData {
				root, _, _ = root.Insert([]byte(key), value)
				entryCount++
			}
			term = snapTerm
			index = snapIndex
			logger.Info("Restored from snapshot",
				"entries", entryCount,
				"term", term,
				"index", index)
		}

		// Then replay WAL entries that occurred after the snapshot
		entries, err := persist.LoadAll()
		if err != nil {
			logger.Error("Failed to load WAL entries", "error", err)
			return nil, err
		}

		// Replay all operations that are newer than the snapshot
		replayedEntries := 0
		for _, entry := range entries {
			if entry.Index > index {
				if entry.Op == opInsert {
					root, _, _ = root.Insert(entry.Key, entry.Value)
					replayedEntries++
				}
				// Update term and index from each entry to get the latest values
				if entry.Term > term {
					term = entry.Term
				}
				if entry.Index > index {
					index = entry.Index
				}
			}
		}

		if replayedEntries > 0 {
			logger.Info("Replayed WAL entries",
				"entries", replayedEntries,
				"final_term", term,
				"final_index", index)
		}

		// Get the final term and index from persistence layer
		// This handles cases where term-only updates occurred
		persistTerm, persistIndex := persist.GetLastState()
		if persistTerm > term {
			term = persistTerm
		}
		if persistIndex > index {
			index = persistIndex
		}
	}

	tree := &Tree{
		root:     root,
		perfs:    ring.New(10),
		persist:  persist,
		term:     term,
		logIndex: index,
		logger:   logger,
	}

	logger.Info("Tree creation complete",
		"has_persistence", persist != nil,
		"term", term,
		"log_index", index)

	return tree, nil
}

/*
ReplayWAL replays the write-ahead log to restore the tree's state.
*/
func (tree *Tree) ReplayWAL() error {
	// Load from snapshot first
	snapshotData, term, index, err := tree.persist.LoadSnapshot()
	if err != nil {
		return err
	}
	if snapshotData != nil {
		for key, value := range snapshotData {
			tree.root, _, _ = tree.root.Insert([]byte(key), value)
		}
		tree.term = term
		tree.logIndex = index
		tree.logger.Info("Restored from snapshot", "term", term, "index", index, "entries", len(snapshotData))
	}

	// Replay WAL
	entries, err := tree.persist.LoadAll()
	if err != nil {
		return err
	}

	replayedCount := 0
	for _, entry := range entries {
		if entry.Index <= tree.logIndex {
			continue // Skip entries already covered by snapshot
		}
		switch entry.Op {
		case opInsert:
			tree.root, _, _ = tree.root.Insert(entry.Key, entry.Value)
			tree.term = entry.Term
			tree.logIndex = entry.Index
			replayedCount++
		case opSnapshot:
			// This is just a term/index marker in the WAL after a snapshot
			if entry.Term > tree.term {
				tree.term = entry.Term
			}
			if entry.Index > tree.logIndex {
				tree.logIndex = entry.Index
			}
		}
	}
	if replayedCount > 0 {
		tree.logger.Info("Replayed WAL entries", "entries", replayedCount, "final_term", tree.term, "final_index", tree.logIndex)
	}

	return nil
}

/*
Seek performs a prefix-based search in the tree, finding the first value whose key
is greater than or equal to the provided key in lexicographical order.
Returns the value and true if found, or nil and false if no such key exists.
*/
func (tree *Tree) Seek(key []byte) ([]byte, bool) {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	t := time.Now()

	it := tree.root.Root().Iterator()
	it.SeekLowerBound(key)

	for k, v, ok := it.Next(); ok; k, v, ok = it.Next() {
		if bytes.Compare(k, key) >= 0 {
			// record perf before returning
			tree.perfs.Value = time.Since(t).Nanoseconds()
			tree.perfs = tree.perfs.Next()
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
	var updated bool
	tree.root, _, updated = tree.root.Insert(key, value)
	// Treat both new and replaced values as a successful update for API semantics
	tree.updated = true

	if updated || tree.updated {
		tree.logIndex++
		// Log to WAL if persistence is enabled
		if tree.persist != nil {
			if err := tree.persist.LogInsert(key, value, tree.term, tree.logIndex); err != nil {
				tree.logger.Error("Failed to log insert to WAL",
					"key_size", len(key),
					"value_size", len(value),
					"term", tree.term,
					"log_index", tree.logIndex,
					"error", err)
			} else {
				tree.logger.Debug("Logged insert to WAL",
					"key_size", len(key),
					"value_size", len(value),
					"term", tree.term,
					"log_index", tree.logIndex)
			}
		}
	}

	duration := time.Since(t)
	tree.perfs.Value = duration.Nanoseconds()
	tree.perfs = tree.perfs.Next()

	tree.logger.Debug("Insert operation complete",
		"key_size", len(key),
		"value_size", len(value),
		"updated", updated,
		"duration", duration,
		"log_index", tree.logIndex)

	return tree, true
}

/*
Get retrieves the value associated with the given key.
Returns the value and true if the key exists, or nil and false if it doesn't.
*/
func (tree *Tree) Get(key []byte) ([]byte, bool) {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	t := time.Now()
	v, ok := tree.root.Get(key)
	tree.perfs.Value = time.Since(t).Nanoseconds()
	tree.perfs = tree.perfs.Next()
	return v, ok
}

/*
AVG returns the average performance of the tree in nanoseconds.
This method is thread-safe and handles nil values and empty data sets.
*/
func (tree *Tree) AVG() int64 {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	var sum int64
	var count int64

	tree.perfs.Do(func(v any) {
		if v == nil {
			return
		}
		if n, ok := v.(int64); ok {
			sum += n
			count++
		}
	})

	if count == 0 {
		return 0
	}
	return sum / count
}

/*
Close closes the tree and persists any remaining data.
*/
func (tree *Tree) Close() error {
	tree.logger.Info("Closing tree", "has_persistence", tree.persist != nil)

	if tree.persist != nil {
		if err := tree.persist.Close(); err != nil {
			tree.logger.Error("Failed to close persistent store", "error", err)
			return err
		}
		tree.logger.Info("Persistent store closed successfully")
	}

	tree.logger.Info("Tree closed successfully")
	return nil
}

// UpdateTerm updates the current term number and persists it
func (tree *Tree) UpdateTerm(term uint64) {
	tree.mu.Lock()
	defer tree.mu.Unlock()

	oldTerm := tree.term
	tree.term = term

	if tree.persist != nil {
		if err := tree.persist.UpdateTerm(term); err != nil {
			tree.logger.Error("Failed to persist term update",
				"old_term", oldTerm,
				"new_term", term,
				"error", err)
		} else {
			tree.logger.Debug("Term updated and persisted",
				"old_term", oldTerm,
				"new_term", term)
		}
	} else {
		tree.logger.Debug("Term updated (no persistence)",
			"old_term", oldTerm,
			"new_term", term)
	}
}

// GetLogState returns the current term and log index
func (tree *Tree) GetLogState() (term, index uint64) {
	tree.mu.RLock()
	defer tree.mu.RUnlock()
	return tree.term, tree.logIndex
}

// CreateSnapshot creates a snapshot of the current tree state
func (tree *Tree) CreateSnapshot() error {
	tree.mu.RLock()
	defer tree.mu.RUnlock()

	if tree.persist != nil {
		tree.logger.Info("Creating snapshot",
			"term", tree.term,
			"log_index", tree.logIndex)

		start := time.Now()
		if err := tree.persist.CreateSnapshotWithTree(tree); err != nil {
			tree.logger.Error("Failed to create snapshot",
				"term", tree.term,
				"log_index", tree.logIndex,
				"error", err)
			return err
		}

		duration := time.Since(start)
		tree.logger.Info("Snapshot created successfully",
			"term", tree.term,
			"log_index", tree.logIndex,
			"duration", duration)
	}
	return nil
}
