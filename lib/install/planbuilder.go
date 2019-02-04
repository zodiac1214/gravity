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

package install

import (
	"fmt"
	"strconv"

	"github.com/gravitational/gravity/lib/app"
	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/fsm"
	"github.com/gravitational/gravity/lib/install/phases"
	"github.com/gravitational/gravity/lib/loc"
	"github.com/gravitational/gravity/lib/ops"
	"github.com/gravitational/gravity/lib/pack"
	"github.com/gravitational/gravity/lib/schema"
	"github.com/gravitational/gravity/lib/storage"

	"github.com/gravitational/trace"
)

// PlanBuilder builds operation plan phases
type PlanBuilder struct {
	// Application is the app being installed
	Application app.Application
	// Runtime is the Runtime of the app being installed
	Runtime app.Application
	// TeleportPackage is the runtime teleport package
	TeleportPackage loc.Locator
	// RBACPackage is the runtime rbac app package
	RBACPackage loc.Locator
	// GravitySitePackage is the gravity-site app package
	GravitySitePackage loc.Locator
	// DNSAppPackage is the dns-app app package
	DNSAppPackage loc.Locator
	// Masters is the list of master nodes
	Masters []storage.Server
	// Nodes is the list of regular nodes
	Nodes []storage.Server
	// Master is one of the master nodes
	Master storage.Server
	// AdminAgent is the cluster agent with admin privileges
	AdminAgent storage.LoginEntry
	// RegularAgent is the cluster agent with non-admin privileges
	RegularAgent storage.LoginEntry
	// ServiceUser is the cluster system user
	ServiceUser storage.OSUser
}

// AddChecksPhase appends preflight checks phase to the provided plan
func (b *PlanBuilder) AddChecksPhase(plan *storage.OperationPlan) {
	plan.Phases = append(plan.Phases, storage.OperationPhase{
		ID:          phases.ChecksPhase,
		Description: "Execute preflight checks",
		Data: &storage.OperationPhaseData{
			Package: &b.Application.Package,
		},
		Step: 0,
	})
}

// AddConfigurePhase appends package configuration phase to the provided plan
func (b *PlanBuilder) AddConfigurePhase(plan *storage.OperationPlan) {
	plan.Phases = append(plan.Phases, storage.OperationPhase{
		ID:          phases.ConfigurePhase,
		Description: "Configure packages for all nodes",
		Requires:    fsm.RequireIfPresent(plan, phases.InstallerPhase, phases.DecryptPhase),
		Step:        3,
	})
}

// AddBootstrapPhase appends nodes bootstrap phase to the provided plan
func (b *PlanBuilder) AddBootstrapPhase(plan *storage.OperationPlan) {
	var bootstrapPhases []storage.OperationPhase
	allNodes := append(b.Masters, b.Nodes...)
	for i, node := range allNodes {
		var description string
		var agent *storage.LoginEntry
		if node.ClusterRole == string(schema.ServiceRoleMaster) {
			description = "Bootstrap master node %v"
			agent = &b.AdminAgent
		} else {
			description = "Bootstrap node %v"
			agent = &b.RegularAgent
		}
		bootstrapPhases = append(bootstrapPhases, storage.OperationPhase{
			ID:          fmt.Sprintf("%v/%v", phases.BootstrapPhase, node.Hostname),
			Description: fmt.Sprintf(description, node.Hostname),
			Data: &storage.OperationPhaseData{
				Server:      &allNodes[i],
				ExecServer:  &allNodes[i],
				Package:     &b.Application.Package,
				Agent:       agent,
				ServiceUser: &b.ServiceUser,
			},
			Step: 3,
		})
	}
	plan.Phases = append(plan.Phases, storage.OperationPhase{
		ID:          phases.BootstrapPhase,
		Description: "Bootstrap all nodes",
		Phases:      bootstrapPhases,
		Parallel:    true,
		Step:        3,
	})
}

