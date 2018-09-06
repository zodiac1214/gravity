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

package expand

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/gravitational/gravity/lib/app"
	"github.com/gravitational/gravity/lib/app/client"
	"github.com/gravitational/gravity/lib/checks"
	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/fsm"
	"github.com/gravitational/gravity/lib/httplib"
	"github.com/gravitational/gravity/lib/install"
	"github.com/gravitational/gravity/lib/localenv"
	validationpb "github.com/gravitational/gravity/lib/network/validation/proto"
	"github.com/gravitational/gravity/lib/ops"
	"github.com/gravitational/gravity/lib/ops/opsclient"
	"github.com/gravitational/gravity/lib/pack"
	"github.com/gravitational/gravity/lib/pack/webpack"
	pb "github.com/gravitational/gravity/lib/rpc/proto"
	rpcserver "github.com/gravitational/gravity/lib/rpc/server"
	"github.com/gravitational/gravity/lib/schema"
	"github.com/gravitational/gravity/lib/storage"
	"github.com/gravitational/gravity/lib/utils"

	"github.com/cenkalti/backoff"
	"github.com/fatih/color"
	"github.com/gravitational/coordinate/leader"
	"github.com/gravitational/roundtrip"
	"github.com/gravitational/trace"
	log "github.com/sirupsen/logrus"
)

// PeerConfig is for peers joining the cluster
type PeerConfig struct {
	// Peers is a list of peer addresses
	Peers []string
	// Context controls state of the peer, e.g. it can be cancelled
	Context context.Context
	// Cancel can be used to cancel the context above
	Cancel context.CancelFunc
	// SystemLogFile is the telekube-system.log file path
	SystemLogFile string
	// AdvertiseAddr is advertise addr of this node
	AdvertiseAddr string
	// ServerAddr is optional address of the agent server.
	// It will be derived from agent instructions if unspecified
	ServerAddr string
	// EventsC is channel with events indicating install progress
	EventsC chan install.Event
	// WatchCh is channel that relays peer reconnect events
	WatchCh chan rpcserver.WatchEvent
	// RuntimeConfig is peer's runtime configuration
	pb.RuntimeConfig
	// Silent allows peer to output its progress
	localenv.Silent
	// FieldLogger is used for logging
	log.FieldLogger
	// Debug turns on FSM debug mode
	DebugMode bool
	// Insecure turns on FSM insecure mode
	Insecure bool
	// LocalBackend is local backend of the joining node
	LocalBackend storage.Backend
	// LocalApps is local apps service of the joining node
	LocalApps app.Applications
	// LocalPackages is local package service of the joining node
	LocalPackages pack.PackageService
	// JoinBackend is the local backend where join-specific operation data is stored
	JoinBackend storage.Backend
	// Manual turns on manual plan execution
	Manual bool
	// OperationID is the ID of existing join operation created via UI
	OperationID string
}

// CheckAndSetDefaults checks the parameters and autodetects some defaults
func (c *PeerConfig) CheckAndSetDefaults() error {
	if c.Context == nil {
		return trace.BadParameter("missing Context")
	}
	if c.Cancel == nil {
		return trace.BadParameter("missing Cancel")
	}
	if len(c.Peers) == 0 {
		return trace.BadParameter("missing Peers")
	}
	if c.AdvertiseAddr == "" {
		return trace.BadParameter("missing AdvertiseAddr")
	}
	if err := install.CheckAddr(c.AdvertiseAddr); err != nil {
		return trace.Wrap(err)
	}
	if c.Token == "" {
		return trace.BadParameter("missing Token")
	}
	if c.EventsC == nil {
		return trace.BadParameter("missing EventsC")
	}
	if c.FieldLogger == nil {
		c.FieldLogger = log.WithField(trace.Component, "peer")
	}
	if c.LocalBackend == nil {
		return trace.BadParameter("missing LocalBackned")
	}
	if c.LocalApps == nil {
		return trace.BadParameter("missing LocalApps")
	}
	if c.LocalPackages == nil {
		return trace.BadParameter("missing LocalPackages")
	}
	if c.JoinBackend == nil {
		return trace.BadParameter("missing JoinBackend")
	}
	return nil
}

