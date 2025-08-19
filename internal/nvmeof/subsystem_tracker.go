/*
Copyright 2025 The Ceph-CSI Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package nvmeof

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/ceph/ceph-csi/internal/util/log"
)

const (
	// Default state directory for tracking subsystem references.
	defaultStateDir = "/var/lib/ceph-csi/nvmeof"
	// File name for subsystem state.
	stateFileName = "subsystem-state.json"
)

// SubsystemTracker tracks NVMe-oF subsystem connection references.
type SubsystemTracker interface {
	// AddReference increments the reference count for a subsystem
	AddReference(ctx context.Context, subsystemNQN, volumeID string) (uint32, error)

	// RemoveReference decrements the reference count for a subsystem
	// Returns true if the reference count reaches zero (should disconnect)
	RemoveReference(ctx context.Context, subsystemNQN, volumeID string) (bool, uint32, error)
}

// subsystemState represents the state of a single subsystem.
type subsystemState struct {
	SubsystemNQN   string    `json:"subsystem_nqn"`
	ReferenceCount uint32    `json:"reference_count"`
	LastUpdated    time.Time `json:"last_updated"`
	NodeID         string    `json:"node_id"`
	Volumes        []string  `json:"volumes"` // List of volume IDs using this subsystem
}

// trackerState represents the overall state file.
type trackerState struct {
	Version    string                     `json:"version"`
	NodeID     string                     `json:"node_id"`
	LastUpdate time.Time                  `json:"last_update"`
	Subsystems map[string]*subsystemState `json:"subsystems"`
}

// fileBasedTracker implements SubsystemTracker using file-based persistence.
type fileBasedTracker struct {
	stateDir  string
	stateFile string
	nodeID    string
	mutex     sync.RWMutex
	state     *trackerState
}

// NewFileBasedTracker creates a new file-based subsystem tracker.
func NewFileBasedTracker(nodeID string, stateDir ...string) (SubsystemTracker, error) {
	dir := defaultStateDir
	if len(stateDir) > 0 && stateDir[0] != "" {
		dir = stateDir[0]
	}

	tracker := &fileBasedTracker{
		stateDir:  dir,
		stateFile: filepath.Join(dir, stateFileName),
		nodeID:    nodeID,
		state: &trackerState{
			Version:    "v1", // TODO: is it good to have version? from where I can fetch it?
			NodeID:     nodeID,
			Subsystems: make(map[string]*subsystemState),
		},
	}

	// Ensure state directory exists
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create state directory %s: %w", dir, err)
	}

	// Load existing state, if the directory just created, loadState returns nil
	if err := tracker.loadState(); err != nil {
		return nil, fmt.Errorf("failed to load subsystem state: %w", err)
	}

	return tracker, nil
}

// AddReference increments the reference count for a subsystem.
func (t *fileBasedTracker) AddReference(ctx context.Context, subsystemNQN, volumeID string) (uint32, error) {
	log.DebugLog(ctx, "Adding reference for subsystem %s", subsystemNQN)

	t.mutex.Lock()
	defer t.mutex.Unlock()

	// Get or create subsystem state
	subsysState, exists := t.state.Subsystems[subsystemNQN]
	if !exists {
		subsysState = &subsystemState{
			SubsystemNQN:   subsystemNQN,
			ReferenceCount: 0,
			NodeID:         t.nodeID,
			Volumes:        make([]string, 0),
		}
		t.state.Subsystems[subsystemNQN] = subsysState
	}

	// Increment reference count
	subsysState.ReferenceCount++
	addVolumeToSubsystem(subsysState, volumeID)
	timeNow := time.Now()
	subsysState.LastUpdated = timeNow
	t.state.LastUpdate = timeNow

	// Save state
	if err := t.saveStateLocked(); err != nil {
		// Rollback on failure
		subsysState.ReferenceCount--
		removeVolumeFromSubsystem(subsysState, volumeID) // rollback

		return 0, fmt.Errorf("failed to save state after adding reference: %w", err)
	}

	log.DebugLog(ctx, "Added reference for subsystem %s, volume %s, new count: %d",
		subsystemNQN, volumeID, subsysState.ReferenceCount)

	return subsysState.ReferenceCount, nil
}

// RemoveReference decrements the reference count for a subsystem.
func (t *fileBasedTracker) RemoveReference(ctx context.Context, subsystemNQN, volumeID string) (bool, uint32, error) {
	log.DebugLog(ctx, "Removing reference for subsystem %s", subsystemNQN)

	t.mutex.Lock()
	defer t.mutex.Unlock()

	subsysState, exists := t.state.Subsystems[subsystemNQN]
	if !exists {
		log.WarningLog(ctx, "Attempted to remove reference for unknown subsystem %s", subsystemNQN)

		return true, 0, nil // Consider it as "should disconnect"
	}

	// Decrement reference count
	if subsysState.ReferenceCount > 0 {
		subsysState.ReferenceCount--
	}
	removeVolumeFromSubsystem(subsysState, volumeID)
	timeNow := time.Now()
	subsysState.LastUpdated = timeNow
	t.state.LastUpdate = timeNow

	shouldDisconnect := subsysState.ReferenceCount == 0

	// Remove from state if no more references
	if shouldDisconnect {
		delete(t.state.Subsystems, subsystemNQN)
	}

	// Save state
	if err := t.saveStateLocked(); err != nil {
		// Rollback on failure
		if shouldDisconnect {
			t.state.Subsystems[subsystemNQN] = subsysState
		}
		subsysState.ReferenceCount++
		addVolumeToSubsystem(subsysState, volumeID) // rollback

		return false, subsysState.ReferenceCount, fmt.Errorf("failed to save state after removing reference: %w", err)
	}

	log.DebugLog(ctx, "Removed reference for subsystem %s, , volume %s, remaining count: %d, should disconnect: %v",
		subsystemNQN, volumeID, subsysState.ReferenceCount, shouldDisconnect)

	return shouldDisconnect, subsysState.ReferenceCount, nil
}

// addVolumeToSubsystem associates a volume ID with a subsystem for tracking.
func addVolumeToSubsystem(subsysState *subsystemState, volumeID string) {
	// Add volume if not already present
	if slices.Contains(subsysState.Volumes, volumeID) {
		return // Already present
	}
	subsysState.Volumes = append(subsysState.Volumes, volumeID)
}

// removeVolumeFromSubsystem removes a volume ID from a subsystem.
func removeVolumeFromSubsystem(subsysState *subsystemState, volumeID string) {
	// Remove volume from list
	newVolumes := make([]string, 0, len(subsysState.Volumes))
	for _, vol := range subsysState.Volumes {
		if vol != volumeID {
			newVolumes = append(newVolumes, vol)
		}
	}
	subsysState.Volumes = newVolumes
}

// GetState returns a copy of the current tracker state for debugging.
func (t *fileBasedTracker) GetState(ctx context.Context) map[string]interface{} {
	t.mutex.RLock()
	defer t.mutex.RUnlock()

	state := make(map[string]interface{})
	state["version"] = t.state.Version
	state["node_id"] = t.state.NodeID
	state["last_update"] = t.state.LastUpdate
	state["subsystem_count"] = len(t.state.Subsystems)

	subsystems := make(map[string]interface{})
	for nqn, subsys := range t.state.Subsystems {
		subsystems[nqn] = map[string]interface{}{
			"reference_count": subsys.ReferenceCount,
			"last_updated":    subsys.LastUpdated,
			"volume_count":    len(subsys.Volumes),
		}
	}
	state["subsystems"] = subsystems

	return state
}

// loadState loads the subsystem state from disk.
func (t *fileBasedTracker) loadState() error {
	// Check if state file exists
	if _, err := os.Stat(t.stateFile); os.IsNotExist(err) {
		log.DefaultLog("State file %s does not exist, starting with empty state", t.stateFile)

		return nil
	}

	// Read state file
	data, err := os.ReadFile(t.stateFile)
	if err != nil {
		return fmt.Errorf("failed to read state file %s: %w", t.stateFile, err)
	}

	// Parse JSON
	var loadedState trackerState
	if err := json.Unmarshal(data, &loadedState); err != nil {
		return fmt.Errorf("failed to parse state file %s: %w", t.stateFile, err)
	}

	// Validate and migrate if necessary
	if loadedState.NodeID != t.nodeID {
		log.DefaultLog("State file node ID mismatch (file: %s, current: %s), starting fresh",
			loadedState.NodeID, t.nodeID)

		return nil
	}

	// Use loaded state
	t.state = &loadedState
	if t.state.Subsystems == nil {
		t.state.Subsystems = make(map[string]*subsystemState)
	}

	log.DefaultLog("Loaded subsystem state with %d entries", len(t.state.Subsystems))

	return nil
}

// saveStateLocked saves the current state to disk (must be called with mutex held).
func (t *fileBasedTracker) saveStateLocked() error {
	// Update timestamp
	t.state.LastUpdate = time.Now()

	// Marshal to JSON
	data, err := json.MarshalIndent(t.state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Write to temporary file first
	tempFile := t.stateFile + ".tmp"
	if err := os.WriteFile(tempFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to write temporary state file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempFile, t.stateFile); err != nil {
		err := os.Remove(tempFile) // Clean up temp file
		if err != nil {
			return fmt.Errorf("failed to remove temporary state file: %w", err)
		}

		return fmt.Errorf("failed to rename state file: %w", err)
	}

	return nil
}
