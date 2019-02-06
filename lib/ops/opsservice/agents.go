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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gravitational/gravity/lib/checks"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/httplib"
	validationpb "github.com/gravitational/gravity/lib/network/validation/proto"
	"github.com/gravitational/gravity/lib/ops"
	"github.com/gravitational/gravity/lib/rpc"
	pb "github.com/gravitational/gravity/lib/rpc/proto"
	rpcserver "github.com/gravitational/gravity/lib/rpc/server"
	"github.com/gravitational/gravity/lib/schema"
	"github.com/gravitational/gravity/lib/storage"
	"github.com/gravitational/gravity/lib/users"
	"github.com/gravitational/gravity/lib/utils"
	licenseapi "github.com/gravitational/license"

	"github.com/gravitational/satellite/agent/proto/agentpb"
	"github.com/gravitational/trace"
	log "github.com/sirupsen/logrus"
	netcontext "golang.org/x/net/context" // TODO: remove in go1.9
)

type agentServer storage.Server

// Address returns the address this server is accessible on
// Address implements remoteServer.Address
func (r agentServer) Address() string { return r.AdvertiseIP }

// HostName returns the hostname of this server.
// HostName implements remoteServer.HostName
func (r agentServer) HostName() string { return r.Hostname }

// Debug provides a reference to the specified server useful for logging
// Debug implements remoteServer.Debug
func (r agentServer) Debug() string { return r.Hostname }

// agentReport returns runtime information about servers
// reported by install agents started during install/upgrade process
func (s *site) agentReport(ctx context.Context, opCtx *operationContext) (*ops.AgentReport, error) {
	infos, err := s.agentService().GetServerInfos(ctx, opCtx.key())
	if err != nil && !trace.IsNotFound(err) {
		return nil, trace.Wrap(err)
	}

	// now wait until all boxes go up and return the IPs
	expectedCount := int(opCtx.getNumServers())
	var message string
	if len(infos) == expectedCount && expectedCount != 0 {
		message = fmt.Sprintf("all servers are up: %v", infos.Hostnames())
	} else {
		if len(infos) == 0 {
			message = fmt.Sprintf("waiting for %v servers", expectedCount)
		} else {
			message = fmt.Sprintf("servers %v are up, waiting for %v more",
				infos.Hostnames(), expectedCount-len(infos))
		}
	}

	return &ops.AgentReport{
		Message: message,
		Servers: infos,
	}, nil

}

func (s *site) waitForAgents(ctx context.Context, opCtx *operationContext) (*ops.AgentReport, error) {
	localCtx, cancel := defaults.WithTimeout(ctx)
	defer cancel()

	err := s.agentService().Wait(localCtx, opCtx.key(), opCtx.getNumServers())
	if err != nil {
		return nil, trace.Wrap(err)
	}

	report, err := s.agentReport(localCtx, opCtx)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return report, nil
}

// NewAgentService returns a new agent service
func NewAgentService(server rpcserver.Server, peerStore *AgentPeerStore, advertiseAddr string,
	log log.FieldLogger) *AgentService {
	return &AgentService{
		FieldLogger:   log,
		Server:        server,
		peerStore:     peerStore,
		advertiseAddr: advertiseAddr,
	}
}

// ServerAddr returns the address the install server is listening on
func (r *AgentService) ServerAddr() string {
	return r.advertiseAddr
}

// GetServerInfos collects system information from all agents given with addrs
func (r *AgentService) GetServerInfos(ctx context.Context, key ops.SiteOperationKey) (checks.ServerInfos, error) {
	group, err := r.peerStore.getOrCreateGroup(key)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	peers := group.GetPeers()
	infos := make(checks.ServerInfos, 0, len(peers))
	for _, p := range peers {
		clt := group.WithContext(ctx, p.Addr())
		info, err := checks.GetServerInfo(ctx, clt)
		if err != nil {
			return nil, trace.Wrap(err)
		}

		infos = append(infos, *info)
	}
	return infos, nil
}

// Exec executes command on a remote server
// that is identified by meeting point and agent's address addr
func (r *AgentService) Exec(ctx context.Context, key ops.SiteOperationKey, addr string, args []string, out io.Writer) error {
	group, err := r.peerStore.getOrCreateGroup(key)
	if err != nil {
		return trace.Wrap(err)
	}

	addr = rpc.AgentAddr(addr)
	return trace.Wrap(group.WithContext(ctx, addr).Command(ctx, r.FieldLogger, out, args...))
}

