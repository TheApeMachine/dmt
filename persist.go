/*
package dmt implements persistence functionality for the radix tree.
The persistence layer uses a Write-Ahead Log (WAL) to ensure data durability
and provides mechanisms for recovery in case of failures.
*/
package dmt

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

/*
Operation types for the WAL. These define the possible operations that can be
recorded in the write-ahead log for persistence and recovery.
*/
const (
	opInsert byte = iota
	opDelete
	opSnapshot
)

/*
WALEntry represents a single write-ahead log entry. Each entry contains the
operation type and the associated key-value pair, allowing for replay during
recovery operations.
*/
type WALEntry struct {
	Op    byte
	Term  uint64
	Index uint64
	Key   []byte
	Value []byte
}

/*
PersistentStore handles the persistence layer for the radix tree.
It manages write-ahead logging and provides mechanisms for durable storage
and recovery of tree data.
*/
type PersistentStore struct {
	walFile    *os.File
	walWriter  *bufio.Writer
	walPath    string
	snapPath   string
	dir        string // Directory for WAL and snapshots
	writeMutex sync.Mutex
	syncChan   chan struct{}
	lastIndex  uint64
	lastTerm   uint64
	// Snapshot control
	snapCount uint64    // Number of entries before triggering snapshot
	lastSnap  time.Time // Time of last snapshot
	snapMutex sync.RWMutex

	// Concurrency control
	closedMutex sync.RWMutex
	closed      bool
	wg          sync.WaitGroup
}

