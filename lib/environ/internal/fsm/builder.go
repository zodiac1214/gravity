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

package fsm

import (
	"fmt"
	"path"

	libphase "github.com/gravitational/gravity/lib/environ/internal/phases"
	libfsm "github.com/gravitational/gravity/lib/fsm"
	"github.com/gravitational/gravity/lib/loc"
	"github.com/gravitational/gravity/lib/ops"
	"github.com/gravitational/gravity/lib/storage"
	"github.com/gravitational/gravity/lib/update"

	"github.com/gravitational/trace"
)

// NewOperationPlan returns a new plan for the specified operation
// and the given set of servers
func NewOperationPlan(app loc.Locator, operation ops.SiteOperation, servers []storage.Server) (*storage.OperationPlan, error) {
	masters, nodes := libfsm.SplitServers(servers)
	if len(masters) == 0 {
		return nil, trace.NotFound("no master servers found in cluster state")
	}

	builder := phaseBuilder{app: app}
	updateMasters := *builder.masters(masters)
	phases := phases{updateMasters}
	if len(nodes) != 0 {
		updateNodes := *builder.nodes(nodes).Require(updateMasters)
		phases = append(phases, updateNodes)
	}

	plan := &storage.OperationPlan{
		OperationID:   operation.ID,
		OperationType: operation.Type,
		AccountID:     operation.AccountID,
		ClusterName:   operation.SiteDomain,
		Phases:        phases.asPhases(),
		Servers:       servers,
	}
	update.ResolvePlan(plan)

	return plan, nil
}

func (r phaseBuilder) masters(servers []storage.Server) *phase {
	root := root(phase{
		ID:          "masters",
		Description: "Update cluster environment variables",
	})
	first, others := servers[0], servers[1:]

	if len(others) == 0 {
		root.AddSequential(r.common(&first)...)
		return &root
	}

	node := r.node(first, first.Hostname, "Update environment variables on node %q")
	if len(others) != 0 {
		node.AddSequential(setLeaderElection(enable(), disable(first), first,
			"stepdown", "Step down %q as Kubernetes leader"))
	}
	node.AddSequential(r.common(&first)...)
	if len(others) != 0 {
		node.AddSequential(setLeaderElection(enable(first), disable(others...), first,
			"elect", "Make node %q Kubernetes leader"))
	}
	root.AddSequential(node)
	for i, server := range others {
		node := r.node(server, server.Hostname, "Update environment variables on node %q")
		node.AddSequential(r.common(&others[i])...)
		node.AddSequential(setLeaderElection(enable(server), disable(), server,
			"enable-elections", "Enable leader election on node %q"))
		root.AddSequential(node)
	}
	return &root
}

func (r phaseBuilder) nodes(servers []storage.Server) *phase {
	root := root(phase{
		ID:          "nodes",
		Description: "Update cluster environment variables",
	})
	for i, server := range servers {
		node := r.node(server, server.Hostname, "Update environment variables on node %q")
		node.AddSequential(r.common(&servers[i])...)
		root.AddSequential(node)
	}
	return &root
}

func (r phaseBuilder) common(server *storage.Server) (phases []phase) {
	phases = append(phases,
		r.drain(server),
		r.updateConfig(server),
		r.restart(server),
		r.taint(server),
		r.uncordon(server),
		r.endpoints(server),
		r.untaint(server),
	)
	return phases
}

func (r phaseBuilder) updateConfig(server *storage.Server) phase {
	node := r.node(*server, "update-config", "Update runtime configuration on node %q")
	node.Executor = libphase.UpdateConfig
	node.Data = &storage.OperationPhaseData{
		Server:  server,
		Package: &r.app,
	}
	return node
}

func (r phaseBuilder) restart(server *storage.Server) phase {
	node := r.node(*server, "restart", "Restart container on node %q")
	node.Executor = libphase.RestartContainer
	node.Data = &storage.OperationPhaseData{
		Server:  server,
		Package: &r.app,
	}
	return node
}

