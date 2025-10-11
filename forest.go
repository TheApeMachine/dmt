package dmt

import (
	"context"
	"fmt"
	"sync"
	"time"
)

/*
Forest manages a single Tree instance for distributed key-value operations.
It provides a consistent interface for tree operations while integrating with
the distributed consensus protocol. The Forest abstraction has been simplified
from managing multiple trees to a single tree per node, aligning with Raft
consensus principles and eliminating unnecessary synchronization complexity.
*/
type Forest struct {
	tree *Tree
	mu   sync.RWMutex
	// Channel to signal new updates that need synchronization
	updates chan struct{}
	// Context for controlling background sync
	ctx    context.Context
	cancel context.CancelFunc
	// Network node for distributed operation
	network *NetworkNode
	// Logger for structured logging
	logger Logger
	// Error handler for comprehensive error handling
	errorHandler *ErrorHandler
}

// ForestConfig holds configuration for creating a new Forest
type ForestConfig struct {
	// Directory for persistence
	PersistDir string
	// Network configuration
	Network *NetworkConfig
	// Logger for structured logging
	Logger Logger
	// Node ID for logging context
	NodeID string
}

/*
NewForest creates and returns a new Forest instance with a single tree.
The forest manages one tree per node, aligning with distributed consensus
principles. A background goroutine is started to handle update notifications
for synchronization triggers.
*/
func NewForest(config ForestConfig) (*Forest, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Initialize logger
	logger := config.Logger
	if logger == nil {
		logger = DefaultLogger(config.NodeID)
	}

	// Initialize error handler
	metrics := NewMetrics()
	errorHandler := NewErrorHandler(logger, metrics, config.NodeID)

	f := &Forest{
		updates:      make(chan struct{}, 1), // Buffered channel to prevent blocking
		ctx:          ctx,
		cancel:       cancel,
		logger:       logger,
		errorHandler: errorHandler,
	}

	logger.Info("Creating new forest",
		"persist_dir", config.PersistDir,
		"has_network_config", config.Network != nil,
		"node_id", config.NodeID)

	// Create single tree with persistence if directory provided
	if config.PersistDir != "" {
		tree, err := NewTree(config.PersistDir)
		if err != nil {
			cancel()
			dmtErr, _ := errorHandler.HandleError(err, "tree_creation", map[string]interface{}{
				"persist_dir": config.PersistDir,
			})
			return nil, fmt.Errorf("failed to create tree: %w", dmtErr)
		}
		f.tree = tree
		logger.Info("Created tree with persistence", "persist_dir", config.PersistDir)
	} else {
		// Create in-memory tree if no persistence directory
		tree, err := NewTree("")
		if err != nil {
			cancel()
			dmtErr, _ := errorHandler.HandleError(err, "tree_creation", nil)
			return nil, fmt.Errorf("failed to create in-memory tree: %w", dmtErr)
		}
		f.tree = tree
		logger.Info("Created in-memory tree")
	}

	// Initialize network node if network config provided
	if config.Network != nil {
		network, err := NewNetworkNode(*config.Network, f)
		if err != nil {
			cancel()
			dmtErr, _ := errorHandler.HandleError(err, "network_creation", map[string]interface{}{
				"listen_addr": config.Network.ListenAddr,
				"peer_count":  len(config.Network.PeerAddrs),
			})
			return nil, fmt.Errorf("failed to create network node: %w", dmtErr)
		}
		f.network = network
		logger.Info("Created network node",
			"listen_addr", config.Network.ListenAddr,
			"peer_count", len(config.Network.PeerAddrs))
	}

	go f.syncLoop()
	logger.Info("Forest initialization complete", "has_tree", f.tree != nil)
	return f, nil
}