/*
NewPersistentStore creates a new persistent store instance.
It initializes the WAL file and sets up background syncing to ensure
data durability. The store will create necessary directories if they
don't exist.
*/
func NewPersistentStore(dir string) (*PersistentStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	walPath := filepath.Join(dir, "wal.log")
	snapPath := filepath.Join(dir, "snapshot")

	walFile, err := os.OpenFile(walPath, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	ps := &PersistentStore{
		walFile:   walFile,
		walWriter: bufio.NewWriter(walFile),
		walPath:   walPath,
		snapPath:  snapPath,
		dir:       dir,
		syncChan:  make(chan struct{}, 100),
		snapCount: 1000, // Create snapshot every 1000 entries
	}

	// Start background syncing
	ps.wg.Add(1)
	go ps.backgroundSync()

	// Load last term/index from WAL
	if err := ps.loadLastState(); err != nil {
		return nil, err
	}

	return ps, nil
}

/*
LogInsert asynchronously logs an insert operation to the WAL.
It writes the operation type, key, and value to the WAL buffer and
signals the background sync goroutine to flush to disk.
*/
func (ps *PersistentStore) LogInsert(key, value []byte, term, index uint64) error {
	ps.closedMutex.RLock()
	defer ps.closedMutex.RUnlock()

	if ps.closed {
		return fmt.Errorf("persistent store is closed")
	}

	ps.writeMutex.Lock()
	defer ps.writeMutex.Unlock()

	// Write entry header
	if err := ps.walWriter.WriteByte(opInsert); err != nil {
		return err
	}

	// Write term and index
	if err := binary.Write(ps.walWriter, binary.LittleEndian, term); err != nil {
		return err
	}
	if err := binary.Write(ps.walWriter, binary.LittleEndian, index); err != nil {
		return err
	}

	// Write key length and key
	if err := binary.Write(ps.walWriter, binary.LittleEndian, uint32(len(key))); err != nil {
		return err
	}
	if _, err := ps.walWriter.Write(key); err != nil {
		return err
	}

	// Write value length and value
	if err := binary.Write(ps.walWriter, binary.LittleEndian, uint32(len(value))); err != nil {
		return err
	}
	if _, err := ps.walWriter.Write(value); err != nil {
		return err
	}

	// Update last term/index
	ps.lastTerm = term
	ps.lastIndex = index

	// Flush immediately for synchronous writes
	if err := ps.walWriter.Flush(); err != nil {
		return err
	}
	if err := ps.walFile.Sync(); err != nil {
		return err
	}

	// Check if snapshot needed
	if index%ps.snapCount == 0 {
		// Note: createSnapshot will be called externally with tree data
		// This is just a placeholder for the snapshot trigger logic
	}

	return nil
}

/*
backgroundSync periodically flushes the WAL to disk.
It listens on the sync channel and ensures that buffered writes are
persisted to stable storage.
*/
func (ps *PersistentStore) backgroundSync() {
	// This function is now a no-op as writes are synchronous.
	// The waitgroup logic remains to prevent breaking changes in Close().
	defer ps.wg.Done()
	for {
		_, ok := <-ps.syncChan
		if !ok {
			return
		}
	}
}

/*
Close closes the persistent store, ensuring all buffered data is
written to disk and resources are properly released.
*/
func (ps *PersistentStore) Close() error {
	ps.closedMutex.Lock()
	if ps.closed {
		ps.closedMutex.Unlock()
		return nil
	}
	ps.closed = true
	ps.closedMutex.Unlock()

	// Signal the background worker to stop and wait for it
	close(ps.syncChan)
	ps.wg.Wait()

	// Final flush and close under lock
	ps.writeMutex.Lock()
	defer ps.writeMutex.Unlock()

	if err := ps.walWriter.Flush(); err != nil {
		ps.walFile.Close() // Best effort
		return err
	}
	return ps.walFile.Close()
}

// loadLastState reads the WAL to find the last term and index
func (ps *PersistentStore) loadLastState() error {
	file, err := os.Open(ps.walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var lastTerm, lastIndex uint64
	var done bool

	for !done {
		// Read operation type
		op, err := reader.ReadByte()
		if err != nil {
			break
		}

		switch op {
		case opInsert:
			// Read term and index
			if err := binary.Read(reader, binary.LittleEndian, &lastTerm); err != nil {
				done = true
				continue
			}
			if err := binary.Read(reader, binary.LittleEndian, &lastIndex); err != nil {
				done = true
				continue
			}

			// Skip key and value
			var length uint32
			if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
				done = true
				continue
			}
			if _, err := reader.Discard(int(length)); err != nil {
				done = true
				continue
			}
			if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
				done = true
				continue
			}
			if _, err := reader.Discard(int(length)); err != nil {
				done = true
				continue
			}

		case opSnapshot:
			// Read snapshot metadata
			if err := binary.Read(reader, binary.LittleEndian, &lastTerm); err != nil {
				done = true
				continue
			}
			if err := binary.Read(reader, binary.LittleEndian, &lastIndex); err != nil {
				done = true
				continue
			}
		}
	}

	ps.lastTerm = lastTerm
	ps.lastIndex = lastIndex
	return nil
}

