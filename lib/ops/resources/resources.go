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

package resources

import (
	"encoding/json"
	"io"

	"github.com/gravitational/gravity/lib/app/resources"
	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/modules"
	"github.com/gravitational/gravity/lib/storage"
	"github.com/gravitational/gravity/lib/utils"

	teleservices "github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/trace"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// Resources defines methods each specific resource controller should implement
//
// The reason it exists is because gravity and tele CLI tools each support
// their own set of resources.
type Resources interface {
	// Create creates the provided resource
	Create(CreateRequest) error
	// GetCollection retrieves a collection of specified resources
	GetCollection(ListRequest) (Collection, error)
	// Remove removes the specified resource
	Remove(RemoveRequest) error
}

// ResourceControl allows to create/list/remove resources
//
// A list of supported resources is determined by the specific controller
// it is initialized with.
type ResourceControl struct {
	// Resources is the specific resource controller
	Resources
}

// CreateRequest describes a request to create a resource
type CreateRequest struct {
	// Resource is the resource to create
	Resource teleservices.UnknownResource
	// Upsert is whether to update a resource
	Upsert bool
	// User is the user to create resource for
	User string
}

// Check validates the request
func (r CreateRequest) Check() error {
	if r.Resource.Kind == "" {
		return trace.BadParameter("resource kind is mandatory")
	}
	return nil
}

// ListRequest describes a request to list resources
type ListRequest struct {
	// Kind is kind of the resource
	Kind string
	// Name is name of the resource
	Name string
	// WithSecrets is whether to display hidden resource fields
	WithSecrets bool
	// User is the resource owner
	User string
}

// Check validates the request
func (r *ListRequest) Check() error {
	if r.Kind == "" {
		return trace.BadParameter("resource kind is mandatory")
	}
	kind := modules.Get().CanonicalKind(r.Kind)
	resources := modules.Get().SupportedResources()
	if !utils.StringInSlice(resources, kind) {
		return trace.BadParameter("unknown resource kind %q", r.Kind)
	}
	r.Kind = kind
	return nil
}

// RemoveRequest describes a request to remove a resource
type RemoveRequest struct {
	// Kind is kind of the resource
	Kind string
	// Name is name of the resource
	Name string
	// Force is whether to suppress not found errors
	Force bool
	// User is the resource owner
	User string
}

// Check validates the request
func (r *RemoveRequest) Check() error {
	if r.Kind == "" {
		return trace.BadParameter("resource kind is mandatory")
	}
	kind := modules.Get().CanonicalKind(r.Kind)
	resources := modules.Get().SupportedResourcesToRemove()
	if !utils.StringInSlice(resources, kind) {
		return trace.BadParameter("unknown resource kind %q", r.Kind)
	}
	switch kind {
	case storage.KindAlertTarget:
	case storage.KindSMTPConfig:
	default:
		if r.Name == "" {
			return trace.BadParameter("resource name is mandatory")
		}
	}
	r.Kind = kind
	return nil
}

// Collection represents printable collection of resources
// that can serialize itself into various format
type Collection interface {
	// WriteText serializes collection in human-friendly text format
	WriteText(w io.Writer) error
	// WriteJSON serializes collection into JSON format
	WriteJSON(w io.Writer) error
	// WriteYAML serializes collection into YAML format
	WriteYAML(w io.Writer) error
	// Resources returns the resources collection in the generic format
	Resources() ([]teleservices.UnknownResource, error)
}

// NewControl creates a new resource control instance
func NewControl(resources Resources) *ResourceControl {
	return &ResourceControl{
		Resources: resources,
	}
}

// Create creates all resources found in the provided data
func (r *ResourceControl) Create(reader io.Reader, upsert bool, user string) (err error) {
	decoder := yaml.NewYAMLOrJSONDecoder(reader, defaults.DecoderBufferSize)
	empty := true
	for err == nil {
		var raw teleservices.UnknownResource
		err = decoder.Decode(&raw)
		if err != nil {
			break
		}
		empty = false
		err = r.Resources.Create(CreateRequest{
			Resource: raw,
			Upsert:   upsert,
			User:     user,
		})
	}
	if err != io.EOF {
		return trace.Wrap(err)
	}
	if empty {
		return trace.BadParameter("no resources found, empty input?")
	}
	return nil
}

// Get retrieves the specified resource collection and outputs it
func (r *ResourceControl) Get(w io.Writer, kind, name string, withSecrets bool, format constants.Format, user string) error {
	collection, err := r.Resources.GetCollection(ListRequest{
		Kind:        kind,
		Name:        name,
		WithSecrets: withSecrets,
		User:        user,
	})
	if err != nil {
		return trace.Wrap(err)
	}
	switch format {
	case constants.EncodingText:
		return collection.WriteText(w)
	case constants.EncodingJSON:
		return collection.WriteJSON(w)
	case constants.EncodingYAML:
		return collection.WriteYAML(w)
	}
	return trace.BadParameter("unsupported format %q, supported are: %v",
		format, constants.OutputFormats)
}

// Remove removes the specified resource
func (r *ResourceControl) Remove(kind, name string, force bool, user string) error {
	err := r.Resources.Remove(RemoveRequest{
		Kind:  kind,
		Name:  name,
		Force: force,
		User:  user,
	})
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

// Split interprets the given reader r as a list of resources and splits
// them in two groups: Kubernetes and Gravity resources
func Split(r io.Reader) (kubernetesResources []runtime.Object, gravityResources []storage.UnknownResource, err error) {
	err = ForEach(r, func(resource storage.UnknownResource) error {
		if isKubernetesResource(resource) {
			// reinterpret as a Kubernetes resource
			var kResource resources.Unknown
			if err := json.Unmarshal(resource.Raw, &kResource); err != nil {
				return trace.Wrap(err)
			}
			kubernetesResources = append(kubernetesResources, &kResource)
		} else {
			gravityResources = append(gravityResources, resource)
		}
		return nil
	})
	if err != nil {
		return nil, nil, trace.Wrap(err)
	}
	return kubernetesResources, gravityResources, nil
}

// ForEach interprets the given reader r as a collection of Gravity resources
// and invokes the specified handler for each resource in the list.
// Returns the first encountered error
func ForEach(r io.Reader, handler ResourceFunc) (err error) {
	decoder := yaml.NewYAMLOrJSONDecoder(r, defaults.DecoderBufferSize)
	for err == nil {
		var resource storage.UnknownResource
		err = decoder.Decode(&resource)
		if err != nil {
			break
		}
		resource.Kind = modules.Get().CanonicalKind(resource.Kind)
		err = handler(resource)
	}
	if err == io.EOF {
		err = nil
	}
	return trace.Wrap(err)
}

// ResourceFunc is a callback that operates on a Gravity resource
type ResourceFunc func(storage.UnknownResource) error

func isKubernetesResource(resource storage.UnknownResource) bool {
	return resource.Version == "" && resource.Kind == ""
}
