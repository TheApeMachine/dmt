/*
package dmt implements health monitoring and status endpoints for the distributed key-value store.
It provides health checks, status reporting, and monitoring endpoints for system observability
and operational monitoring of the distributed system.
*/
package dmt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

/*
HealthStatus represents the overall health status of a component.
*/
type HealthStatus string

const (
	// HealthStatusHealthy indicates the component is functioning normally
	HealthStatusHealthy HealthStatus = "healthy"
	// HealthStatusDegraded indicates the component has issues but is still functional
	HealthStatusDegraded HealthStatus = "degraded"
	// HealthStatusUnhealthy indicates the component is not functioning properly
	HealthStatusUnhealthy HealthStatus = "unhealthy"
)

/*
HealthCheck represents a single health check with its status and details.
*/
type HealthCheck struct {
	Name        string                 `json:"name"`
	Status      HealthStatus           `json:"status"`
	Message     string                 `json:"message"`
	LastChecked time.Time              `json:"last_checked"`
	Duration    time.Duration          `json:"duration"`
	Details     map[string]interface{} `json:"details,omitempty"`
}

// Clone returns a deep copy of the HealthCheck, ensuring the returned
// object (and its Details map) can be safely mutated by callers without
// affecting the original internal state.
func (hc *HealthCheck) Clone() *HealthCheck {
	if hc == nil {
		return nil
	}

	clone := &HealthCheck{
		Name:        hc.Name,
		Status:      hc.Status,
		Message:     hc.Message,
		LastChecked: hc.LastChecked,
		Duration:    hc.Duration,
	}

	if hc.Details != nil {
		clone.Details = make(map[string]interface{}, len(hc.Details))
		for k, v := range hc.Details {
			clone.Details[k] = v
		}
	}

	return clone
}

/*
HealthMonitor manages health checks and provides status endpoints.
It runs periodic health checks and maintains the overall system health status.
*/
type HealthMonitor struct {
	checks        map[string]*HealthCheck
	checkFuncs    map[string]func() *HealthCheck
	mutex         sync.RWMutex
	logger        Logger
	metrics       *Metrics
	checkInterval time.Duration
	ctx           context.Context
	cancel        context.CancelFunc
}

/*
HealthMonitorConfig holds configuration for the health monitor.
*/
type HealthMonitorConfig struct {
	CheckInterval time.Duration
	Logger        Logger
	Metrics       *Metrics
}

/*
NewHealthMonitor creates a new health monitor with the provided configuration.
*/
func NewHealthMonitor(config HealthMonitorConfig) *HealthMonitor {
	ctx, cancel := context.WithCancel(context.Background())

	if config.CheckInterval == 0 {
		config.CheckInterval = 30 * time.Second
	}

	// Ensure we always have a non-nil logger to avoid panics where logging is optional
	if config.Logger == nil {
		config.Logger = NewNoOpLogger()
	}

	hm := &HealthMonitor{
		checks:        make(map[string]*HealthCheck),
		checkFuncs:    make(map[string]func() *HealthCheck),
		logger:        config.Logger,
		metrics:       config.Metrics,
		checkInterval: config.CheckInterval,
		ctx:           ctx,
		cancel:        cancel,
	}

	// Start the health check loop
	go hm.healthCheckLoop()

	return hm
}

/*
RegisterCheck registers a health check function with the monitor.
*/
func (hm *HealthMonitor) RegisterCheck(name string, checkFunc func() *HealthCheck) {
	hm.mutex.Lock()
	defer hm.mutex.Unlock()

	hm.checkFuncs[name] = checkFunc
	hm.logger.Info("Registered health check", "check_name", name)
}

/*
healthCheckLoop runs periodic health checks.
*/
func (hm *HealthMonitor) healthCheckLoop() {
	ticker := time.NewTicker(hm.checkInterval)
	defer ticker.Stop()

	// Run initial checks
	hm.runAllChecks()

	for {
		select {
		case <-hm.ctx.Done():
			return
		case <-ticker.C:
			hm.runAllChecks()
		}
	}
}