// Validate executes preflight checks on the node specified with addr
// using the specified manifest.
func (r *AgentService) Validate(ctx context.Context, key ops.SiteOperationKey, addr string,
	manifest schema.Manifest, profileName string) ([]*agentpb.Probe, error) {
	group, err := r.peerStore.getOrCreateGroup(key)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	bytes, err := json.Marshal(manifest)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	cluster, err := r.peerStore.backend.GetSite(key.SiteDomain)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	operation, err := r.peerStore.backend.GetSiteOperation(key.SiteDomain, key.OperationID)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	req := validationpb.ValidateRequest{
		Manifest: bytes,
		Profile:  profileName,
		// Verify full requirements from the manifest
		FullRequirements: true,
		Options: &validationpb.ValidateOptions{
			VxlanPort: int32(operation.Vars().OnPrem.VxlanPort),
			DnsAddrs:  cluster.DNSConfig.Addrs,
			DnsPort:   int32(cluster.DNSConfig.Port),
		},
		Docker: &validationpb.Docker{
			StorageDriver: cluster.ClusterState.Docker.StorageDriver,
		},
	}
	addr = rpc.AgentAddr(addr)
	failedProbes, err := group.WithContext(ctx, addr).Validate(ctx, &req)
	return failedProbes, trace.Wrap(err)
}

// CheckPorts executes the ports pingpong network test in the agent cluster
func (r *AgentService) CheckPorts(ctx context.Context, key ops.SiteOperationKey, game checks.PingPongGame) (checks.PingPongGameResults, error) {
	group, err := r.peerStore.getOrCreateGroup(key)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	results, err := pingPong(ctx, group.AgentGroup, game, ports)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return results, nil
}

// CheckBandwidth executes the bandwidth test in the agent cluster
func (r *AgentService) CheckBandwidth(ctx context.Context, key ops.SiteOperationKey, game checks.PingPongGame) (checks.PingPongGameResults, error) {
	group, err := r.peerStore.getOrCreateGroup(key)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	results, err := pingPong(ctx, group.AgentGroup, game, bandwidth)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return results, nil
}

// Wait blocks until the specified number of agents have connected for the
// the given operation. Context can be used for canceling the operation.
func (r *AgentService) Wait(ctx context.Context, key ops.SiteOperationKey, numAgents int) error {
	log.Debugf("Wait for %v agents.", numAgents)
	group, err := r.peerStore.getOrCreateGroup(key)
	if err != nil {
		return trace.Wrap(err)
	}

	// Start a goroutine to duplicate updates about new peers
	// into watchCh before querying the number of already joined agents.
	// This way we can be sure that no update after that point is lost.
	watchCh := make(chan rpcserver.Peer, numAgents)
	localCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		for {
			select {
			case peer := <-group.watchCh:
				select {
				case watchCh <- peer:
				case <-localCtx.Done():
					return
				}
			case <-localCtx.Done():
				return
			}
		}
	}()

	numAgents = numAgents - int(group.NumPeers())
	r.Debugf("Waiting for %v agents.", numAgents)
	for numAgents > 0 {
		select {
		case <-watchCh:
			numAgents = numAgents - 1
		case <-ctx.Done():
			return trace.Wrap(ctx.Err())
		}
	}
	return nil
}

// StopAgents shuts down remote agents
func (r *AgentService) StopAgents(ctx context.Context, key ops.SiteOperationKey) error {
	group, err := r.peerStore.getGroup(key)
	if err != nil {
		return trace.Wrap(err)
	}

	err = group.Shutdown(ctx)
	if err != nil {
		return trace.Wrap(err)
	}

	r.peerStore.removeGroup(ctx, key)
	return nil
}

// AgentService is a controller for install agents.
// Implements ops.AgentService
type AgentService struct {
	log.FieldLogger
	rpcserver.Server
	peerStore     *AgentPeerStore
	advertiseAddr string
}

// NewAgentPeerStore creates a new instance of this agent peer store
func NewAgentPeerStore(backend storage.Backend, users users.Users,
	teleport ops.TeleportProxyService, log log.FieldLogger) *AgentPeerStore {
	return &AgentPeerStore{
		FieldLogger: log,
		teleport:    teleport,
		groups:      make(map[ops.SiteOperationKey]agentGroup),
		backend:     backend,
		users:       users,
	}
}

// NewPeer adds a new peer
func (r *AgentPeerStore) NewPeer(ctx netcontext.Context, req pb.PeerJoinRequest, peer rpcserver.Peer) error {
	r.Infof("NewPeer(%v).", peer.Addr())

	token, user, err := r.authenticatePeer(req.Config.Token)
	if err != nil {
		return trace.Wrap(err)
	}

	info, err := storage.UnmarshalSystemInfo(req.SystemInfo)
	if err != nil {
		return trace.Wrap(err)
	}

	group, err := r.getOrCreateGroup(ops.SiteOperationKey{
		AccountID:   user.GetAccountID(),
		SiteDomain:  token.SiteDomain,
		OperationID: token.OperationID,
	})
	if err != nil {
		return trace.Wrap(err)
	}

	if req.Config.KeyValues[ops.AgentMode] != ops.AgentModeShrink {
		errCheck := r.validatePeer(ctx, group, info, req, token.SiteDomain)
		if errCheck != nil {
			return trace.Wrap(errCheck)
		}
	}

	group.add(peer, info.GetHostname())
	select {
	case group.watchCh <- peer:
		// Notify about a new peer
	default:
	}
	return nil
}

