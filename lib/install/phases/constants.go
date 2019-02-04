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

package phases

const (
	// ChecksPhase is a phase that executes preflight checks
	ChecksPhase = "/checks"
	// InstallerPhase is a phase that downloads installer from Ops Center
	InstallerPhase = "/installer"
	// DecryptPhase is a phase that decrypts encrypted packages
	DecryptPhase = "/decrypt"
	// ConfigurePhase is a phase that configures cluster packages
	ConfigurePhase = "/configure"
	// BootstrapPhase is a phase that prepares the nodes for installation
	BootstrapPhase = "/bootstrap"
	// PullPhase is a phase that pulls configured packages
	PullPhase = "/pull"
	// MastersPhase is a phase that installs system software on master nodes
	MastersPhase = "/masters"
	// NodesPhase is a phase that installs system software on regular nodes
	NodesPhase = "/nodes"
	// WaitPhase is a phase that waits for planet to start
	WaitPhase = "/wait"
	// RBACPhase is a phase that creates Kubernetes RBAC resources
	RBACPhase = "/rbac"
	// ResourcesPhase is a phase that creates user supplied Kubernetes resources
	ResourcesPhase = "/resources"
	// GravityResourcesPhase is a phase that creates user supplied Gravity resources
	GravityResourcesPhase = "/gravity-resources"
	// ExportPhase is a phase that exports application layers to registries
	ExportPhase = "/export"
	// RuntimePhase is a phase that installs system applications
	RuntimePhase = "/runtime"
	// AppPhase is a phase that installs user application
	AppPhase = "/app"
	// EnableElectionPhase turns on election participation for master nodes
	// at the end of the installation. During installation, the election is
	// off with a single master
	EnableElectionPhase = "/election"
)