// Peer is a client that manages joining the cluster
type Peer struct {
	PeerConfig
	// agentDoneCh is the agent's done channel.
	// Only set after the agent has been started
	agentDoneCh <-chan struct{}
	// agent is this peer's RPC agent
	agent rpcserver.Server
}

// NewPeer returns new cluster peer client
func NewPeer(cfg PeerConfig) (*Peer, error) {
	if err := cfg.CheckAndSetDefaults(); err != nil {
		return nil, trace.Wrap(err)
	}
	return &Peer{PeerConfig: cfg}, nil
}

// Init initializes the peer
func (p *Peer) Init() error {
	if err := install.InitLogging(p.SystemLogFile); err != nil {
		return trace.Wrap(err)
	}
	if err := p.bootstrap(); err != nil {
		return trace.Wrap(err)
	}
	utils.WatchTerminationSignals(p.Context, p.Cancel, p, p.FieldLogger)
	watchReconnects(p.Context, p.Cancel, p.WatchCh)
	return nil
}

func (p *Peer) dialSite(addr string) (*operationContext, error) {
	var targetURL string
	// assume that is URL
	if strings.Contains(addr, "http") {
		targetURL = addr
	} else {
		targetURL = fmt.Sprintf("https://%v:%v", addr, defaults.GravitySiteNodePort)
	}
	httpClient := roundtrip.HTTPClient(httplib.GetClient(true))
	operator, err := opsclient.NewBearerClient(targetURL, p.Token, httpClient)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	packages, err := webpack.NewBearerClient(targetURL, p.Token, httpClient)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	apps, err := client.NewBearerClient(targetURL, p.Token, httpClient)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	cluster, err := operator.GetLocalSite()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	err = p.checkAndSetServerProfile(cluster.App)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	installOp, _, err := ops.GetInstallOperation(cluster.Key(), operator)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	err = p.runLocalChecks(*cluster, *installOp)
	if err != nil {
		return nil, utils.Abort(err) // stop retrying on failed checks
	}
	var operation *ops.SiteOperation
	if p.OperationID == "" {
		operation, err = p.createExpandOperation(operator, *cluster)
	} else {
		operation, err = p.joinExpandOperation(operator, *cluster)
	}
	if err != nil {
		return nil, trace.Wrap(err)
	}
	creds, err := install.LoadRPCCredentials(p.Context, packages, p.FieldLogger)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &operationContext{
		Operator:  operator,
		Packages:  packages,
		Apps:      apps,
		Operation: *operation,
		Site:      *cluster,
		Creds:     *creds,
	}, nil
}

