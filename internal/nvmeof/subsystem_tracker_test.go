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
	"testing"

	"github.com/stretchr/testify/require"
)

// getState returns a copy of the current tracker state for debugging.
//
//nolint:unused // placed here, as it is not used (yet)?
func (t *fileBasedTracker) getState() map[string]interface{} {
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

func TestSubsystemTracker(t *testing.T) {
	t.Parallel()

	volumeA := "pvc-a9ec3ab0-51a9-4e68-bbed-6d4815fb0b9e"
	volumeB := "pvc-baa328d2-37bc-4710-b1ae-a8b6a09f4013"

	t.Run("AddRemoveReferenceFIFO", func(t *testing.T) {
		t.Parallel()

		tracker, err := NewFileBasedTracker("localhost", t.TempDir())
		require.NoError(t, err)

		subsystemNQN := t.Name()

		refs, err := tracker.AddReference(t.Context(), subsystemNQN, volumeA)
		require.NoError(t, err)
		require.Equal(t, uint32(1), refs)

		refs, err = tracker.AddReference(t.Context(), subsystemNQN, volumeB)
		require.NoError(t, err)
		require.Equal(t, uint32(2), refs)

		doDisconnect, refs, err := tracker.RemoveReference(t.Context(), subsystemNQN, volumeA)
		require.NoError(t, err)
		require.False(t, doDisconnect)
		require.Equal(t, uint32(1), refs)

		doDisconnect, refs, err = tracker.RemoveReference(t.Context(), subsystemNQN, volumeB)
		require.NoError(t, err)
		require.True(t, doDisconnect)
		require.Equal(t, uint32(0), refs)
	})

	t.Run("AddRemoveReferenceLIFO", func(t *testing.T) {
		t.Parallel()

		tracker, err := NewFileBasedTracker("localhost", t.TempDir())
		require.NoError(t, err)

		subsystemNQN := t.Name()

		refs, err := tracker.AddReference(t.Context(), subsystemNQN, volumeA)
		require.NoError(t, err)
		require.Equal(t, uint32(1), refs)

		refs, err = tracker.AddReference(t.Context(), subsystemNQN, volumeB)
		require.NoError(t, err)
		require.Equal(t, uint32(2), refs)

		doDisconnect, refs, err := tracker.RemoveReference(t.Context(), subsystemNQN, volumeB)
		require.NoError(t, err)
		require.False(t, doDisconnect)
		require.Equal(t, uint32(1), refs)

		doDisconnect, refs, err = tracker.RemoveReference(t.Context(), subsystemNQN, volumeA)
		require.NoError(t, err)
		require.True(t, doDisconnect)
		require.Equal(t, uint32(0), refs)
	})

	t.Run("AddRemoveReferenceTwice", func(t *testing.T) {
		t.Parallel()

		tracker, err := NewFileBasedTracker("localhost", t.TempDir())
		require.NoError(t, err)

		subsystemNQN := t.Name()

		refs, err := tracker.AddReference(t.Context(), subsystemNQN, volumeA)
		require.NoError(t, err)
		require.Equal(t, uint32(1), refs)

		refs, err = tracker.AddReference(t.Context(), subsystemNQN, volumeB)
		require.NoError(t, err)
		require.Equal(t, uint32(2), refs)

		doDisconnect, refs, err := tracker.RemoveReference(t.Context(), subsystemNQN, volumeB)
		require.NoError(t, err)
		require.False(t, doDisconnect)
		require.Equal(t, uint32(1), refs)

		doDisconnect, refs, err = tracker.RemoveReference(t.Context(), subsystemNQN, volumeB)
		require.NoError(t, err)
		require.False(t, doDisconnect)
		require.Equal(t, uint32(1), refs)
	})

	t.Run("NegativeReference", func(t *testing.T) {
		t.Parallel()

		tracker, err := NewFileBasedTracker("localhost", t.TempDir())
		require.NoError(t, err)

		subsystemNQN := t.Name()

		doDisconnect, refs, err := tracker.RemoveReference(t.Context(), subsystemNQN, volumeA)
		require.True(t, doDisconnect)
		require.Equal(t, uint32(0), refs)
		require.NoError(t, err)
	})
}