// RemovePeer removes the specified peer from the store
func (r *AgentPeerStore) RemovePeer(ctx netcontext.Context, req pb.PeerLeaveRequest, peer rpcserver.Peer) error {
	r.Infof("RemovePeer(%v).", peer.Addr())

	token, user, err := r.authenticatePeer(req.Config.Token)
	if err != nil {
		return trace.Wrap(err)
	}

	info, err := storage.UnmarshalSystemInfo(req.SystemInfo)
	if err != nil {
		return trace.Wrap(err)
	}

	group, err := r.getOrCreateGroup(ops.SiteOperationKey{
		AccountID:   user.GetAccountID(),
		SiteDomain:  token.SiteDomain,
		OperationID: token.OperationID,
	})
	if err != nil {
		return trace.Wrap(err)
	}

	group.remove(ctx, peer, info.GetHostname())
	return nil
}

// authenticatePeer validates the auth token supplied by a connecting/leaving peer
func (r *AgentPeerStore) authenticatePeer(token string) (*storage.ProvisioningToken, storage.User, error) {
	provToken, err := r.users.GetProvisioningToken(token)
	if err != nil {
		r.Warnf("Invalid peer auth token %q: %v.", token, trace.DebugReport(err))
		return nil, nil, trace.AccessDenied("peer auth failed: %v",
			trace.UserMessage(err))
	}
	user, _, err := r.users.AuthenticateUser(httplib.AuthCreds{
		Password: provToken.Token,
		Type:     httplib.AuthBearer,
	})
	if err != nil {
		r.Warnf("Peer auth failed: %v.", trace.DebugReport(err))
		return nil, nil, trace.AccessDenied("user auth failed: %v",
			trace.UserMessage(err))
	}
	return provToken, user, nil
}

func (r *AgentPeerStore) validatePeer(ctx context.Context, group *agentGroup, info storage.System,
	req pb.PeerJoinRequest, clusterName string) error {
	if err := r.checkHostname(ctx, group, req.Addr, info.GetHostname(), clusterName); err != nil {
		return trace.Wrap(err)
	}

	if err := r.checkLicense(ctx, int(group.NumPeers()), clusterName, info); err != nil {
		return trace.Wrap(err)
	}

	return nil
}

func (r *AgentPeerStore) checkHostname(ctx context.Context, group *agentGroup, addr, hostname, clusterName string) error {
	// collect hostnames from existing servers (for expand)
	servers, err := r.teleport.GetServers(ctx, clusterName, nil)
	if err != nil && !trace.IsNotFound(err) {
		return trace.Wrap(err)
	}

	var existingServers []string
	for _, server := range servers {
		hostname := server.GetLabels()[ops.Hostname]
		if hostname == "" {
			log.Warnf("Server hostname is empty: %+v.", server)
			continue
		}
		existingServers = append(existingServers, hostname)
	}

	if utils.StringInSlice(existingServers, hostname) {
		return trace.AccessDenied("One of existing servers already has hostname %q: %q.", hostname, existingServers)
	}

	if group.hasPeer(addr, hostname) {
		return trace.AccessDenied("One of existing peers already has hostname %q.", hostname)
	}

	r.Debugf("Verified hostname %q.", hostname)
	return nil
}

func (r *AgentPeerStore) checkLicense(ctx context.Context, numPeers int, clusterName string, info storage.System) error {
	cluster, err := r.backend.GetSite(clusterName)
	if err != nil {
		return trace.Wrap(err)
	}

	if cluster.License == "" {
		r.Debugf("Cluster %q does not have license, skip license check.", clusterName)
		return nil
	}

	license, err := licenseapi.ParseLicense(cluster.License)
	if err != nil {
		return trace.Wrap(err)
	}

	count, err := r.teleport.GetServerCount(ctx, clusterName)
	if err != nil {
		return trace.Wrap(err)
	}

	err = license.GetPayload().CheckCount(count + numPeers + 1)
	if err != nil {
		return trace.AccessDenied(trace.UserMessage(err))
	}

	err = checkLicenseCPU(license.GetPayload(), info.GetNumCPU())
	if err != nil {
		return trace.AccessDenied("peer %v not authorized", info.GetHostname())
	}

	r.Debugf("Verified license for %q.", clusterName)
	return nil
}