/*
Close stops the background synchronization goroutine and cleans up resources.
Should be called when the Forest is no longer needed.
*/
func (f *Forest) Close() {
	f.logger.Info("Closing forest", "has_tree", f.tree != nil)

	// Cancel context to stop background goroutines
	if f.cancel != nil {
		f.cancel()
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// Close network node if it exists
	if f.network != nil {
		if err := f.network.Close(); err != nil {
			dmtErr, _ := f.errorHandler.HandleError(err, "network_close", nil)
			f.logger.Error("Failed to close network node", "error", dmtErr)
		} else {
			f.logger.Info("Network node closed successfully")
		}
	}

	// Close the tree
	if f.tree != nil {
		if err := f.tree.Close(); err != nil {
			dmtErr, _ := f.errorHandler.HandleError(err, "tree_close", nil)
			f.logger.Error("Failed to close tree", "error", dmtErr)
		} else {
			f.logger.Debug("Tree closed successfully")
		}
	}

	// Drain the updates channel to prevent goroutine leaks
	// The syncLoop should have already exited due to context cancellation
	select {
	case <-f.updates:
		f.logger.Debug("Drained pending update notification")
	default:
		// No pending updates
	}

	f.logger.Info("Forest closed successfully")
}

/*
syncLoop runs in the background and handles update notifications.
It triggers network synchronization when new data is inserted,
enabling immediate sync instead of only periodic sync.
*/
func (f *Forest) syncLoop() {
	ticker := time.NewTicker(5 * time.Second) // Periodic maintenance every 5 seconds
	defer ticker.Stop()

	for {
		select {
		case <-f.ctx.Done():
			return
		case <-f.updates: // Triggered by new updates
			f.triggerNetworkSync()
		case <-ticker.C: // Periodic maintenance
			f.performMaintenance()
		}
	}
}

/*
triggerNetworkSync triggers network synchronization when new data is available.
This enables immediate sync with peers instead of waiting for periodic sync.
*/
func (forest *Forest) triggerNetworkSync() {
	forest.logger.Debug("Triggering network sync due to update")

	// If we have a network node, trigger immediate sync with peers
	if forest.network != nil {
		go forest.network.syncWithPeers()
	}
}

/*
performMaintenance performs periodic maintenance tasks for the forest.
It triggers cleanup operations and manages resource usage.
*/
func (forest *Forest) performMaintenance() {
	forest.logger.Debug("Performing periodic maintenance")

	forest.mu.RLock()
	tree := forest.tree
	forest.mu.RUnlock()

	if tree != nil && tree.persist != nil {
		// Create snapshot every 1000 operations to manage WAL size
		_, logIndex := tree.GetLogState()
		if logIndex > 0 && logIndex%1000 == 0 {
			if err := tree.CreateSnapshot(); err != nil {
				dmtErr, _ := forest.errorHandler.HandleError(err, "snapshot_creation", map[string]interface{}{
					"log_index": logIndex,
				})
				forest.logger.Error("Failed to create snapshot", "error", dmtErr)
			} else {
				forest.logger.Debug("Created snapshot", "log_index", logIndex)
			}
		}
	}
}

/*
GetTree returns the single tree managed by this forest.
This method provides access to the underlying tree for operations
that need direct tree access.
*/
func (forest *Forest) GetTree() *Tree {
	forest.mu.RLock()
	defer forest.mu.RUnlock()
	return forest.tree
}

/*
getTree returns the single tree managed by this forest.
This is a simplified version of the previous getFastestTree method
that now simply returns the single tree instance.
*/
func (forest *Forest) getTree() *Tree {
	forest.mu.RLock()
	defer forest.mu.RUnlock()
	return forest.tree
}

/*
Get retrieves a value from the forest's tree.
Returns the value and true if the key exists, or nil and false if it doesn't
or if the forest has no tree.
*/
func (forest *Forest) Get(key []byte) ([]byte, bool) {
	tree := forest.getTree()
	if tree == nil {
		return nil, false
	}
	return tree.Get(key)
}

/*
Seek performs a prefix-based search using the forest's tree.
It finds the first value whose key is greater than or equal to the provided key
in lexicographical order. Returns the value and true if found, or nil and false
if no such key exists or if the forest has no tree.
*/
func (forest *Forest) Seek(key []byte) ([]byte, bool) {
	tree := forest.getTree()
	if tree == nil {
		return nil, false
	}
	return tree.Seek(key)
}

/*
Insert adds or updates a key-value pair in the forest's tree.
In a distributed setting, only the leader node can accept writes. Non-leader
nodes will return an error directing clients to the current leader.

For leaders, the operation follows Raft protocol:
1. Append entry to local log
2. Replicate to followers via AppendEntries RPC
3. Apply to state machine once committed by majority

For non-networked mode, the operation is applied directly to the local tree.
*/
func (forest *Forest) Insert(key []byte, value []byte) error {
	forest.logger.Debug("Insert operation requested",
		"key_size", len(key),
		"value_size", len(value),
		"has_network", forest.network != nil)

	// Check if this node is the leader before accepting writes
	if forest.network != nil && forest.network.election != nil {
		if !forest.network.election.isLeader() {
			// Return error indicating this node is not the leader
			err := fmt.Errorf("not leader: writes must be sent to the leader node")
			dmtErr := NewLogicError("insert", err, map[string]interface{}{
				"key_size":   len(key),
				"value_size": len(value),
				"is_leader":  false,
			})
			forest.logger.Warn("Insert rejected: not leader", "error", dmtErr)
			return dmtErr
		}

		forest.logger.Debug("Leader processing insert", "key_size", len(key), "value_size", len(value))

		// Leader: append to log first, then replicate
		logIndex := forest.network.election.appendLogEntry(key, value)

		// Replicate to followers and commit if quorum achieved
		if err := forest.network.ReplicateLogEntry(key, value, logIndex); err != nil {
			dmtErr, _ := forest.errorHandler.HandleError(err, "log_replication", map[string]interface{}{
				"key_size":   len(key),
				"value_size": len(value),
				"log_index":  logIndex,
			})
			forest.logger.Error("Failed to replicate log entry", "log_index", logIndex, "error", dmtErr)
			return fmt.Errorf("failed to replicate log entry: %w", dmtErr)
		}

		forest.logger.Info("Insert operation committed",
			"log_index", logIndex,
			"key_size", len(key),
			"value_size", len(value))

		// Trigger network sync notification
		select {
		case forest.updates <- struct{}{}:
		default:
		}

		// Entry has been committed and will be applied by the apply loop
		return nil
	}

	// Non-networked mode: apply directly to local tree
	forest.mu.Lock()
	tree := forest.tree
	forest.mu.Unlock()

	if tree == nil {
		err := fmt.Errorf("no tree available for insert")
		dmtErr := NewLogicError("insert", err, map[string]interface{}{
			"key_size":   len(key),
			"value_size": len(value),
		})
		forest.logger.Error("Insert failed: no tree", "error", dmtErr)
		return dmtErr
	}

	forest.logger.Debug("Non-networked insert")
	if _, updated := tree.Insert(key, value); updated {
		forest.logger.Debug("Updated tree")

		// Trigger update notification for sync
		select {
		case forest.updates <- struct{}{}:
		default:
		}
	}

	forest.logger.Debug("Insert operation complete")
	return nil
}
