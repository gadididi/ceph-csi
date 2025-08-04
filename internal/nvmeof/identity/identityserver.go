package identity

import (
	"context"

	csicommon "github.com/ceph/ceph-csi/internal/csi-common"
	"github.com/container-storage-interface/spec/lib/go/csi"
)

type Server struct {
	*csicommon.DefaultIdentityServer
}

func NewIdentityServer(d *csicommon.CSIDriver) *Server {
	return &Server{
		DefaultIdentityServer: csicommon.NewDefaultIdentityServer(d),
	}
}

func (is *Server) GetPluginCapabilities(
	ctx context.Context,
	req *csi.GetPluginCapabilitiesRequest,
) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: []*csi.PluginCapability{
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
					},
				},
			},
			// {
			// 	Type: &csi.PluginCapability_VolumeExpansion_{
			// 		VolumeExpansion: &csi.PluginCapability_VolumeExpansion{
			// 			Type: csi.PluginCapability_VolumeExpansion_ONLINE, // TODO: Check if it is possible to support ONLINE expansion
			// 		},
			// 	},
			// },
		},
	}, nil
}