// CreateSnapshotWithTree creates a new snapshot with complete tree state and truncates the WAL
func (ps *PersistentStore) CreateSnapshotWithTree(tree *Tree) error {
	ps.snapMutex.Lock()
	defer ps.snapMutex.Unlock()

	// Ensure minimum time between snapshots
	if time.Since(ps.lastSnap) < time.Minute {
		return nil
	}

	// Create snapshot directory if not exists
	if err := os.MkdirAll(ps.snapPath, 0755); err != nil {
		return err
	}

	// Create new snapshot file
	snapFile := filepath.Join(ps.snapPath, fmt.Sprintf("snapshot-%d-%d", ps.lastTerm, ps.lastIndex))
	file, err := os.Create(snapFile)
	if err != nil {
		return err
	}
	defer file.Close()

	// Count entries first to write accurate header
	var entryCount uint64
	if tree != nil {
		it := tree.root.Root().Iterator()
		for _, _, ok := it.Next(); ok; _, _, ok = it.Next() {
			entryCount++
		}
	}

	// Write snapshot header with metadata and entry count
	if err := binary.Write(file, binary.LittleEndian, ps.lastTerm); err != nil {
		return err
	}
	if err := binary.Write(file, binary.LittleEndian, ps.lastIndex); err != nil {
		return err
	}
	if err := binary.Write(file, binary.LittleEndian, entryCount); err != nil {
		return err
	}

	// Write all key-value pairs from the tree
	if tree != nil {
		it := tree.root.Root().Iterator()
		for key, value, ok := it.Next(); ok; key, value, ok = it.Next() {
			// Write key length and key
			if err := binary.Write(file, binary.LittleEndian, uint32(len(key))); err != nil {
				return err
			}
			if _, err := file.Write(key); err != nil {
				return err
			}

			// Write value length and value
			if err := binary.Write(file, binary.LittleEndian, uint32(len(value))); err != nil {
				return err
			}
			if _, err := file.Write(value); err != nil {
				return err
			}
		}
	}

	// Ensure data is written to disk
	if err := file.Sync(); err != nil {
		return err
	}

	// Log snapshot creation in WAL
	ps.writeMutex.Lock()
	defer ps.writeMutex.Unlock()

	if err := ps.walWriter.WriteByte(opSnapshot); err != nil {
		return err
	}
	if err := binary.Write(ps.walWriter, binary.LittleEndian, ps.lastTerm); err != nil {
		return err
	}
	if err := binary.Write(ps.walWriter, binary.LittleEndian, ps.lastIndex); err != nil {
		return err
	}

	// Truncate WAL
	if err := ps.truncateWAL(); err != nil {
		return err
	}

	ps.lastSnap = time.Now()
	return nil
}