// AddPullPhase appends package download phase to the provided plan
func (b *PlanBuilder) AddPullPhase(plan *storage.OperationPlan) {
	var pullPhases []storage.OperationPhase
	allNodes := append(b.Masters, b.Nodes...)
	for i, node := range allNodes {
		var description string
		if node.ClusterRole == string(schema.ServiceRoleMaster) {
			description = "Pull packages on master node %v"
		} else {
			description = "Pull packages on node %v"
		}
		pullPhases = append(pullPhases, storage.OperationPhase{
			ID:          fmt.Sprintf("%v/%v", phases.PullPhase, node.Hostname),
			Description: fmt.Sprintf(description, node.Hostname),
			Data: &storage.OperationPhaseData{
				Server:      &allNodes[i],
				ExecServer:  &allNodes[i],
				Package:     &b.Application.Package,
				ServiceUser: &b.ServiceUser,
			},
			Requires: []string{phases.ConfigurePhase, phases.BootstrapPhase},
			Step:     3,
		})
	}
	plan.Phases = append(plan.Phases, storage.OperationPhase{
		ID:          phases.PullPhase,
		Description: "Pull configured packages",
		Phases:      pullPhases,
		Requires:    []string{phases.ConfigurePhase, phases.BootstrapPhase},
		Parallel:    true,
		Step:        3,
	})
}

// AddMastersPhase appends master nodes system installation phase to the provided plan
func (b *PlanBuilder) AddMastersPhase(plan *storage.OperationPlan) error {
	var masterPhases []storage.OperationPhase
	for i, node := range b.Masters {
		planetPackage, err := b.Application.Manifest.RuntimePackageForProfile(node.Role)
		if err != nil {
			return trace.Wrap(err)
		}
		masterPhases = append(masterPhases, storage.OperationPhase{
			ID: fmt.Sprintf("%v/%v", phases.MastersPhase, node.Hostname),
			Description: fmt.Sprintf("Install system software on master node %v",
				node.Hostname),
			Phases: []storage.OperationPhase{
				{
					ID: fmt.Sprintf("%v/%v/teleport", phases.MastersPhase, node.Hostname),
					Description: fmt.Sprintf("Install system package %v:%v on master node %v",
						b.TeleportPackage.Name, b.TeleportPackage.Version, node.Hostname),
					Data: &storage.OperationPhaseData{
						Server:     &b.Masters[i],
						ExecServer: &b.Masters[i],
						Package:    &b.TeleportPackage,
					},
					Requires: []string{fmt.Sprintf("%v/%v", phases.PullPhase, node.Hostname)},
					Step:     4,
				},
				{
					ID: fmt.Sprintf("%v/%v/planet", phases.MastersPhase, node.Hostname),
					Description: fmt.Sprintf("Install system package %v:%v on master node %v",
						planetPackage.Name, planetPackage.Version, node.Hostname),
					Data: &storage.OperationPhaseData{
						Server:     &b.Masters[i],
						ExecServer: &b.Masters[i],
						Package:    planetPackage,
						Labels:     pack.RuntimePackageLabels,
					},
					Requires: []string{fmt.Sprintf("%v/%v", phases.PullPhase, node.Hostname)},
					Step:     4,
				},
			},
			Requires: []string{fmt.Sprintf("%v/%v", phases.PullPhase, node.Hostname)},
			Step:     4,
		})
	}
	plan.Phases = append(plan.Phases, storage.OperationPhase{
		ID:          phases.MastersPhase,
		Description: "Install system software on master nodes",
		Phases:      masterPhases,
		Requires:    []string{phases.PullPhase},
		Parallel:    true,
		Step:        4,
	})
	return nil
}