// createExpandOperation creates a new expand operation
func (p *Peer) createExpandOperation(operator ops.Operator, cluster ops.Site) (*ops.SiteOperation, error) {
	key, err := operator.CreateSiteExpandOperation(ops.CreateSiteExpandOperationRequest{
		AccountID:   cluster.AccountID,
		SiteDomain:  cluster.Domain,
		Provisioner: schema.ProvisionerOnPrem,
		Servers:     map[string]int{p.Role: 1},
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	err = operator.SetOperationState(*key, ops.SetOperationStateRequest{
		State: ops.OperationStateReady,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	operation, err := operator.GetSiteOperation(*key)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return operation, nil
}

// joinExpandOperation joins existing expand operation that was created via UI
//
// It will be returning an error until the operation moves to "ready" state
func (p *Peer) joinExpandOperation(operator ops.Operator, cluster ops.Site) (*ops.SiteOperation, error) {
	operation, err := operator.GetSiteOperation(ops.SiteOperationKey{
		AccountID:   cluster.AccountID,
		SiteDomain:  cluster.Domain,
		OperationID: p.OperationID,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return operation, nil
}

// dialWizard connects to a wizard
func (p *Peer) dialWizard(addr string) (*operationContext, error) {
	env, err := localenv.NewRemoteEnvironment()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	_, err = env.LoginWizard(addr)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	cluster, operation, err := validateWizardState(env.Operator)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	err = p.checkAndSetServerProfile(cluster.App)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	err = p.runLocalChecks(*cluster, *operation)
	if err != nil {
		return nil, utils.Abort(err) // stop retrying on failed checks
	}
	creds, err := install.LoadRPCCredentials(p.Context, env.Packages, p.FieldLogger)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &operationContext{
		Operator:  env.Operator,
		Packages:  env.Packages,
		Apps:      env.Apps,
		Operation: *operation,
		Site:      *cluster,
		Creds:     *creds,
	}, nil
}

func (p *Peer) checkAndSetServerProfile(app ops.Application) error {
	if p.Role == "" {
		for _, profile := range app.Manifest.NodeProfiles {
			p.Role = profile.Name
			p.Infof("no role specified, picking %q", p.Role)
			break
		}
	}
	for _, profile := range app.Manifest.NodeProfiles {
		if profile.Name == p.Role {
			return nil
		}
	}
	return trace.NotFound("server role %q is not found", p.Role)
}

// runLocalChecks makes sure node satisfies system requirements
func (p *Peer) runLocalChecks(cluster ops.Site, installOperation ops.SiteOperation) error {
	return checks.RunLocalChecks(checks.LocalChecksRequest{
		Context:  p.Context,
		Manifest: cluster.App.Manifest,
		Role:     p.Role,
		Options: &validationpb.ValidateOptions{
			VxlanPort: int32(installOperation.GetVars().OnPrem.VxlanPort),
		},
		AutoFix: true,
	})
}

// operationContext describes the active install/expand operation.
// Used by peers to add new nodes for install/expand and poll progress
// of the operation.
type operationContext struct {
	Operator  ops.Operator
	Packages  pack.PackageService
	Apps      app.Applications
	Operation ops.SiteOperation
	Site      ops.Site
	Creds     rpcserver.Credentials
}

// connect dials to either a running wizard OpsCenter or a local gravity cluster.
// For wizard, if the dial succeeds, it will join the active installation and return
// an operation context of the active install operation.
//
// For a local gravity cluster, it will attempt to start the expand operation
// and will return an operation context wrapping a new expand operation.
func (p *Peer) connect() (*operationContext, error) {
	ticker := backoff.NewTicker(leader.NewUnlimitedExponentialBackOff())
	for {
		select {
		case <-p.Context.Done():
			return nil, trace.ConnectionProblem(p.Context.Err(), "context is closing")
		case tm := <-ticker.C:
			if tm.IsZero() {
				return nil, trace.ConnectionProblem(nil, "timeout")
			}
			ctx, err := p.tryConnect()
			if err != nil {
				// join token is incorrect, fail immediately and report to user
				if trace.IsAccessDenied(err) {
					return nil, trace.AccessDenied("access denied: bad secret token")
				}
				if err, ok := trace.Unwrap(err).(*utils.AbortRetry); ok {
					return nil, trace.BadParameter(err.OriginalError())
				}
				// most of the time errors are expected, like another operation
				// is in progress, so just retry until we connect (or timeout)
				continue
			}
			return ctx, nil
		}
	}
}

func (p *Peer) tryConnect() (op *operationContext, err error) {
	p.sendMessage("Connecting to cluster")
	for _, addr := range p.Peers {
		op, err = p.dialWizard(addr)
		if err == nil {
			p.sendMessage("Connected to installer at %v", addr)
			return op, nil
		}
		if utils.IsAbortError(err) {
			return nil, trace.Wrap(err)
		}

		// already exists error is returned when there's an ongoing install operation,
		// do not attempt to dial the site until it fully completes
		if trace.IsAlreadyExists(err) {
			p.sendMessage("Waiting for the install operation to finish")
			return nil, trace.Wrap(err)
		}
		p.Infof("Failed connecting to wizard: %v.", err)

		op, err = p.dialSite(addr)
		if err == nil {
			p.sendMessage("Connected to existing cluster at %v", addr)
			return op, nil
		}
		if utils.IsAbortError(err) {
			return nil, trace.Wrap(err)
		}

		p.Infof("Failed connecting to cluster: %v.", err)
		if trace.IsCompareFailed(err) {
			p.sendMessage("Waiting for another operation to finish at %v", addr)
		}
	}
	return op, trace.Wrap(err)
}

func (p *Peer) run() error {
	ctx, err := p.connect()
	if err != nil {
		return trace.Wrap(err)
	}

	err = p.ensureServiceUserAndBinary(*ctx)
	if err != nil {
		return trace.Wrap(err)
	}

	installState := ctx.Operation.InstallExpand
	if installState == nil {
		return trace.BadParameter("internal error, no install state for %v", ctx.Operation)
	}
	p.Debugf("Got operation state: %#v.", installState)

	err = p.checkAndSetServerProfile(ctx.Site.App)
	if err != nil {
		return trace.Wrap(err)
	}

	agentInstructions, ok := installState.Agents[p.Role]
	if !ok {
		return trace.NotFound("agent instructions not found for %v", p.Role)
	}

	listener, err := net.Listen("tcp", defaults.GravityRPCAgentAddr(p.AdvertiseAddr))
	if err != nil {
		return trace.Wrap(err)
	}

	config := rpcserver.PeerConfig{
		Config: rpcserver.Config{
			Listener:      listener,
			Credentials:   ctx.Creds,
			RuntimeConfig: p.RuntimeConfig,
		},
		WatchCh: p.WatchCh,
		ReconnectStrategy: rpcserver.ReconnectStrategy{
			ShouldReconnect: utils.ShouldReconnectPeer,
		},
	}
	agent, err := install.StartAgent(agentInstructions.AgentURL, config,
		p.WithField("peer", listener.Addr().String()))
	if err != nil {
		listener.Close()
		return trace.Wrap(err)
	}

	p.agent = agent
	p.agentDoneCh = agent.Done()
	go agent.Serve()

	if ctx.Operation.Type == ops.OperationExpand {
		err = p.startExpandOperation(*ctx)
		if err != nil {
			agent.Stop(p.Context)
			if err := ctx.Operator.DeleteSiteOperation(ctx.Operation.Key()); err != nil {
				p.Errorf("failed to delete operation: %v", trace.DebugReport(err))
			}
			return trace.Wrap(err)
		}
	}

	install.PollProgress(p.Context, p.send, ctx.Operator, ctx.Operation.Key(), agent.Done())
	return nil
}

// Stop shuts down RPC agent
func (p *Peer) Stop(ctx context.Context) error {
	if p.agent == nil {
		return nil
	}
	return trace.Wrap(p.agent.Stop(ctx))
}

// waitForOperation blocks until the join operation is not ready
func (p *Peer) waitForOperation(ctx operationContext) error {
	ticker := backoff.NewTicker(backoff.NewConstantBackOff(1 * time.Second))
	defer ticker.Stop()
	for {
		select {
		case <-p.Context.Done():
			return trace.ConnectionProblem(p.Context.Err(), "context closed")
		case <-ticker.C:
			operation, err := ctx.Operator.GetSiteOperation(ctx.Operation.Key())
			if err != nil {
				return trace.Wrap(err)
			}
			if operation.State != ops.OperationStateReady {
				p.Info("Operation is not ready yet.")
				continue
			}
			p.Info("Operation is ready!")
			return nil
		}
	}
}

func (p *Peer) waitForAgents(ctx operationContext) error {
	ticker := backoff.NewTicker(&backoff.ExponentialBackOff{
		InitialInterval: time.Second,
		Multiplier:      1.5,
		MaxInterval:     10 * time.Second,
		MaxElapsedTime:  5 * time.Minute,
		Clock:           backoff.SystemClock,
	})
	defer ticker.Stop()
	for {
		select {
		case <-p.Context.Done():
			return trace.ConnectionProblem(p.Context.Err(), "context is closing")
		case tm := <-ticker.C:
			if tm.IsZero() {
				return trace.ConnectionProblem(nil, "timed out waiting for agents to join")
			}
			report, err := ctx.Operator.GetSiteExpandOperationAgentReport(ctx.Operation.Key())
			if err != nil {
				p.Warningf("%v", err)
				continue
			}
			if len(report.Servers) == 0 {
				continue
			}
			op, err := ctx.Operator.GetSiteOperation(ctx.Operation.Key())
			if err != nil {
				p.Warningf("%v", err)
				continue
			}
			req, err := install.GetServers(*op, report.Servers)
			if err != nil {
				p.Warningf("%v", err)
				continue
			}
			err = ctx.Operator.UpdateExpandOperationState(ctx.Operation.Key(), *req)
			if err != nil {
				return trace.Wrap(err)
			}
			p.Infof("installation can proceed! %v", report)
			return nil
		}
	}
}

func (p *Peer) send(e install.Event) {
	select {
	case p.EventsC <- e:
	case <-p.Context.Done():
		select {
		case p.EventsC <- install.Event{Error: trace.ConnectionProblem(p.Context.Err(), "context is closing")}:
		default:
		}
	default:
		p.Warningf("failed to send event, events channel is blocked")
	}
}

// sendMessage sends an event with just a progress message
func (p *Peer) sendMessage(format string, args ...interface{}) {
	p.send(install.Event{
		Progress: &ops.ProgressEntry{
			Message: fmt.Sprintf(format, args...)},
	})
}

// PrintStep outputs a message to the console
func (p *Peer) PrintStep(format string, args ...interface{}) {
	p.printf("%v\t%v\n", time.Now().UTC().Format(constants.HumanDateFormatSeconds),
		fmt.Sprintf(format, args...))
}

func (p *Peer) printf(format string, args ...interface{}) {
	p.Silent.Printf(format, args...)
}

// Start starts non-interactive join process
func (p *Peer) Start() (err error) {
	go func() {
		err := p.run()
		if err != nil {
			p.send(install.Event{Error: err})
		}
	}()
	return nil
}

// Done returns the done channel for the agent if one has been started.
// If no agent has been started, nil channel is returned
func (p *Peer) Done() <-chan struct{} {
	return p.agentDoneCh
}

// Wait waits for the expand operation to complete
func (p *Peer) Wait() error {
	start := time.Now()
	for {
		select {
		case <-p.Done():
			p.Info("Agent shut down.")
			return nil
		case event := <-p.EventsC:
			if event.Error != nil {
				if utils.IsContextCancelledError(event.Error) {
					return nil
				}
				return trace.Wrap(event.Error)
			}
			progress := event.Progress
			if progress.Message != "" {
				p.PrintStep(progress.Message)
			}
			if progress.State == ops.ProgressStateCompleted {
				p.PrintStep(color.GreenString("Joined cluster in %v", time.Now().Sub(start)))
				return nil
			}
			if progress.State == ops.ProgressStateFailed {
				p.Silent.Println(color.RedString("Failed to join the cluster"))
				p.Silent.Printf("---\nAgent process will keep running so you can re-run certain steps.\n" +
					"Once no longer needed, this process can be shutdown using Ctrl-C.\n")
			}
		}
	}
}

func (p *Peer) startExpandOperation(ctx operationContext) error {
	err := p.waitForOperation(ctx)
	if err != nil {
		return trace.Wrap(err)
	}
	err = p.waitForAgents(ctx)
	if err != nil {
		return trace.Wrap(err)
	}
	err = p.initOperationPlan(ctx)
	if err != nil {
		return trace.Wrap(err)
	}
	err = p.syncOperation(ctx)
	if err != nil {
		return trace.Wrap(err)
	}
	if p.Manual {
		p.Silent.Println(`Operation was started in manual mode
Inspect the operation plan using "gravity plan" and execute plan phases manually using "gravity join --phase=<phase-id>"
After all phases have completed successfully, shutdown this process using Ctrl-C`)
		return nil
	}
	fsm, err := p.getFSM(ctx)
	if err != nil {
		return trace.Wrap(err)
	}
	go func() {
		fsmErr := fsm.ExecutePlan(p.Context, utils.NewNopProgress(), false)
		if err != nil {
			p.Errorf("Failed to execute plan: %v.",
				trace.DebugReport(err))
		}
		err := fsm.Complete(fsmErr)
		if err != nil {
			p.Errorf("Failed to complete operation: %v.",
				trace.DebugReport(err))
		}
	}()
	// err = ctx.Operator.SiteExpandOperationStart(ctx.Operation.Key())
	// if err != nil {
	// 	return trace.Wrap(err)
	// }
	return nil
}

func (p *Peer) getFSM(ctx operationContext) (*fsm.FSM, error) {
	return NewFSM(FSMConfig{
		Operator:      ctx.Operator,
		OperationKey:  ctx.Operation.Key(),
		Apps:          ctx.Apps,
		Packages:      ctx.Packages,
		LocalBackend:  p.LocalBackend,
		LocalApps:     p.LocalApps,
		LocalPackages: p.LocalPackages,
		JoinBackend:   p.JoinBackend,
		Credentials:   ctx.Creds.Client,
		DebugMode:     p.DebugMode,
		Insecure:      p.Insecure,
	})
}

func validateWizardState(operator ops.Operator) (*ops.Site, *ops.SiteOperation, error) {
	accounts, err := operator.GetAccounts()
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	if len(accounts) == 0 {
		return nil, nil, trace.NotFound("no accounts created yet")
	}
	account := accounts[0]
	clusters, err := operator.GetSites(account.ID)
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	if len(clusters) == 0 {
		return nil, nil, trace.NotFound("no sites created yet")
	}
	cluster := clusters[0]

	operation, progress, err := ops.GetInstallOperation(cluster.Key(), operator)
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}

	if progress.IsCompleted() && operation.State == ops.OperationStateCompleted {
		return nil, nil, trace.BadParameter("installation already completed")
	}

	switch operation.State {
	case ops.OperationStateInstallInitiated, ops.OperationStateFailed:
		// Consider these states for resuming the installation
		// (including failed that puts the operation into manual mode)
	default:
		return nil, nil,
			trace.AlreadyExists("operation %v is in progress", operation)
	}
	if len(operation.InstallExpand.Profiles) == 0 {
		return nil, nil,
			trace.ConnectionProblem(nil, "no server profiles selected yet")
	}

	if operation.State == ops.OperationStateFailed {
		// Cannot validate the agents for a failed operation
		// that has been placed into manual mode
		return &cluster, operation, nil
	}

	report, err := operator.GetSiteInstallOperationAgentReport(operation.Key())
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	// do not join until there's at least one other agent, this way we can make sure that
	// the agent on the installer node (the one that runs "install") joins first
	if len(report.Servers) == 0 {
		return nil, nil, trace.NotFound("no other agents joined yet")
	}

	return &cluster, operation, nil
}

func watchReconnects(ctx context.Context, cancel context.CancelFunc, watchCh <-chan rpcserver.WatchEvent) {
	go func() {
		for event := range watchCh {
			if event.Error == nil {
				continue
			}
			log.Warnf("Failed to reconnect to %v: %v.", event.Peer, event.Error)
			cancel()
			return
		}
	}()
}