/*
runAllChecks executes all registered health checks.
*/
func (hm *HealthMonitor) runAllChecks() {
	hm.mutex.Lock()
	checkFuncs := make(map[string]func() *HealthCheck)
	for name, fn := range hm.checkFuncs {
		checkFuncs[name] = fn
	}
	hm.mutex.Unlock()

	for name, checkFunc := range checkFuncs {
		start := time.Now()
		check := checkFunc()
		check.Duration = time.Since(start)
		check.LastChecked = time.Now()

		hm.mutex.Lock()
		hm.checks[name] = check
		hm.mutex.Unlock()

		hm.logger.Debug("Health check completed",
			"check_name", name,
			"status", check.Status,
			"duration", check.Duration,
			"message", check.Message)
	}
}

/*
GetOverallStatus returns the overall system health status.
*/
func (hm *HealthMonitor) GetOverallStatus() HealthStatus {
	hm.mutex.RLock()
	defer hm.mutex.RUnlock()

	if len(hm.checks) == 0 {
		return HealthStatusUnhealthy
	}

	hasUnhealthy := false
	hasDegraded := false

	for _, check := range hm.checks {
		switch check.Status {
		case HealthStatusUnhealthy:
			hasUnhealthy = true
		case HealthStatusDegraded:
			hasDegraded = true
		}
	}

	if hasUnhealthy {
		return HealthStatusUnhealthy
	}
	if hasDegraded {
		return HealthStatusDegraded
	}

	return HealthStatusHealthy
}

/*
GetHealthReport returns a comprehensive health report.
*/
func (hm *HealthMonitor) GetHealthReport() map[string]interface{} {
	hm.mutex.RLock()
	defer hm.mutex.RUnlock()

	checks := make(map[string]*HealthCheck, len(hm.checks))
	for name, check := range hm.checks {
		checks[name] = check.Clone()
	}

	return map[string]interface{}{
		"overall_status": hm.GetOverallStatus(),
		"timestamp":      time.Now(),
		"checks":         checks,
	}
}

/*
Close shuts down the health monitor.

Note: hm.cancel must be non-nil and is set by NewHealthMonitor.
*/
func (hm *HealthMonitor) Close() {
	hm.cancel()
	if hm.logger != nil {
		hm.logger.Info("Health monitor stopped")
	}
}

/*
HTTPHandler returns an HTTP handler for health check endpoints.
*/
func (hm *HealthMonitor) HTTPHandler() http.Handler {
	mux := http.NewServeMux()

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		report := hm.GetHealthReport()
		status := hm.GetOverallStatus()

		// Set HTTP status code based on health
		switch status {
		case HealthStatusHealthy:
			w.WriteHeader(http.StatusOK)
		case HealthStatusDegraded:
			w.WriteHeader(http.StatusOK) // Still OK but with warnings
		case HealthStatusUnhealthy:
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(report)
	})

	// Readiness check endpoint
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		status := hm.GetOverallStatus()

		if status == HealthStatusUnhealthy {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ready":  false,
				"status": status,
			})
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ready":  true,
				"status": status,
			})
		}
	})

	// Liveness check endpoint
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		// Simple liveness check - if we can respond, we're alive
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"alive":     true,
			"timestamp": time.Now(),
		})
	})

	// Metrics endpoint
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if hm.metrics != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(hm.metrics.GetMetrics())
		} else {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "metrics not available",
			})
		}
	})

	return mux
}

