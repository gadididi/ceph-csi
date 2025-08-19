package driver

import (
	csicommon "github.com/ceph/ceph-csi/internal/csi-common"
	"github.com/ceph/ceph-csi/internal/nvmeof/controller"
	"github.com/ceph/ceph-csi/internal/nvmeof/identity"
	"github.com/ceph/ceph-csi/internal/nvmeof/nodeserver"
	"github.com/ceph/ceph-csi/internal/rbd"
	"github.com/ceph/ceph-csi/internal/util"
	"github.com/ceph/ceph-csi/internal/util/log"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

// Driver contains the default identity and controller struct.
type Driver struct{}

// NewDriver returns new NVMe-oF driver.
func NewDriver() *Driver {
	return &Driver{}
}

// Run starts the NVMe-oF CSI driver.
func (d *Driver) Run(conf *util.Config) {
	// TODO: move rbd initialization to a common function
	// update clone soft and hard limit
	rbd.SetGlobalInt("rbdHardMaxCloneDepth", conf.RbdHardMaxCloneDepth)
	rbd.SetGlobalInt("rbdSoftMaxCloneDepth", conf.RbdSoftMaxCloneDepth)
	rbd.SetGlobalBool("skipForceFlatten", conf.SkipForceFlatten)
	rbd.SetGlobalInt("maxSnapshotsOnImage", conf.MaxSnapshotsOnImage)
	rbd.SetGlobalInt("minSnapshotsOnImageToStartFlatten", conf.MinSnapshotsOnImage)
	// Create instances of the volume and snapshot journal
	rbd.InitJournals(conf.InstanceID)
	// Initialize CSI driver
	cd := csicommon.NewCSIDriver(conf.DriverName, util.DriverVersion, conf.NodeID, conf.InstanceID, conf.EnableFencing)
	if cd == nil {
		log.FatalLogMsg("failed to initialize CSI driver")
	}

	// Set capabilities (same as RBD since we're wrapping it)
	if conf.IsControllerServer || !conf.IsNodeServer {
		cd.AddControllerServiceCapabilities([]csi.ControllerServiceCapability_RPC_Type{
			csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
			// csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,   // TODO: Implement volume expansion
			// csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT, // TODO: not sure if we need\is possible this feature
			// csi.ControllerServiceCapability_RPC_CLONE_VOLUME, // TODO: not sure if we need\is possible this feature
		})

		// TODO - add support for volume group management?
		cd.AddVolumeCapabilityAccessModes([]csi.VolumeCapability_AccessMode_Mode{
			csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
			// csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
			// csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER,
			// csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER,
		})
	}

	// Create gRPC servers
	server := csicommon.NewNonBlockingGRPCServer()
	srv := &csicommon.Servers{
		IS: identity.NewIdentityServer(cd),
	}

	switch {
	case conf.IsNodeServer:
		ns, err := nodeserver.NewNodeServer(cd, conf.Vtype)
		if err != nil {
			log.FatalLogMsg("failed to initialize node server: %v", err)
		}
		srv.NS = ns
	case conf.IsControllerServer:
		cs, err := controller.NewControllerServer(cd)
		if err != nil {
			log.FatalLogMsg("failed to initialize controller server: %v", err)
		}
		srv.CS = cs
	default:
		ns, err := nodeserver.NewNodeServer(cd, conf.Vtype)
		if err != nil {
			log.FatalLogMsg("failed to initialize node server: %v", err)
		}
		srv.NS = ns
		cs, err := controller.NewControllerServer(cd)
		if err != nil {
			log.FatalLogMsg("failed to initialize controller server: %v", err)
		}
		srv.CS = cs
	}

	server.Start(conf.Endpoint, srv, csicommon.MiddlewareServerOptionConfig{
		LogSlowOpInterval: conf.LogSlowOpInterval,
	})

	if conf.EnableProfiling {
		go util.StartMetricsServer(conf)
		go util.EnableProfiling()
	}

	server.Wait()
}
