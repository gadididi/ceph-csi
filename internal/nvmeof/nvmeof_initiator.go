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
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ceph/ceph-csi/internal/util"
	"github.com/ceph/ceph-csi/internal/util/log"
)

const (
	// Command timeouts.
	connectTimeout    = 30 * time.Second
	disconnectTimeout = 15 * time.Second
)

// NVMeInitiator handles NVMe-oF initiator operations.
type NVMeInitiator interface {
	// LoadKernelModules ensures required kernel modules are loaded
	LoadKernelModules(ctx context.Context) error

	// ConnectSubsystem connects to an NVMe-oF subsystem
	ConnectSubsystem(ctx context.Context, req *ConnectRequest) (bool, error)

	// DisconnectSubsystem disconnects from an NVMe-oF subsystem
	DisconnectSubsystem(ctx context.Context, subsystemNQN string) error

	//
	GetNamespaceDeviceByUUID(ctx context.Context, uuid string) (string, error)
}

// ConnectRequest represents a subsystem connection request.
type ConnectRequest struct {
	SubsystemNQN string
	// TODO - change to struct with ip + port that exist in the current unmerged PR.
	Addresses []string // ["10.242.64.32", "10.242.64.33"]
	Ports     []string // ["4420", "4420"]
	Transport string   // "tcp"
	HostNQN   string   // Optional - empty means use system default
}

// nvmeInitiator implements NVMeInitiator interface.
type nvmeInitiator struct{}

// NewNVMeInitiator creates a new NVMe-oF initiator.
func NewNVMeInitiator() NVMeInitiator {
	return &nvmeInitiator{}
}

// LoadKernelModules ensures required kernel modules are loaded.
func (ni *nvmeInitiator) LoadKernelModules(ctx context.Context) error {
	log.DebugLog(ctx, "Loading NVMe-oF kernel modules")
	var stderr string
	modules := []string{
		"nvme_tcp",
		"nvme_fabrics",
	}

	for _, module := range modules {
		// check if the module is loaded or compiled in
		_, err := os.Stat("/sys/module/" + module)
		if os.IsNotExist(err) {
			// try to load the module
			_, stderr, err = util.ExecCommand(context.TODO(), "modprobe", module)
			if err != nil {
				log.DebugLog(ctx, "%s modprobe failed (%v): %q", module, err, stderr)
			}
		}
	}

	log.DebugLog(ctx, "All NVMe-oF kernel modules loaded successfully")

	return nil
}

// ConnectSubsystem connects to an NVMe-oF subsystem.
func (ni *nvmeInitiator) ConnectSubsystem(ctx context.Context, req *ConnectRequest) (bool, error) {
	log.DebugLog(ctx, "Connecting to NVMe-oF subsystem %s at %v:%v",
		req.SubsystemNQN, req.Addresses, req.Ports)

	// Try connecting to each address until one succeeds
	var lastErr error
	for i, address := range req.Addresses {
		port := "4420" // TODO: add Default port?
		if i < len(req.Ports) {
			port = req.Ports[i]
		}

		// Build nvme connect command for this address
		args := []string{
			"connect",
			"-t", req.Transport,
			"-n", req.SubsystemNQN,
			"-a", address,
			"-s", port,
			"-l", "1800", // TODO - known value for connection timeout.move to be const.
		}

		// Add HostNQN only if specified
		if req.HostNQN != "" {
			args = append(args, "--hostnqn", req.HostNQN)
		}

		stdout, stderr, err := util.ExecCommandWithTimeout(ctx, connectTimeout, "nvme", args...)
		// Execute connection
		if err != nil {
			log.WarningLog(ctx, "Failed to connect to %s:%s - stdout: %s, stderr: %s", address, port, stdout, stderr)
			lastErr = err // TODO: fail or continue to next?

			continue
		}

		log.DebugLog(ctx, "Successfully connected to subsystem %s via %s:%s",
			req.SubsystemNQN, address, port)

		return true, nil
	}

	// TODO: what to do if the list is empty or failed to connect to all ip? error or warning?
	return false, fmt.Errorf("failed to connect to any gateway: %w", lastErr)
}

// DisconnectSubsystem disconnects from an NVMe-oF subsystem using NQN
// TODO: check if no devices left then disconnect.
func (ni *nvmeInitiator) DisconnectSubsystem(ctx context.Context, subsystemNQN string) error {
	log.DebugLog(ctx, "Disconnecting from NVMe-oF subsystem %s", subsystemNQN)

	// Disconnect using nvme disconnect with NQN
	args := []string{"disconnect", "-n", subsystemNQN}
	stdout, stderr, err := util.ExecCommandWithTimeout(ctx, disconnectTimeout, "nvme", args...)
	if err != nil {
		log.WarningLog(ctx, "Failed to disconnect from %s - stdout: %s, stderr: %s", subsystemNQN, stdout, stderr)
	}

	log.DebugLog(ctx, "Successfully disconnected from subsystem %s", subsystemNQN)

	return nil
}

func (ni *nvmeInitiator) GetNamespaceDeviceByUUID(ctx context.Context, uuid string) (string, error) {
	expectedPath := "/dev/disk/by-id/nvme-uuid." + uuid
	if _, err := os.Stat(expectedPath); err == nil {
		// Verify it's a symlink and readable
		if _, err := os.Readlink(expectedPath); err == nil {
			return expectedPath, nil
		}
	}
	// Try with dashes if not present
	cleanUUID := strings.ReplaceAll(uuid, "-", "")
	formattedUUID := formatUUID(cleanUUID) // adds dashes in standard positions
	expectedPath = "/dev/disk/by-id/nvme-uuid." + formattedUUID
	if _, err := os.Stat(expectedPath); err == nil {
		if _, err := os.Readlink(expectedPath); err == nil {
			return expectedPath, nil
		}
	}

	return "", fmt.Errorf("device path with uuid: %s not found", uuid)
}

// Helper to format UUID with dashes.
// TODO: move to nvmeof util.
func formatUUID(uuid string) string {
	// Remove any existing dashes
	clean := strings.ReplaceAll(uuid, "-", "")

	// Add dashes in standard positions
	// 438cb4a8ae90477485677ea1414cd3ac -> 438cb4a8-ae90-4774-8567-7ea1414cd3ac
	if len(clean) == 32 {
		return clean[0:8] + "-" + clean[8:12] + "-" + clean[12:16] + "-" + clean[16:20] + "-" + clean[20:32]
	}

	// Return as-is if not standard length
	return uuid
}