func (r *AgentPeerStore) getOrCreateGroup(key ops.SiteOperationKey) (*agentGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if group, ok := r.groups[key]; ok {
		return &group, nil
	}

	group, err := r.addGroup(key)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return group, nil
}

func (r *AgentPeerStore) getGroup(key ops.SiteOperationKey) (*agentGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if group, ok := r.groups[key]; ok {
		return &group, nil
	}

	return nil, trace.NotFound("no execution group for %v", key)
}

func (r *AgentPeerStore) removeGroup(ctx context.Context, key ops.SiteOperationKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if group, ok := r.groups[key]; ok {
		group.Close(ctx)
	}
	delete(r.groups, key)
}

// addGroup adds a new empty group.
// Requires r.mu to be held.
func (r *AgentPeerStore) addGroup(key ops.SiteOperationKey) (*agentGroup, error) {
	config := rpcserver.AgentGroupConfig{
		FieldLogger: log.StandardLogger(),
	}
	group, err := rpcserver.NewAgentGroup(config, nil)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	group.Start()
	agentGroup := agentGroup{
		AgentGroup: *group,
		watchCh:    make(chan rpcserver.Peer),
		hostnames:  make(map[string]string),
	}
	r.WithField("key", key).Debug("Added group.")
	// FIXME: assignment copies lock value
	r.groups[key] = agentGroup
	return &agentGroup, nil
}

// AgentPeerStore manages groups of agents based on operation context.
// Implements rpcserver.PeerStore
type AgentPeerStore struct {
	log.FieldLogger
	backend  storage.Backend
	users    users.Users
	teleport ops.TeleportProxyService
	mu       sync.Mutex
	groups   map[ops.SiteOperationKey]agentGroup
}

func (r *agentGroup) add(p rpcserver.Peer, hostname string) {
	r.AgentGroup.Add(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hostnames[p.Addr()] = hostname
}

func (r *agentGroup) remove(ctx netcontext.Context, p rpcserver.Peer, hostname string) {
	r.AgentGroup.Remove(ctx, p)
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.hostnames, p.Addr())
}

// hasPeer determines whether the group already has a peer with the specified
// hostname but a different address
func (r *agentGroup) hasPeer(addr, hostname string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for existingAddr, existingHostname := range r.hostnames {
		if existingHostname == hostname && existingAddr != addr {
			return true
		}
	}
	return false
}

type agentGroup struct {
	rpcserver.AgentGroup
	// watchCh receives new peers
	watchCh chan rpcserver.Peer
	mu      sync.Mutex
	// hostnames maps peer address to a hostname
	hostnames map[string]string
}

func pingPong(ctx context.Context, group rpcserver.AgentGroup, game checks.PingPongGame, fn pingpongHandler) (checks.PingPongGameResults, error) {
	resultsCh := make(chan pingpongResult)
	for addr, req := range game {
		addr = rpc.AgentAddr(addr)
		go fn(ctx, group, addr, req, resultsCh)
	}

	results := make(checks.PingPongGameResults, len(game))
	for _, req := range game {
		select {
		case result := <-resultsCh:
			if result.err != nil {
				return nil, trace.Wrap(result.err)
			}
			results[result.addr] = *result.resp
		case <-time.After(2 * req.Duration):
			return nil, trace.LimitExceeded("timeout waiting for servers")
		}
	}
	return results, nil
}

func ports(ctx context.Context, group rpcserver.AgentGroup, addr string, req checks.PingPongRequest, resultsCh chan<- pingpongResult) {
	resp, err := group.WithContext(ctx, addr).CheckPorts(ctx, req.PortsProto())
	if err != nil {
		resultsCh <- pingpongResult{addr: addr, err: err}
		return
	}
	resultsCh <- pingpongResult{addr: addr, resp: checks.ResultFromPortsProto(resp, nil)}
}

func bandwidth(ctx context.Context, group rpcserver.AgentGroup, addr string, req checks.PingPongRequest, resultsCh chan<- pingpongResult) {
	resp, err := group.WithContext(ctx, addr).CheckBandwidth(ctx, req.BandwidthProto())
	if err != nil {
		resultsCh <- pingpongResult{addr: addr, err: err}
		return
	}
	resultsCh <- pingpongResult{addr: addr, resp: checks.ResultFromBandwidthProto(resp, nil)}
}

type pingpongHandler func(ctx context.Context, group rpcserver.AgentGroup, addr string, req checks.PingPongRequest, resultsCh chan<- pingpongResult)

type pingpongResult struct {
	addr string
	resp *checks.PingPongResult
	err  error
}