/*
CreateNetworkNodeHealthChecks creates standard health checks for a network node.
*/
func CreateNetworkNodeHealthChecks(node *NetworkNode) map[string]func() *HealthCheck {
	return map[string]func() *HealthCheck{
		"peer_connectivity": func() *HealthCheck {
			node.peersMutex.RLock()
			peerCount := len(node.peers)
			configuredPeers := len(node.config.PeerAddrs)
			node.peersMutex.RUnlock()

			status := HealthStatusHealthy
			message := fmt.Sprintf("Connected to %d/%d peers", peerCount, configuredPeers)

			if peerCount == 0 && configuredPeers > 0 {
				status = HealthStatusUnhealthy
				message = "No peer connections available"
			} else if peerCount < configuredPeers/2 {
				status = HealthStatusDegraded
				message = fmt.Sprintf("Only %d/%d peers connected", peerCount, configuredPeers)
			}

			return &HealthCheck{
				Name:    "peer_connectivity",
				Status:  status,
				Message: message,
				Details: map[string]interface{}{
					"connected_peers":  peerCount,
					"configured_peers": configuredPeers,
				},
			}
		},

		"election_status": func() *HealthCheck {
			if node.election == nil {
				return &HealthCheck{
					Name:    "election_status",
					Status:  HealthStatusUnhealthy,
					Message: "Election manager not initialized",
				}
			}

			isLeader := node.election.isLeader()
			currentTerm := node.election.getCurrentTerm()

			status := HealthStatusHealthy
			message := "Election system functioning normally"

			if isLeader {
				message = fmt.Sprintf("Node is leader (term %d)", currentTerm)
			} else {
				message = fmt.Sprintf("Node is follower (term %d)", currentTerm)
			}

			return &HealthCheck{
				Name:    "election_status",
				Status:  status,
				Message: message,
				Details: map[string]interface{}{
					"is_leader":    isLeader,
					"current_term": currentTerm,
				},
			}
		},

		"merkle_tree_status": func() *HealthCheck {
			if node.merkleTree == nil {
				return &HealthCheck{
					Name:    "merkle_tree_status",
					Status:  HealthStatusUnhealthy,
					Message: "Merkle tree not initialized",
				}
			}

			leafCount, nodeCount, cacheSize := node.merkleTree.GetMemoryUsage()

			status := HealthStatusHealthy
			message := "Merkle tree functioning normally"

			// Check for excessive memory usage
			if nodeCount > 50000 || cacheSize > 50000 {
				status = HealthStatusDegraded
				message = "Merkle tree using high memory"
			}

			return &HealthCheck{
				Name:    "merkle_tree_status",
				Status:  status,
				Message: message,
				Details: map[string]interface{}{
					"leaf_count": leafCount,
					"node_count": nodeCount,
					"cache_size": cacheSize,
				},
			}
		},

		"forest_status": func() *HealthCheck {
			if node.forest == nil {
				return &HealthCheck{
					Name:    "forest_status",
					Status:  HealthStatusUnhealthy,
					Message: "Forest not initialized",
				}
			}

			node.forest.mu.RLock()
			hasTree := node.forest.tree != nil
			node.forest.mu.RUnlock()

			status := HealthStatusHealthy
			var message string
			if hasTree {
				message = "Forest with tree functioning normally"
			} else {
				message = "Forest initialized but no tree available"
				status = HealthStatusUnhealthy
			}

			return &HealthCheck{
				Name:    "forest_status",
				Status:  status,
				Message: message,
				Details: map[string]interface{}{
					"has_tree": hasTree,
				},
			}
		},
	}
}

/*
StartHealthMonitorServer starts an HTTP server for health monitoring endpoints.
*/
func StartHealthMonitorServer(addr string, monitor *HealthMonitor) *http.Server {
	server := &http.Server{
		Addr:    addr,
		Handler: monitor.HTTPHandler(),
	}

	// Channel to capture non-graceful server errors (including immediate startup failures)
	errCh := make(chan error, 1)

	// Serve in background and report non-ErrServerClosed errors
	go func() {
		monitor.logger.Info("Starting health monitor server", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Only report real errors; graceful shutdown returns http.ErrServerClosed
			errCh <- err
		}
	}()

	// Watch for monitor shutdown or server error and cleanly stop the HTTP server
	go func() {
		select {
		case <-monitor.ctx.Done():
			// Graceful shutdown with timeout when the monitor is closed
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(ctx); err != nil && err != http.ErrServerClosed {
				monitor.logger.Error("Health monitor server shutdown error", "error", err)
			} else {
				monitor.logger.Info("Health monitor server stopped")
			}
		case err := <-errCh:
			// Handle immediate startup or runtime failures; ensure server resources are released
			monitor.logger.Error("Health monitor server error", "error", err)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if sErr := server.Shutdown(ctx); sErr != nil && sErr != http.ErrServerClosed {
				// If graceful shutdown fails (or server never started), force close
				if cErr := server.Close(); cErr != nil {
					monitor.logger.Error("Health monitor server close error", "error", cErr)
				}
			}
		}
	}()

	return server
}
