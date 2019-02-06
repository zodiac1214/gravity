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

package rpc

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/loc"
	"github.com/gravitational/gravity/lib/ops"
	"github.com/gravitational/gravity/lib/schema"
	"github.com/gravitational/gravity/lib/state"
	"github.com/gravitational/gravity/lib/storage"
	"github.com/gravitational/gravity/lib/utils"

	teleclient "github.com/gravitational/teleport/lib/client"
	"github.com/gravitational/trace"
	"github.com/sirupsen/logrus"
)

// DeployAgentsRequest defines the extent of configuration
// necessary to deploy agents on the local cluster.
type DeployAgentsRequest struct {
	// GravityPackage specifies the gravity binary package to use
	// as the main process
	GravityPackage loc.Locator

	// ClusterState is the cluster state
	ClusterState storage.ClusterState

	// Servers lists the servers to deploy
	Servers []DeployServer

	// SecretsPackage specifies the package with RPC credentials
	SecretsPackage loc.Locator

	// Proxy telekube proxy for remote execution
	Proxy *teleclient.ProxyClient

	// FieldLogger defines the logger to use
	logrus.FieldLogger

	// LeaderParams defines which parameters to pass to the leader agent process.
	// The leader agent would be driving the update in case of the automatic update operation.
	LeaderParams string

	// Leader is the node where the leader agent should be launched
	//
	// If not set, the first master node will serve as a leader
	Leader *storage.Server

	// NodeParams defines which parameters to pass to the regular agent process.
	NodeParams string
}

// Check validates the request to deploy agents
func (r DeployAgentsRequest) Check() error {
	// if the leader node was explicitly passed, make sure
	// it is present among the deploy nodes
	if r.Leader != nil && len(r.LeaderParams) != 0 {
		leaderPresent := false
		for _, node := range r.Servers {
			if node.AdvertiseIP == r.Leader.AdvertiseIP {
				leaderPresent = true
				break
			}
		}
		if !leaderPresent {
			return trace.NotFound("requested leader node %v was not found among deploy servers: %v",
				r.Leader.AdvertiseIP, r.Servers)
		}
	}
	return nil
}

// canBeLeader returns true if the provided node can run leader agent
func (r DeployAgentsRequest) canBeLeader(node DeployServer) bool {
	// if there are no leader-specific parameters, there is no leader agent
	if len(r.LeaderParams) == 0 {
		return false
	}
	// if no specific leader node was requested, any master will do
	if r.Leader == nil {
		return node.Role == schema.ServiceRoleMaster
	}
	// otherwise see if this is the requested leader node
	return r.Leader.AdvertiseIP == node.AdvertiseIP
}

// DeployAgents uses teleport to discover cluster nodes, distribute and run RPC agents
// across the local cluster.
// One of the master nodes is selected to control the automatic update operation specified
// with req.LeaderParams.
func DeployAgents(ctx context.Context, req DeployAgentsRequest) error {
	if err := req.Check(); err != nil {
		return trace.Wrap(err)
	}
	errors := make(chan error, len(req.Servers))
	leaderProcessScheduled := false
	for _, server := range req.Servers {
		leaderProcess := false
		if !leaderProcessScheduled && req.canBeLeader(server) {
			leaderProcess = true
			leaderProcessScheduled = true
			req.WithField("args", req.LeaderParams).
				Infof("Master process will run on node %v/%v.",
					server.Hostname, server.NodeAddr)
		}

		// determine the server's state directory
		stateServer, err := req.ClusterState.FindServerByIP(server.AdvertiseIP)
		if err != nil {
			return trace.Wrap(err)
		}
		serverStateDir := stateServer.StateDir()

		go func(node, nodeStateDir string, leader bool) {
			err := trace.Wrap(deployAgentOnNode(ctx, req, node, nodeStateDir,
				leader, req.SecretsPackage.String()))
			if err != nil {
				logrus.WithError(err).WithField("node", node).Warnf("Failed to deploy agent.")
			}
			errors <- err
		}(server.NodeAddr, serverStateDir, leaderProcess)
	}

	err := utils.CollectErrors(ctx, errors)
	if err != nil {
		return trace.Wrap(err, "failed to deploy agents")
	}

	if !leaderProcessScheduled && len(req.LeaderParams) > 0 {
		return trace.NotFound("No nodes with %s=%s were found while scheduling agents, requested operation %q is not running.",
			schema.ServiceLabelRole, schema.ServiceRoleMaster, req.LeaderParams)
	}

	req.Println("Agents deployed.")
	return nil
}