// AddNodesPhase appends regular nodes system installation phase to the provided plan
func (b *PlanBuilder) AddNodesPhase(plan *storage.OperationPlan) error {
	var nodePhases []storage.OperationPhase
	for i, node := range b.Nodes {
		planetPackage, err := b.Application.Manifest.RuntimePackageForProfile(node.Role)
		if err != nil {
			return trace.Wrap(err)
		}
		nodePhases = append(nodePhases, storage.OperationPhase{
			ID: fmt.Sprintf("%v/%v", phases.NodesPhase, node.Hostname),
			Description: fmt.Sprintf("Install system software on node %v",
				node.Hostname),
			Phases: []storage.OperationPhase{
				{
					ID: fmt.Sprintf("%v/%v/teleport", phases.NodesPhase, node.Hostname),
					Description: fmt.Sprintf("Install system package %v:%v on node %v",
						b.TeleportPackage.Name, b.TeleportPackage.Version, node.Hostname),
					Data: &storage.OperationPhaseData{
						Server:     &b.Nodes[i],
						ExecServer: &b.Nodes[i],
						Package:    &b.TeleportPackage,
					},
					Requires: []string{fmt.Sprintf("%v/%v", phases.PullPhase, node.Hostname)},
					Step:     4,
				},
				{
					ID: fmt.Sprintf("%v/%v/planet", phases.NodesPhase, node.Hostname),
					Description: fmt.Sprintf("Install system package %v:%v on node %v",
						planetPackage.Name, planetPackage.Version, node.Hostname),
					Data: &storage.OperationPhaseData{
						Server:     &b.Nodes[i],
						ExecServer: &b.Nodes[i],
						Package:    planetPackage,
						Labels:     pack.RuntimePackageLabels,
					},
					Requires: []string{fmt.Sprintf("%v/%v", phases.PullPhase, node.Hostname)},
					Step:     4,
				},
			},
			Requires: []string{fmt.Sprintf("%v/%v", phases.PullPhase, node.Hostname)},
			Step:     4,
		})
	}
	plan.Phases = append(plan.Phases, storage.OperationPhase{
		ID:          phases.NodesPhase,
		Description: "Install system software on regular nodes",
		Phases:      nodePhases,
		Requires:    []string{phases.PullPhase},
		Parallel:    true,
		Step:        4,
	})
	return nil
}

// AddWaitPhase appends planet startup wait phase to the provided plan
func (b *PlanBuilder) AddWaitPhase(plan *storage.OperationPlan) {
	plan.Phases = append(plan.Phases, storage.OperationPhase{
		ID:          phases.WaitPhase,
		Description: "Wait for system services to start on all nodes",
		Requires:    fsm.RequireIfPresent(plan, phases.MastersPhase, phases.NodesPhase),
		Data: &storage.OperationPhaseData{
			Server: &b.Master,
		},
		Step: 4,
	})
}

// AddRBACPhase appends K8s RBAC initialization phase to the provided plan
func (b *PlanBuilder) AddRBACPhase(plan *storage.OperationPlan) {
	plan.Phases = append(plan.Phases, storage.OperationPhase{
		ID:          phases.RBACPhase,
		Description: "Bootstrap Kubernetes roles and PSPs",
		Data: &storage.OperationPhaseData{
			Server:  &b.Master,
			Package: &b.RBACPackage,
		},
		Requires: []string{phases.WaitPhase},
		Step:     4,
	})
}

// AddResourcesPhase appends K8s resources initialization phase to the provided plan
func (b *PlanBuilder) AddResourcesPhase(plan *storage.OperationPlan, resources []byte) {
	plan.Phases = append(plan.Phases, storage.OperationPhase{
		ID:          phases.ResourcesPhase,
		Description: "Create user-supplied Kubernetes resources",
		Data: &storage.OperationPhaseData{
			Server:    &b.Master,
			Resources: resources,
		},
		Requires: []string{phases.RBACPhase},
		Step:     4,
	})
}

