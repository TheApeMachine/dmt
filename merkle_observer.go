package dmt

import (
	"context"
	"sync"
)

// MerkleTreeEvent represents an event in the Merkle tree
type MerkleTreeEvent struct {
	Type      string // "insert", "rebuild", "delete"
	Key       []byte
	Value     []byte
	OldHash   []byte // Previous root hash
	NewHash   []byte // New root hash
	Timestamp int64
}

// MerkleTreeObserver defines the interface for tree observers
type MerkleTreeObserver interface {
	OnTreeEvent(ctx context.Context, event MerkleTreeEvent) error
}

// ObservableMerkleTree extends MerkleTree with observer pattern
type ObservableMerkleTree struct {
	*MerkleTree
	observers []MerkleTreeObserver
	mu        sync.RWMutex
}

// NewObservableMerkleTree creates a new observable Merkle tree
func NewObservableMerkleTree() *ObservableMerkleTree {
	return &ObservableMerkleTree{
		MerkleTree: NewMerkleTree(),
		observers:  make([]MerkleTreeObserver, 0),
	}
}

// AddObserver adds an observer to the tree
func (omt *ObservableMerkleTree) AddObserver(observer MerkleTreeObserver) {
	omt.mu.Lock()
	defer omt.mu.Unlock()
	omt.observers = append(omt.observers, observer)
}

// RemoveObserver removes an observer from the tree
func (omt *ObservableMerkleTree) RemoveObserver(observer MerkleTreeObserver) {
	omt.mu.Lock()
	defer omt.mu.Unlock()

	for i, obs := range omt.observers {
		if obs == observer {
			omt.observers = append(omt.observers[:i], omt.observers[i+1:]...)
			break
		}
	}
}

// notifyObservers notifies all observers of an event
func (omt *ObservableMerkleTree) notifyObservers(ctx context.Context, event MerkleTreeEvent) {
	omt.mu.RLock()
	observers := make([]MerkleTreeObserver, len(omt.observers))
	copy(observers, omt.observers)
	omt.mu.RUnlock()

	// Notify observers concurrently
	var wg sync.WaitGroup
	for _, observer := range observers {
		wg.Add(1)
		go func(obs MerkleTreeObserver) {
			defer wg.Done()
			if err := obs.OnTreeEvent(ctx, event); err != nil {
				// Log error but don't fail the operation
				// In production, you might want to use a proper logger
			}
		}(observer)
	}
	wg.Wait()
}

// Insert overrides the base Insert to notify observers
func (omt *ObservableMerkleTree) Insert(key, value []byte) error {
	oldHash := omt.getRootHash()

	err := omt.MerkleTree.Insert(key, value)
	if err != nil {
		return err
	}

	newHash := omt.getRootHash()

	event := MerkleTreeEvent{
		Type:      "insert",
		Key:       append([]byte(nil), key...),
		Value:     append([]byte(nil), value...),
		OldHash:   oldHash,
		NewHash:   newHash,
		Timestamp: getCurrentTimestamp(),
	}

	go omt.notifyObservers(context.Background(), event)
	return nil
}

// Rebuild overrides the base Rebuild to notify observers
func (omt *ObservableMerkleTree) Rebuild() {
	oldHash := omt.getRootHash()

	omt.MerkleTree.Rebuild()

	newHash := omt.getRootHash()

	event := MerkleTreeEvent{
		Type:      "rebuild",
		OldHash:   oldHash,
		NewHash:   newHash,
		Timestamp: getCurrentTimestamp(),
	}

	go omt.notifyObservers(context.Background(), event)
}

// getRootHash safely gets the current root hash
func (omt *ObservableMerkleTree) getRootHash() []byte {
	omt.MerkleTree.mu.RLock()
	defer omt.MerkleTree.mu.RUnlock()

	if omt.MerkleTree.Root != nil {
		return append([]byte(nil), omt.MerkleTree.Root.Hash...)
	}
	return nil
}

// getCurrentTimestamp returns current timestamp (placeholder)
func getCurrentTimestamp() int64 {
	return 0 // In real implementation, use time.Now().UnixNano()
}

// NetworkSyncObserver implements observer for network synchronization
type NetworkSyncObserver struct {
	nodeID string
	peers  []string
}

// NewNetworkSyncObserver creates a new network sync observer
func NewNetworkSyncObserver(nodeID string, peers []string) *NetworkSyncObserver {
	return &NetworkSyncObserver{
		nodeID: nodeID,
		peers:  peers,
	}
}

// OnTreeEvent handles tree events for network synchronization
func (nso *NetworkSyncObserver) OnTreeEvent(ctx context.Context, event MerkleTreeEvent) error {
	// In a real implementation, this would trigger network sync
	// For now, just log the event
	switch event.Type {
	case "insert":
		// Trigger sync with peers about new insertion
	case "rebuild":
		// Notify peers about tree rebuild
	}
	return nil
}

// MetricsObserver implements observer for metrics collection
type MetricsObserver struct {
	metrics *Metrics
}

// NewMetricsObserver creates a new metrics observer
func NewMetricsObserver(metrics *Metrics) *MetricsObserver {
	return &MetricsObserver{
		metrics: metrics,
	}
}

// OnTreeEvent handles tree events for metrics collection
func (mo *MetricsObserver) OnTreeEvent(ctx context.Context, event MerkleTreeEvent) error {
	switch event.Type {
	case "insert":
		// Record insert metrics
		if mo.metrics != nil {
			// mo.metrics.RecordMerkleInsert()
		}
	case "rebuild":
		// Record rebuild metrics
		if mo.metrics != nil {
			// mo.metrics.RecordMerkleRebuild()
		}
	}
	return nil
}