// DeployServer describes an agent to deploy on every node during update.
//
// Agents come in two flavors: passive or controller.
// Once an agent cluster has been built, an agent will be selected to
// control the update (i.e. give commands to other agents) if the process is automatic.
type DeployServer struct {
	// Role specifies the server's service role
	Role schema.ServiceRole
	// AdvertiseIP specifies the address the server is available on
	AdvertiseIP string
	// Hostname specifies the server's hostname
	Hostname string
	// NodeAddr is the server's address in teleport context
	NodeAddr string
}

// NewDeployServer creates a new instance of DeployServer using teleport node information
func NewDeployServer(ctx context.Context, node storage.Server, proxyClient *teleclient.ProxyClient) (*DeployServer, error) {
	teleservers, err := proxyClient.FindServersByLabels(ctx, defaults.Namespace,
		map[string]string{ops.AdvertiseIP: node.AdvertiseIP})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	if len(teleservers) == 0 {
		return nil, trace.NotFound("no teleport server found for %v", node)
	}
	// there should be at most a single server with the specified advertise IP
	teleserver := teleservers[0]
	role, ok := teleserver.GetLabels()[schema.ServiceLabelRole]
	if !ok {
		role = node.ClusterRole
	}
	advertiseIP := teleserver.GetLabels()[ops.AdvertiseIP]
	return &DeployServer{
		Role:        schema.ServiceRole(role),
		Hostname:    teleserver.GetHostname(),
		AdvertiseIP: advertiseIP,
		NodeAddr:    teleserver.GetAddr(),
	}, nil
}

func deployAgentOnNode(ctx context.Context, req DeployAgentsRequest, node, nodeStateDir string, leader bool, secretsPackage string) error {
	nodeClient, err := req.Proxy.ConnectToNode(ctx, node, defaults.SSHUser, false)
	if err != nil {
		return trace.Wrap(err, node)
	}
	defer nodeClient.Close()

	gravityHostPath := filepath.Join(
		state.GravityRPCAgentDir(nodeStateDir), constants.GravityPackage)
	gravityPlanetPath := filepath.Join(
		defaults.GravityRPCAgentDir, constants.GravityPackage)
	secretsHostDir := filepath.Join(
		state.GravityRPCAgentDir(nodeStateDir), defaults.SecretsDir)
	secretsPlanetDir := filepath.Join(
		defaults.GravityRPCAgentDir, defaults.SecretsDir)

	var runCmd string
	if leader {
		runCmd = fmt.Sprintf("%s agent --debug install %s",
			gravityHostPath, req.LeaderParams)
	} else {
		runCmd = fmt.Sprintf("%s agent --debug install %s",
			gravityHostPath, req.NodeParams)
	}

	err = utils.NewSSHCommands(nodeClient.Client).
		C("rm -rf %s", secretsHostDir).
		C("mkdir -p %s", secretsHostDir).
		WithRetries("%s enter -- --notty %s -- package unpack %s %s --debug --ops-url=%s --insecure",
			constants.GravityBin, defaults.GravityBin, secretsPackage, secretsPlanetDir, defaults.GravityServiceURL).
		IgnoreError("/usr/bin/systemctl stop %s", defaults.GravityRPCAgentServiceName).
		WithRetries("%s enter -- --notty %s -- package export --file-mask=%o %s %s --ops-url=%s --insecure",
			constants.GravityBin, defaults.GravityBin, defaults.SharedExecutableMask,
			req.GravityPackage, gravityPlanetPath, defaults.GravityServiceURL).
		C(runCmd).
		WithLogger(req.WithField("node", node)).
		Run(ctx)
	if err != nil {
		return trace.Wrap(err)
	}

	return nil
}