// AddExportPhase appends Docker images export phase to the provided plan
func (b *PlanBuilder) AddExportPhase(plan *storage.OperationPlan) {
	var exportPhases []storage.OperationPhase
	for i, node := range b.Masters {
		exportPhases = append(exportPhases, storage.OperationPhase{
			ID: fmt.Sprintf("%v/%v", phases.ExportPhase, node.Hostname),
			Description: fmt.Sprintf("Populate Docker registry on master node %v",
				node.Hostname),
			Data: &storage.OperationPhaseData{
				Server:     &b.Masters[i],
				ExecServer: &b.Masters[i],
				Package:    &b.Application.Package,
			},
			Requires: []string{phases.WaitPhase},
			Step:     4,
		})
	}
	plan.Phases = append(plan.Phases, storage.OperationPhase{
		ID:          phases.ExportPhase,
		Description: "Export applications layers to Docker registries",
		Phases:      exportPhases,
		Requires:    []string{phases.WaitPhase},
		Parallel:    true,
		Step:        4,
	})
}

// AddRuntimePhase appends system applications installation phase to the provided plan
func (b *PlanBuilder) AddRuntimePhase(plan *storage.OperationPlan) error {
	runtimeLocators, err := app.GetDirectDeps(b.Runtime)
	if err != nil {
		return trace.Wrap(err)
	}
	var runtimePhases []storage.OperationPhase
	for i, locator := range runtimeLocators {
		if b.skipDependency(locator) {
			continue
		}
		runtimePhases = append(runtimePhases, storage.OperationPhase{
			ID: fmt.Sprintf("%v/%v", phases.RuntimePhase, locator.Name),
			Description: fmt.Sprintf("Install system application %v:%v",
				locator.Name, locator.Version),
			Data: &storage.OperationPhaseData{
				Server:      &b.Master,
				Package:     &runtimeLocators[i],
				ServiceUser: &b.ServiceUser,
			},
			Requires: []string{phases.RBACPhase},
			Step:     5,
		})
	}
	plan.Phases = append(plan.Phases, storage.OperationPhase{
		ID:          phases.RuntimePhase,
		Description: "Install system applications",
		Phases:      runtimePhases,
		Requires:    []string{phases.RBACPhase},
		Step:        5,
	})
	return nil
}

// AddApplicationPhase appends user application installation phase to the provided plan
func (b *PlanBuilder) AddApplicationPhase(plan *storage.OperationPlan) error {
	applicationLocators, err := app.GetDirectDeps(b.Application)
	if err != nil {
		return trace.Wrap(err)
	}
	var applicationPhases []storage.OperationPhase
	for i, locator := range applicationLocators {
		applicationPhases = append(applicationPhases, storage.OperationPhase{
			ID: fmt.Sprintf("%v/%v", phases.AppPhase, locator.Name),
			Description: fmt.Sprintf("Install application %v:%v",
				locator.Name, locator.Version),
			Data: &storage.OperationPhaseData{
				Server:      &b.Master,
				Package:     &applicationLocators[i],
				ServiceUser: &b.ServiceUser,
			},
			Requires: []string{phases.RuntimePhase},
			Step:     6,
		})
	}
	plan.Phases = append(plan.Phases, storage.OperationPhase{
		ID:          phases.AppPhase,
		Description: "Install user application",
		Phases:      applicationPhases,
		Requires:    []string{phases.RuntimePhase},
		Step:        6,
	})
	return nil
}

// AddEnableElectionPhase appends leader election enabling phase to the provided plan
func (b *PlanBuilder) AddEnableElectionPhase(plan *storage.OperationPlan) {
	plan.Phases = append(plan.Phases, storage.OperationPhase{
		ID:          phases.EnableElectionPhase,
		Description: "Enable elections",
		Requires:    []string{phases.AppPhase},
		Data: &storage.OperationPhaseData{
			Server: &b.Master,
		},
		Step: 9,
	})
}

