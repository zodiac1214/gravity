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

package storage

import (
	"time"

	"github.com/gravitational/gravity/lib/loc"
	"github.com/gravitational/gravity/lib/utils"

	"github.com/gravitational/trace"
)

// OperationPlan represents a plan of an operation as a collection of phases
type OperationPlan struct {
	// OperationID is the ID of the operation the plan belongs to
	OperationID string `json:"operation_id"`
	// OperationType is the type of the operation the plan belongs to
	OperationType string `json:"operation_type"`
	// AccountID is the ID of the account initiated the operation
	AccountID string `json:"account_id"`
	// ClusterName is the name of the cluster for the operation
	ClusterName string `json:"cluster_name"`
	// Phases is the list of phases the plan consists of
	Phases []OperationPhase `json:"phases"`
	// Servers is the list of all cluster servers
	Servers []Server `json:"servers"`
	// GravityPackage is updated gravity package locator
	GravityPackage loc.Locator `json:"gravity_package"`
	// CreatedAt is the plan creation timestamp
	CreatedAt time.Time `json:"created_at"`
	// DNSConfig specifies cluster DNS configuration
	DNSConfig DNSConfig `json:"dns_config"`
}

// Check makes sure operation plan is valid
func (p OperationPlan) Check() error {
	if p.OperationID == "" {
		return trace.BadParameter("missing OperationID")
	}
	if p.OperationType == "" {
		return trace.BadParameter("missing OperationType")
	}
	if p.ClusterName == "" {
		return trace.BadParameter("missing ClusterName")
	}
	return nil
}

// OperationPhase represents a single operation plan phase
type OperationPhase struct {
	// ID is the ID of the phase within operation
	ID string `json:"id"`
	// Executor is function which should execute this phase
	Executor string `json:"executor"`
	// Description is verbose description of the phase
	Description string `json:"description,omitepty" yaml:"description,omitempty"`
	// State is the current phase state
	State string `json:"state,omitempty" yaml:"state,omitempty"`
	// Step maps the phase to its corresponding step on the UI progress screen
	Step int `json:"step"`
	// Phases is the list of sub-phases the phase consists of
	Phases []OperationPhase `json:"phases,omitempty" yaml:"phases,omitempty"`
	// Requires is a list of phase names that need to be
	// completed before this phase can be executed
	Requires []string `json:"requires,omitempty" yaml:"requires,omitempty"`
	// Parallel enables parallel execution of sub-phases
	Parallel bool `json:"parallel"`
	// Updated is the last phase update time
	Updated time.Time `json:"updated,omitempty" yaml:"updated,omitempty"`
	// Data is optional phase-specific data attached to the phase
	Data *OperationPhaseData `json:"data,omitempty" yaml:"data,omitempty"`
	// Error is the error that happened during phase execution
	Error *trace.RawTrace `json:"error"`
}

// OperationPhaseData represents data attached to an operation phase
type OperationPhaseData struct {
	// Server is the server the phase operates on
	Server *Server `json:"server,omitempty" yaml:"server,omitempty"`
	// ExecServer is an optional server the phase is supposed to be executed on.
	// If unspecified, the Server is used
	ExecServer *Server `json:"exec_server,omitempty" yaml:"exec_server,omitempty"`
	// Master is the selected master node the phase needs access to
	Master *Server `json:"master,omitempty" yaml:"master,omitempty"`
	// Package is the package locator for the phase, e.g. update package
	Package *loc.Locator `json:"package,omitempty" yaml:"package,omitempty"`
	// Labels can optionally identify the package
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	// InstalledPackage references the installed application package
	InstalledPackage *loc.Locator `json:"installed_package,omitempty" yaml:"installed_package,omitempty"`
	// RuntimePackage references the update runtime package
	RuntimePackage *loc.Locator `json:"runtime_package,omitempty" yaml:"runtime_package,omitempty"`
	// UpdatePlanet indicates whether the planet needs to be updated during bootstrap
	UpdatePlanet bool `json:"update_planet" yaml:"update_planet"`
	// ElectionChange describes changes to make to cluster elections
	ElectionChange *ElectionChange `json:"election_status,omitempty" yaml:"election_status,omitempty"`
	// Agent is the credentials of the agent that should be logged in
	Agent *LoginEntry `json:"agent,omitempty" yaml:"agent,omitempty"`
	// Resources is the Kubernetes resources to create
	Resources []byte `json:"resources,omitempty" yaml:"resources,omitempty"`
	// License is the cluster license
	License []byte `json:"license,omitempty" yaml:"license,omitempty"`
	// TrustedCluster is the resource data for a trusted cluster representing an Ops Center
	TrustedCluster []byte `json:"trusted_cluster_resource,omitempty" yaml:"trusted_cluster_resource,omitempty"`
	// ServiceUser specifies the optional service user to use as a context
	// for file operations
	ServiceUser *OSUser `json:"service_user,omitempty" yaml:"service_user,omitempty"`
	// Data is arbitrary text data to provide to a phase executor
	Data string `json:"data,omitempty" yaml:"data,omitempty"`
}

