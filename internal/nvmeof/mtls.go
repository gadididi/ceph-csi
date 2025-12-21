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
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultMTLSCertPath = "/etc/nvmeof-certs/CLIENT_CERT"
	defaultMTLSKeyPath  = "/etc/nvmeof-certs/CLIENT_KEY"
	defaultMTLSCAPath   = "/etc/nvmeof-certs/CA_CERT"
)

// loadTLSCredentials returns the appropriate transport credentials for the gateway connection.
// If mTLS is disabled, returns insecure credentials.
// If mTLS is enabled, loads certificates from the mounted Secret and returns TLS credentials.
func (c *GatewayRpcClient) loadTLSCredentials() (credentials.TransportCredentials, error) {
	if !c.config.MTLSEnabled {
		return insecure.NewCredentials(), nil
	}

	return loadMTLSCredentials(
		defaultMTLSCertPath,
		defaultMTLSKeyPath,
		defaultMTLSCAPath,
	)
}

// loadMTLSCredentials loads mTLS certificates and returns gRPC transport credentials.
func loadMTLSCredentials(certPath, keyPath, caPath string) (credentials.TransportCredentials, error) {
	// Load client certificate and key
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load client cert/key from %s, %s: %w",
			certPath, keyPath, err)
	}

	// Load CA certificate
	// #nosec G304 -- caPath is a hardcoded constant, not user input
	caCert, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA cert from %s: %w", caPath, err)
	}

	// Create CA cert pool
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to append CA cert from %s", caPath)
	}

	// Create TLS configuration
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}

	return credentials.NewTLS(tlsConfig), nil
}