// GetPlanBuilder returns a new plan builder for this installer and provided
// operation that can be used to build operation plan phases
func (i *Installer) GetPlanBuilder(cluster ops.Site, op ops.SiteOperation) (*PlanBuilder, error) {
	// determine which app and runtime are being installed
	application, err := i.Apps.GetApp(i.AppPackage)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	base := application.Manifest.Base()
	if base == nil {
		return nil, trace.BadParameter("application %v does not have a runtime",
			i.AppPackage)
	}
	runtime, err := i.Apps.GetApp(*base)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	// sort out all servers into masters and nodes
	masters, nodes := fsm.SplitServers(op.Servers)
	if len(masters) == 0 {
		return nil, trace.BadParameter(
			"at least one master server is required: %v", op.Servers)
	}
	// pick one of master nodes for executing phases that need to
	// be executed from any master node
	master := masters[0]
	// prepare information about application packages that will be required
	// during plan generation
	teleportPackage, err := application.Manifest.Dependencies.ByName(
		constants.TeleportPackage)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	rbacPackage, err := application.Manifest.Dependencies.ByName(
		constants.BootstrapConfigPackage)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	gravitySitePackage, err := application.Manifest.Dependencies.ByName(
		constants.GravitySitePackage)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	dnsAppPackage, err := application.Manifest.Dependencies.ByName(
		constants.DNSAppPackage)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	// retrieve cluster agents
	adminAgent, err := i.Operator.GetClusterAgent(ops.ClusterAgentRequest{
		AccountID:   op.AccountID,
		ClusterName: op.SiteDomain,
		Admin:       true,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	regularAgent, err := i.Operator.GetClusterAgent(ops.ClusterAgentRequest{
		AccountID:   op.AccountID,
		ClusterName: op.SiteDomain,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &PlanBuilder{
		Application:        *application,
		Runtime:            *runtime,
		TeleportPackage:    *teleportPackage,
		RBACPackage:        *rbacPackage,
		GravitySitePackage: *gravitySitePackage,
		DNSAppPackage:      *dnsAppPackage,
		Masters:            masters,
		Nodes:              nodes,
		Master:             master,
		AdminAgent:         *adminAgent,
		RegularAgent:       *regularAgent,
		ServiceUser: storage.OSUser{
			Name: i.Config.ServiceUser.Name,
			UID:  strconv.Itoa(i.Config.ServiceUser.UID),
			GID:  strconv.Itoa(i.Config.ServiceUser.GID),
		},
	}, nil
}

// splitServers splits the provided servers into masters and nodes
func splitServers(servers []storage.Server, app app.Application) (masters []storage.Server, nodes []storage.Server, err error) {
	count := 0
	for _, server := range servers {
		profile, err := app.Manifest.NodeProfiles.ByName(server.Role)
		if err != nil {
			return nil, nil, trace.Wrap(err)
		}
		switch profile.ServiceRole {
		case schema.ServiceRoleMaster, "":
			if count < defaults.MaxMasterNodes {
				server.ClusterRole = string(schema.ServiceRoleMaster)
				masters = append(masters, server)
				count++
			} else {
				server.ClusterRole = string(schema.ServiceRoleNode)
				nodes = append(nodes, server)
			}
		case schema.ServiceRoleNode:
			server.ClusterRole = string(schema.ServiceRoleNode)
			nodes = append(nodes, server)
		default:
			return nil, nil, trace.BadParameter(
				"unknown cluster role %q for node profile %q",
				profile.ServiceRole, server.Role)
		}
	}
	return masters, nodes, nil
}

// skipDependency returns true if the dependency package specified by dep
// should be skipped when installing the provided application
func (b *PlanBuilder) skipDependency(dep loc.Locator) bool {
	// rbac-app is installed separately
	if dep.Name == constants.BootstrapConfigPackage {
		return true
	}
	// do not install bandwagon unless the app uses it in its post-install
	if dep.Name == defaults.BandwagonPackageName {
		setup := b.Application.Manifest.SetupEndpoint()
		if setup == nil || setup.ServiceName != defaults.BandwagonServiceName {
			return true
		}
	}
	return false
}
