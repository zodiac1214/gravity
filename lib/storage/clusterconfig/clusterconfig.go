/*
Copyright 2018-2019 Gravitational, Inc.

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

package clusterconfig

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/storage"

	teleservices "github.com/gravitational/teleport/lib/services"
	teleutils "github.com/gravitational/teleport/lib/utils"
	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"
)

// Interface manages cluster configuration
type Interface interface {
	// Resource provides common resource methods
	teleservices.Resource
	// GetKubeletConfig returns the configuration of the kubelet
	GetKubeletConfig() *Kubelet
	// GetAPIServerConfig returns the configuration of the API server
	GetAPIServerConfig() *ControlPlaneComponent
	// GetGlobalConfig returns the global configuration
	GetGlobalConfig() Global
}

// Resource describes the cluster configuration resource
type Resource struct {
	// Kind is a resource kind
	Kind string `json:"kind"`
	// Version is a resource version
	Version string `json:"version"`
	// Metadata specifies resource metadata
	Metadata teleservices.Metadata `json:"metadata"`
	// Spec defines the resource
	Spec Spec `json:"spec"`
}

// GetName returns the name of the resource name
func (r *Resource) GetName() string {
	return r.Metadata.Name
}

// SetName resets the resource name to the specified value
func (r *Resource) SetName(name string) {
	r.Metadata.Name = name
}

// GetMetadata returns resource metadata
func (r *Resource) GetMetadata() teleservices.Metadata {
	return r.Metadata
}

// SetExpiry resets expiration time to the specified value
func (r *Resource) SetExpiry(expires time.Time) {
	r.Metadata.SetExpiry(expires)
}

// Expires returns expiration time
func (r *Resource) Expiry() time.Time {
	return r.Metadata.Expiry()
}

// SetTTL resets the resources's time to live to the specified value
// using given clock implementation
func (r *Resource) SetTTL(clock clockwork.Clock, ttl time.Duration) {
	r.Metadata.SetTTL(clock, ttl)
}

// GetKubeletConfig returns the configuration of the kubelet
func (r *Resource) GetKubeletConfig() *Kubelet {
	return r.Spec.ComponentConfigs.Kubelet
}

// GetAPIServerConfig returns the configuration of the API server
func (r *Resource) GetAPIServerConfig() *ControlPlaneComponent {
	return r.Spec.APIServer
}

// GetGlobalConfig returns the global configuration
func (r *Resource) GetGlobalConfig() Global {
	return r.Spec.Global
}

// Unmarshal unmarshals the resource from either YAML- or JSON-encoded data
func Unmarshal(data []byte) (Interface, error) {
	if len(data) == 0 {
		return nil, trace.BadParameter("empty input")
	}
	jsonData, err := teleutils.ToJSON(data)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	var hdr teleservices.ResourceHeader
	err = json.Unmarshal(jsonData, &hdr)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	switch hdr.Version {
	case "v1":
		var config Resource
		err := teleutils.UnmarshalWithSchema(getSpecSchema(), &config, jsonData)
		if err != nil {
			return nil, trace.BadParameter(err.Error())
		}
		// TODO(dmitri): set namespace explicitly - schema default is ignored
		// as teleservices.Metadata.Namespace is configured as unserializable
		config.Metadata.Namespace = defaults.KubeSystemNamespace
		config.Metadata.Name = constants.ClusterConfigurationMap
		if config.Metadata.Expires != nil {
			teleutils.UTC(config.Metadata.Expires)
		}
		return &config, nil
	}
	return nil, trace.BadParameter(
		"%v resource version %q is not supported", storage.KindClusterConfiguration, hdr.Version)
}

// Marshal marshals this resource as JSON
func Marshal(config Interface, opts ...teleservices.MarshalOption) ([]byte, error) {
	return json.Marshal(config)
}

// Spec defines the cluster configuration resource
type Spec struct {
	// ComponentsConfigs groups component configurations
	ComponentConfigs
	// APIServer specifies API server configuration
	APIServer *ControlPlaneComponent `json:"apiServer,omitempty"`
	// TODO: Scheduler, ControllerManager, Proxy
	// Global describes global configuration
	Global Global `json:"global"`
}

// ComponentsConfigs groups component configurations
type ComponentConfigs struct {
	// Kubelet defines kubelet configuration
	Kubelet *Kubelet `json:"kubelet,omitempty"`
}

// Kubelet defines kubelet configuration
type Kubelet struct {
	json.RawMessage
}

// ControlPlaneComponent defines configuration of a control plane component
type ControlPlaneComponent struct {
	json.RawMessage
}

// Global describes global configuration
type Global struct {
	// CloudProvider specifies the cloud provider
	CloudProvider string `json:"cloudProvider"`
	// CloudConfig describes the cloud configuration.
	// The configuration is provider-specific
	CloudConfig CloudConfig `json:"cloudConfig"`
}

func (r CloudConfig) MarshalJSON() ([]byte, error) {
	bytes, err := json.Marshal(r.Config)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return bytes, nil
}

func (r *CloudConfig) UnmarshalJSON(data []byte) error {
	var config string
	if err := json.Unmarshal(data, &config); err != nil {
		return trace.Wrap(err)
	}
	r.Config = config
	return nil
}

// CloudConfig describes cluster cloud configuration
type CloudConfig struct {
	// Config specifies cloud configuration verbatim
	Config string
}

// specSchemaTemplate is JSON schema for the cluster configuration resource
const specSchemaTemplate = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["kind", "spec", "version"],
  "properties": {
    "kind": {"type": "string"},
    "version": {"type": "string", "default": "v1"},
    "metadata": {
      "default": {},
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "name": {"type": "string", "default": "%v"},
        "namespace": {"type": "string", "default": "%v"},
        "description": {"type": "string"},
        "expires": {"type": "string"},
        "labels": {
          "type": "object",
          "patternProperties": {
             "^[a-zA-Z/.0-9_-]$":  {"type": "string"}
          }
        }
      }
    },
    "spec": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "global": {
          "type": "object",
          "properties": {
            "cloudProvider": {"type": "string"},
            "cloudConfig": {"type": "string"}
          }
        },
        "kubelet": {"type": ["object", "null"]},
        "scheduler": {"type": ["object", "null"]},
        "proxy": {"type": ["object", "null"]},
        "controller-manager": {"type": ["object", "null"]},
        "apiserver": {"type": ["object", "null"]}
      }
    }
  }
}`

// getSpecSchema returns the formatted JSON schema for the cluster configuration resource
func getSpecSchema() string {
	return fmt.Sprintf(specSchemaTemplate,
		constants.ClusterConfigurationMap, defaults.KubeSystemNamespace)
}