// truncateWAL creates a new WAL file with just the snapshot entry
func (ps *PersistentStore) truncateWAL() error {
	// Create new WAL file
	newPath := ps.walPath + ".new"
	newFile, err := os.Create(newPath)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(newFile)

	// Write snapshot entry
	if err := writer.WriteByte(opSnapshot); err != nil {
		newFile.Close()
		return err
	}
	if err := binary.Write(writer, binary.LittleEndian, ps.lastTerm); err != nil {
		newFile.Close()
		return err
	}
	if err := binary.Write(writer, binary.LittleEndian, ps.lastIndex); err != nil {
		newFile.Close()
		return err
	}

	// Ensure all data is written
	if err := writer.Flush(); err != nil {
		newFile.Close()
		return err
	}
	if err := newFile.Sync(); err != nil {
		newFile.Close()
		return err
	}
	newFile.Close()

	// Replace old WAL with new one
	if err := os.Rename(newPath, ps.walPath); err != nil {
		return err
	}

	// Update file handles
	ps.walFile.Close()
	ps.walFile, err = os.OpenFile(ps.walPath, os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	ps.walWriter = bufio.NewWriter(ps.walFile)

	return nil
}

// GetLastState returns the last recorded term and index
func (ps *PersistentStore) GetLastState() (term, index uint64) {
	ps.writeMutex.Lock()
	defer ps.writeMutex.Unlock()
	return ps.lastTerm, ps.lastIndex
}

// UpdateTerm persists the current term to the WAL without adding a KV entry.
// It writes a snapshot-style record to track the latest term while preserving index.
func (ps *PersistentStore) UpdateTerm(term uint64) error {
	ps.writeMutex.Lock()
	defer ps.writeMutex.Unlock()

	// Check if closed under write lock to avoid races with Close()
	ps.closedMutex.RLock()
	if ps.closed {
		ps.closedMutex.RUnlock()
		return nil
	}
	ps.closedMutex.RUnlock()

	ps.lastTerm = term
	// Write a snapshot record with new term and current index
	if err := ps.walWriter.WriteByte(opSnapshot); err != nil {
		return err
	}
	if err := binary.Write(ps.walWriter, binary.LittleEndian, ps.lastTerm); err != nil {
		return err
	}
	if err := binary.Write(ps.walWriter, binary.LittleEndian, ps.lastIndex); err != nil {
		return err
	}
	// Flush immediately to ensure term updates are durable
	if err := ps.walWriter.Flush(); err != nil {
		return err
	}
	return ps.walFile.Sync()
}

// LoadSnapshot loads the most recent snapshot and returns the key-value pairs
func (ps *PersistentStore) LoadSnapshot() (map[string][]byte, uint64, uint64, error) {
	// Find the most recent snapshot file
	snapFiles, err := filepath.Glob(filepath.Join(ps.snapPath, "snapshot-*"))
	if err != nil {
		return nil, 0, 0, err
	}

	if len(snapFiles) == 0 {
		return nil, 0, 0, nil // No snapshot exists
	}

	// Use the most recent snapshot (they're named with term-index, so lexicographic sort works)
	var latestSnap string
	for _, snapFile := range snapFiles {
		if snapFile > latestSnap {
			latestSnap = snapFile
		}
	}

	file, err := os.Open(latestSnap)
	if err != nil {
		return nil, 0, 0, err
	}
	defer file.Close()

	// Read snapshot header
	var term, index, entryCount uint64
	if err := binary.Read(file, binary.LittleEndian, &term); err != nil {
		return nil, 0, 0, err
	}
	if err := binary.Read(file, binary.LittleEndian, &index); err != nil {
		return nil, 0, 0, err
	}
	if err := binary.Read(file, binary.LittleEndian, &entryCount); err != nil {
		return nil, 0, 0, err
	}

	// Read all key-value pairs
	data := make(map[string][]byte)
	for i := uint64(0); i < entryCount; i++ {
		// Read key
		var keyLen uint32
		if err := binary.Read(file, binary.LittleEndian, &keyLen); err != nil {
			return nil, 0, 0, err
		}
		key := make([]byte, keyLen)
		if _, err := file.Read(key); err != nil {
			return nil, 0, 0, err
		}

		// Read value
		var valueLen uint32
		if err := binary.Read(file, binary.LittleEndian, &valueLen); err != nil {
			return nil, 0, 0, err
		}
		value := make([]byte, valueLen)
		if _, err := file.Read(value); err != nil {
			return nil, 0, 0, err
		}

		data[string(key)] = value
	}

	return data, term, index, nil
}

// LoadAll returns all key/value entries recorded in the WAL in write order.
// It is used for state recovery to rebuild in-memory structures.
func (ps *PersistentStore) LoadAll() ([]WALEntry, error) {
	file, err := os.Open(ps.walPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	entries := make([]WALEntry, 0)
	for {
		op, err := reader.ReadByte()
		if err != nil {
			break
		}
		switch op {
		case opInsert:
			var term uint64
			var index uint64
			if err := binary.Read(reader, binary.LittleEndian, &term); err != nil {
				return entries, nil
			}
			if err := binary.Read(reader, binary.LittleEndian, &index); err != nil {
				return entries, nil
			}
			var klen uint32
			if err := binary.Read(reader, binary.LittleEndian, &klen); err != nil {
				return entries, nil
			}
			key := make([]byte, klen)
			if _, err := reader.Read(key); err != nil {
				return entries, nil
			}
			var vlen uint32
			if err := binary.Read(reader, binary.LittleEndian, &vlen); err != nil {
				return entries, nil
			}
			val := make([]byte, vlen)
			if _, err := reader.Read(val); err != nil {
				return entries, nil
			}
			entries = append(entries, WALEntry{Op: opInsert, Term: term, Index: index, Key: key, Value: val})
		case opSnapshot:
			// Read snapshot metadata and include as term-only entry
			var term uint64
			var index uint64
			if err := binary.Read(reader, binary.LittleEndian, &term); err != nil {
				return entries, nil
			}
			if err := binary.Read(reader, binary.LittleEndian, &index); err != nil {
				return entries, nil
			}
			// Add term-only entry to preserve term updates
			entries = append(entries, WALEntry{Op: opSnapshot, Term: term, Index: index, Key: nil, Value: nil})
		}
	}
	return entries, nil
}
