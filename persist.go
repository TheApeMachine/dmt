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
	writeMutex sync.Mutex
	syncChan   chan struct{}
	lastIndex  uint64
	lastTerm   uint64
	// Snapshot control
	snapCount uint64    // Number of entries before triggering snapshot
	lastSnap  time.Time // Time of last snapshot
	snapMutex sync.RWMutex
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
		syncChan:  make(chan struct{}, 100),
		snapCount: 1000, // Create snapshot every 1000 entries
	}

	// Start background syncing
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

	// Signal for background sync
	select {
	case ps.syncChan <- struct{}{}:
	default:
	}

	// Check if snapshot needed
	if index%ps.snapCount == 0 {
		go ps.createSnapshot()
	}

	return nil
}

/*
backgroundSync periodically flushes the WAL to disk.
It listens on the sync channel and ensures that buffered writes are
persisted to stable storage.
*/
func (ps *PersistentStore) backgroundSync() {
	for range ps.syncChan {
		ps.writeMutex.Lock()
		ps.walWriter.Flush()
		ps.walFile.Sync()
		ps.writeMutex.Unlock()
	}
}

/*
Close closes the persistent store, ensuring all buffered data is
written to disk and resources are properly released.
*/
func (ps *PersistentStore) Close() error {
	close(ps.syncChan)
	ps.writeMutex.Lock()
	defer ps.writeMutex.Unlock()

	if err := ps.walWriter.Flush(); err != nil {
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

// createSnapshot creates a new snapshot and truncates the WAL
func (ps *PersistentStore) createSnapshot() error {
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

	// Write snapshot metadata
	if err := binary.Write(file, binary.LittleEndian, ps.lastTerm); err != nil {
		return err
	}
	if err := binary.Write(file, binary.LittleEndian, ps.lastIndex); err != nil {
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
