/*
Copyright 2018 Gravitational, Inc.

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

package opsservice

import (
	"github.com/gravitational/gravity/lib/ops"
	"github.com/gravitational/gravity/lib/storage"

	"github.com/gravitational/trace"
)

// rotateSecrets generates a new set of TLS keys for the given node
// as a package that will be automatically downloaded during upgrade
func (s *site) rotateSecrets(ctx *operationContext, node *ProvisionedServer, installOp ops.SiteOperation) (*ops.RotatePackageResponse, error) {
	secretsPackage, err := s.planetSecretsNextPackage(node)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	subnets := installOp.InstallExpand.Subnets
	if subnets.IsEmpty() {
		// Subnets are empty when updating an older installation
		subnets = storage.DefaultSubnets
	}

	if !node.IsMaster() {
		resp, err := s.getPlanetNodeSecretsPackage(ctx, node, secretsPackage)
		if err != nil {
			return nil, trace.Wrap(err)
		}
		return resp, nil
	}

	masterParams := planetMasterParams{
		master:            node,
		secretsPackage:    secretsPackage,
		serviceSubnetCIDR: subnets.Service,
	}
	// if we have a connection to Ops Center set up, configure
	// SNI host so Ops Center can dial in
	trustedCluster, err := storage.GetTrustedCluster(s.backend())
	if err == nil {
		masterParams.sniHost = trustedCluster.GetSNIHost()
	}

	resp, err := s.getPlanetMasterSecretsPackage(ctx, masterParams)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return resp, nil
}