// ElectionChange describes changes to make to cluster elections
type ElectionChange struct {
	// EnableServers is a list of servers that we should enable elections on
	EnableServers []Server `json:"enable_server,omitempty" yaml:"enable_server,omitempty"`
	// DisableServers is a list of servers that we should disable elections on
	DisableServers []Server `json:"disable_servers,omitempty" yaml:"disable_servers,omitempty"`
}

// PlanChange represents a single operation plan state change
type PlanChange struct {
	// ID is the change ID
	ID string `json:"id"`
	// ClusterName is the name of the cluster for the operation
	ClusterName string `json:"cluster_name"`
	// OperationID is the ID of the operation this change is for
	OperationID string `json:"operation_id"`
	// PhaseID is the ID of the phase the change refers to
	PhaseID string `json:"phase_id"`
	// NewState is the state the phase moved into
	NewState string `json:"new_state"`
	// Created is the change timestamp
	Created time.Time `json:"created"`
	// Error is the error that happened during phase execution
	Error *trace.RawTrace `json:"error"`
}

// PlanChangelog is a list of plan state changes
type PlanChangelog []PlanChange

// Latest returns the most recent plan change entry for the specified phase
func (c PlanChangelog) Latest(phaseID string) *PlanChange {
	var latest *PlanChange
	for i, change := range c {
		if change.PhaseID != phaseID {
			continue
		}
		if latest == nil || change.Created.After(latest.Created) {
			latest = &(c[i])
		}
	}
	return latest
}

// HasSubphases returns true if the phase has 1 or more subphases
func (p OperationPhase) HasSubphases() bool {
	return len(p.Phases) > 0
}

// IsUnstarted returns true if the phase is in "unstarted" state
func (p OperationPhase) IsUnstarted() bool {
	return p.GetState() == OperationPhaseStateUnstarted
}

// IsInProgress returns true if the phase is in "in progress" state
func (p OperationPhase) IsInProgress() bool {
	return p.GetState() == OperationPhaseStateInProgress
}

// IsCompleted returns true if the phase is in "completed" state
func (p OperationPhase) IsCompleted() bool {
	return p.GetState() == OperationPhaseStateCompleted
}

// IsFailed returns true if the phase is in "failed" state
func (p OperationPhase) IsFailed() bool {
	return p.GetState() == OperationPhaseStateFailed
}

// IsRolledBack returns true if the phase is in "rolled back" state
func (p OperationPhase) IsRolledBack() bool {
	return p.GetState() == OperationPhaseStateRolledBack
}

// GetLastUpdateTime returns the phase last updated time
func (p OperationPhase) GetLastUpdateTime() time.Time {
	if len(p.Phases) == 0 {
		return p.Updated
	}
	last := p.Phases[0].GetLastUpdateTime()
	for _, phase := range p.Phases[1:] {
		if phase.GetLastUpdateTime().After(last) {
			last = phase.GetLastUpdateTime()
		}
	}
	return last
}

// GetState returns the phase state based on the states of all its subphases
func (p OperationPhase) GetState() string {
	// if the phase doesn't have subphases, then just return its state from property
	if len(p.Phases) == 0 {
		if p.State == "" {
			return OperationPhaseStateUnstarted
		}
		return p.State
	}
	// otherwise collect states of all subphases
	states := utils.NewStringSet()
	for _, phase := range p.Phases {
		states.Add(phase.GetState())
	}
	// if all subphases are in the same state, then this phase is in this state as well
	if len(states) == 1 {
		return states.Slice()[0]
	}
	// if any of the subphases is failed or rolled back then this phase is failed
	if states.Has(OperationPhaseStateFailed) || states.Has(OperationPhaseStateRolledBack) {
		return OperationPhaseStateFailed
	}
	// otherwise we consider the whole phase to be in progress because it hasn't
	// converged to a single state yet
	return OperationPhaseStateInProgress
}

const (
	// OperationPhaseStateUnstarted means that the phase or all of its subphases haven't started executing yet
	OperationPhaseStateUnstarted = "unstarted"
	// OperationPhaseStateInProgress means that the phase or any of its subphases haven't reached any of the final states yet
	OperationPhaseStateInProgress = "in_progress"
	// OperationPhaseStateCompleted means that the phase or all of its subphases have been completed
	OperationPhaseStateCompleted = "completed"
	// OperationPhaseStateFailed means that the phase or all of its subphases have failed
	OperationPhaseStateFailed = "failed"
	// OperationPhaseStateRolledBack means that the phase or all of its subphases have been rolled back
	OperationPhaseStateRolledBack = "rolled_back"
)