func (r phaseBuilder) taint(server *storage.Server) phase {
	node := r.node(*server, "taint", "Taint node %q")
	node.Executor = libphase.Taint
	node.Data = &storage.OperationPhaseData{
		Server: server,
	}
	return node
}

func (r phaseBuilder) untaint(server *storage.Server) phase {
	node := r.node(*server, "untaint", "Remove taint from node %q")
	node.Executor = libphase.Untaint
	node.Data = &storage.OperationPhaseData{
		Server: server,
	}
	return node
}

func (r phaseBuilder) uncordon(server *storage.Server) phase {
	node := r.node(*server, "uncordon", "Uncordon node %q")
	node.Executor = libphase.Uncordon
	node.Data = &storage.OperationPhaseData{
		Server: server,
	}
	return node
}

func (r phaseBuilder) endpoints(server *storage.Server) phase {
	node := r.node(*server, "endpoints", "Wait for endpoints on node %q")
	node.Executor = libphase.Endpoints
	node.Data = &storage.OperationPhaseData{
		Server: server,
	}
	return node
}

func (r phaseBuilder) drain(server *storage.Server) phase {
	node := r.node(*server, "drain", "Drain node %q")
	node.Executor = libphase.Drain
	node.Data = &storage.OperationPhaseData{
		Server: server,
	}
	return node
}

func (r phaseBuilder) node(server storage.Server, id, format string) phase {
	return phase{
		ID:          id,
		Description: fmt.Sprintf(format, server.Hostname),
	}
}

// setLeaderElection creates a phase that will change the leader election state in the cluster
// enable - the list of servers to enable election on
// disable - the list of servers to disable election on
// server - The server the phase should be executed on, and used to name the phase
// key - is the identifier of the phase (combined with server.Hostname)
// msg - is a format string used to describe the phase
func setLeaderElection(enable, disable []storage.Server, server storage.Server, id, format string) phase {
	return phase{
		ID:          id,
		Executor:    libphase.Elections,
		Description: fmt.Sprintf(format, server.Hostname),
		Data: &storage.OperationPhaseData{
			Server: &server,
			ElectionChange: &storage.ElectionChange{
				EnableServers:  enable,
				DisableServers: disable,
			},
		},
	}
}

func enable(servers ...storage.Server) []storage.Server  { return servers }
func disable(servers ...storage.Server) []storage.Server { return servers }

type phaseBuilder struct {
	app loc.Locator
}

// AddSequential will append sub-phases which depend one upon another
func (p *phase) AddSequential(sub ...phase) {
	for i := range sub {
		if len(p.Phases) > 0 {
			sub[i].Require(phase(p.Phases[len(p.Phases)-1]))
		}
		p.Phases = append(p.Phases, storage.OperationPhase(sub[i]))
	}
}

// AddParallel will append sub-phases which depend on parent only
func (p *phase) AddParallel(sub ...phase) {
	p.Phases = append(p.Phases, phases(sub).asPhases()...)
}

// Required adds the specified phases reqs as requirements for this phase
func (p *phase) Require(reqs ...phase) *phase {
	for _, req := range reqs {
		p.Requires = append(p.Requires, req.ID)
	}
	return p
}

// ChildLiteral adds the specified sub phase ID as a child of this phase
// and returns the resulting path
func (p *phase) ChildLiteral(sub string) string {
	if p == nil {
		return path.Join("/", sub)
	}
	return path.Join(p.ID, sub)
}

// Root makes the specified phase root
func root(sub phase) phase {
	sub.ID = path.Join("/", sub.ID)
	return sub
}

type phase storage.OperationPhase

func (r phases) asPhases() (result []storage.OperationPhase) {
	result = make([]storage.OperationPhase, 0, len(r))
	for _, phase := range r {
		result = append(result, storage.OperationPhase(phase))
	}
	return result
}

type phases []phase