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

package update

import (
	"context"
	"time"

	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/fsm"
	"github.com/gravitational/gravity/lib/kubernetes"
	"github.com/gravitational/gravity/lib/loc"
	"github.com/gravitational/gravity/lib/storage"
	"github.com/gravitational/gravity/lib/utils"

	"github.com/cenkalti/backoff"
	"github.com/gravitational/rigging"
	"github.com/gravitational/trace"
	log "github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	rbacv1beta1 "k8s.io/api/rbac/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	kubeapi "k8s.io/client-go/kubernetes"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// phaseTaint defines the operation of adding a taint to the node
type phaseTaint struct {
	kubernetesOperation
}

// NewPhaseTaint returns a new executor for adding a taint to a node
func NewPhaseTaint(c FSMConfig, plan storage.OperationPlan, phase storage.OperationPhase, logger log.FieldLogger) (*phaseTaint, error) {
	op, err := newKubernetesOperation(c, plan, phase, logger)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &phaseTaint{
		kubernetesOperation: *op,
	}, nil
}

// Execute adds a taint on the specified node.
func (p *phaseTaint) Execute(ctx context.Context) error {
	p.Infof("Taint %v.", formatServer(p.Server))
	err := taint(ctx, p.Client.CoreV1().Nodes(), p.Server.KubeNodeID(), addTaint(true))
	return trace.Wrap(err)
}

// Rollback removes the taint from the node
func (p *phaseTaint) Rollback(ctx context.Context) error {
	p.Infof("Remove taint from %v.", formatServer(p.Server))
	err := taint(ctx, p.Client.CoreV1().Nodes(), p.Server.KubeNodeID(), addTaint(false))
	if err != nil && !trace.IsNotFound(err) {
		return trace.Wrap(err)
	}
	return nil
}

// phaseUntaint defines the operation of removing a taint from the node
type phaseUntaint struct {
	kubernetesOperation
}

// NewPhaseUntaint returns a new executor for removing a taint from a node
func NewPhaseUntaint(c FSMConfig, plan storage.OperationPlan, phase storage.OperationPhase, logger log.FieldLogger) (*phaseUntaint, error) {
	op, err := newKubernetesOperation(c, plan, phase, logger)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &phaseUntaint{
		kubernetesOperation: *op,
	}, nil
}

// Execute removes a taint from the specified node.
func (p *phaseUntaint) Execute(ctx context.Context) error {
	p.Infof("Remove taint from %v.", formatServer(p.Server))
	err := taint(ctx, p.Client.CoreV1().Nodes(), p.Server.KubeNodeID(), addTaint(false))
	return trace.Wrap(err)
}

// Rollback is a no-op for this phase
func (p *phaseUntaint) Rollback(context.Context) error {
	return nil
}

// phaseDrain defines the operation of draining a node
type phaseDrain struct {
	kubernetesOperation
}

// NewPhaseDrain returns a new executor for draining a node
func NewPhaseDrain(c FSMConfig, plan storage.OperationPlan, phase storage.OperationPhase, logger log.FieldLogger) (*phaseDrain, error) {
	op, err := newKubernetesOperation(c, plan, phase, logger)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &phaseDrain{
		kubernetesOperation: *op,
	}, nil
}

// Execute drains the specified node
func (p *phaseDrain) Execute(ctx context.Context) error {
	p.Infof("Drain %v.", formatServer(p.Server))
	ctx, cancel := context.WithTimeout(ctx, defaults.DrainTimeout)
	defer cancel()
	err := retry(ctx, func() error {
		return trace.Wrap(drain(ctx, p.Client, p.Server.KubeNodeID()))
	}, defaults.DrainErrorTimeout)
	return trace.Wrap(err)
}

// Rollback reverts the effect of drain by uncordoning the node
func (p *phaseDrain) Rollback(ctx context.Context) error {
	p.Infof("Uncordon %v.", formatServer(p.Server))
	err := uncordon(ctx, p.Client.CoreV1().Nodes(), p.Server.KubeNodeID())
	return trace.Wrap(err)
}

// phaseKubeletPermissions defines the operation to bootstrap additional permissions for kubelet.
// This is necessary for a master node that is upgraded first and needs to update node status (via patch)
// on an older api server.
type phaseKubeletPermissions struct {
	kubernetesOperation
}

// NewPhaseKubeletPermissions returns a new executor for bootstrapping additional kubelet permissions
func NewPhaseKubeletPermissions(c FSMConfig, plan storage.OperationPlan, phase storage.OperationPhase, logger log.FieldLogger) (*phaseKubeletPermissions, error) {
	op, err := newKubernetesOperation(c, plan, phase, logger)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &phaseKubeletPermissions{
		kubernetesOperation: *op,
	}, nil
}

// Execute adds additional permissions for kubelet
func (p *phaseKubeletPermissions) Execute(context.Context) error {
	p.Infof("Update kubelet perrmissiong on %v.", formatServer(p.Server))
	err := updateKubeletPermissions(p.Client)
	return trace.Wrap(err)
}

// Rollback removes the previously added clusterrole/clusterrolebinding for kubelet
func (p *phaseKubeletPermissions) Rollback(context.Context) error {
	return trace.Wrap(removeKubeletPermissions(p.Client))
}

// phaseUncordon defines the operation of uncordoning a node
type phaseUncordon struct {
	kubernetesOperation
}

// NewPhaseUncordon returns a new executor for uncordoning a node
func NewPhaseUncordon(c FSMConfig, plan storage.OperationPlan, phase storage.OperationPhase, logger log.FieldLogger) (*phaseUncordon, error) {
	op, err := newKubernetesOperation(c, plan, phase, logger)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &phaseUncordon{
		kubernetesOperation: *op,
	}, nil
}

// Execute uncordons the specified node.
// This will block until DNS/cluster controller endpoints are populated
func (p *phaseUncordon) Execute(ctx context.Context) error {
	p.Infof("Uncordon %v.", formatServer(p.Server))
	err := uncordon(ctx, p.Client.CoreV1().Nodes(), p.Server.KubeNodeID())
	return trace.Wrap(err)
}

// Rollback is a no-op for this phase
func (p *phaseUncordon) Rollback(context.Context) error {
	return nil
}

// phaseEndpoints defines the operation waiting for DNS/cluster endpoints after
// a node has been drained
type phaseEndpoints struct {
	kubernetesOperation
}

// NewPhaseEndpoints returns a new executor for waiting for endpoints
func NewPhaseEndpoints(c FSMConfig, plan storage.OperationPlan, phase storage.OperationPhase, logger log.FieldLogger) (*phaseEndpoints, error) {
	op, err := newKubernetesOperation(c, plan, phase, logger)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &phaseEndpoints{
		kubernetesOperation: *op,
	}, nil
}

// Execute waits for endpoints
func (p *phaseEndpoints) Execute(ctx context.Context) error {
	p.Infof("Wait for endpoints on %v.", formatServer(p.Server))
	err := waitForEndpoints(ctx, p.Client.CoreV1(), p.Server)
	return trace.Wrap(err)
}

// Rollback is a no-op for this phase
func (p *phaseEndpoints) Rollback(context.Context) error {
	return nil
}

func newKubernetesOperation(c FSMConfig, plan storage.OperationPlan, phase storage.OperationPhase, logger log.FieldLogger) (*kubernetesOperation, error) {
	if phase.Data == nil || phase.Data.Server == nil {
		return nil, trace.NotFound("no server specified for phase %q", phase.ID)
	}

	if c.Client == nil {
		return nil, trace.BadParameter("phase %q must be run from a master node (requires kubernetes client)", phase.ID)
	}
	return &kubernetesOperation{
		Client:      c.Client,
		OperationID: plan.OperationID,
		Server:      *phase.Data.Server,
		Servers:     plan.Servers,
		FieldLogger: logger,
	}, nil
}

// kubernetesOperation defines a kubernetes operation
type kubernetesOperation struct {
	// Client specifies the kubernetes API client
	Client *kubeapi.Clientset
	// OperationID is the id of the current update operation
	OperationID string
	// Server is the server currently being updated
	Server storage.Server
	// Servers is the list of servers being updated
	Servers []storage.Server
	log.FieldLogger
}

// PreCheck makes sure the phase is being executed on the correct server
func (p *kubernetesOperation) PreCheck(context.Context) error {
	return trace.Wrap(fsm.CheckMasterServer(p.Servers))
}

// PostCheck is no-op for this phase
func (p *kubernetesOperation) PostCheck(context.Context) error {
	return nil
}

func taint(ctx context.Context, client corev1.NodeInterface, node string, add addTaint) error {
	taint := v1.Taint{
		Key:    defaults.RunLevelLabel,
		Value:  defaults.RunLevelSystem,
		Effect: v1.TaintEffectNoExecute,
	}

	var taintsToAdd, taintsToRemove []v1.Taint
	if add {
		taintsToAdd = append(taintsToAdd, taint)
	} else {
		taintsToRemove = append(taintsToRemove, taint)
	}

	err := kubernetes.UpdateTaints(ctx, client, node, taintsToAdd, taintsToRemove)
	if err != nil {
		if add {
			return trace.Wrap(err, "failed to add taint %v to node %q", taint, node)
		}
		return trace.Wrap(err, "failed to remove taint %v from node %q", taint, node)
	}
	return nil
}

func drain(ctx context.Context, client *kubeapi.Clientset, node string) error {
	err := kubernetes.Drain(ctx, client, node)
	return trace.Wrap(err)
}

func uncordon(ctx context.Context, client corev1.NodeInterface, node string) error {
	err := kubernetes.SetUnschedulable(ctx, client, node, false)
	return trace.Wrap(err)
}

func updateKubeletPermissions(client *kubeapi.Clientset) error {
	err := createKubeletRole(client)
	if err != nil && !trace.IsAlreadyExists(err) {
		return trace.Wrap(err)
	}

	err = createKubeletRoleBinding(client)
	if err != nil && !trace.IsAlreadyExists(err) {
		return trace.Wrap(err)
	}
	return nil
}

func createKubeletRole(client *kubeapi.Clientset) error {
	_, err := client.Rbac().ClusterRoles().Create(&rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: defaults.KubeletUpdatePermissionsRole},
		Rules: []rbacv1.PolicyRule{
			{Verbs: []string{"patch"}, APIGroups: []string{""}, Resources: []string{"nodes/status"}},
		},
	})

	err = rigging.ConvertError(err)
	if err == nil {
		return nil
	}
	if !trace.IsNotFound(err) {
		return trace.Wrap(err)
	}
	// If there's no RBAC v1 support, drop down to v1beta1
	_, err = client.RbacV1beta1().ClusterRoles().Create(&rbacv1beta1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: defaults.KubeletUpdatePermissionsRole},
		Rules: []rbacv1beta1.PolicyRule{
			{Verbs: []string{"patch"}, APIGroups: []string{""}, Resources: []string{"nodes/status"}},
		},
	})
	return trace.Wrap(rigging.ConvertError(err))
}

func createKubeletRoleBinding(client *kubeapi.Clientset) error {
	_, err := client.Rbac().ClusterRoleBindings().Create(&rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: defaults.KubeletUpdatePermissionsRole},
		Subjects:   []rbacv1.Subject{{Kind: constants.KubernetesKindUser, Name: constants.KubeletUser}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: constants.RbacAPIGroup,
			Name:     defaults.KubeletUpdatePermissionsRole,
			Kind:     rigging.KindClusterRole,
		},
	})
	err = rigging.ConvertError(err)
	if err == nil {
		return nil
	}
	if !trace.IsNotFound(err) {
		return trace.Wrap(err)
	}
	// If there's no RBAC v1 support, drop down to v1beta1
	_, err = client.RbacV1beta1().ClusterRoleBindings().Create(&rbacv1beta1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: defaults.KubeletUpdatePermissionsRole},
		Subjects:   []rbacv1beta1.Subject{{Kind: constants.KubernetesKindUser, Name: constants.KubeletUser}},
		RoleRef: rbacv1beta1.RoleRef{
			APIGroup: constants.RbacAPIGroup,
			Name:     defaults.KubeletUpdatePermissionsRole,
			Kind:     rigging.KindClusterRole,
		},
	})
	return trace.Wrap(rigging.ConvertError(err))
}

func removeKubeletPermissions(client *kubeapi.Clientset) error {
	err := rigging.ConvertError(client.Rbac().ClusterRoles().Delete(defaults.KubeletUpdatePermissionsRole, nil))
	if err != nil && !trace.IsNotFound(err) {
		return trace.Wrap(err)
	}
	err = rigging.ConvertError(client.Rbac().ClusterRoleBindings().Delete(defaults.KubeletUpdatePermissionsRole, nil))
	if err != nil && !trace.IsNotFound(err) {
		return trace.Wrap(err)
	}
	return nil
}

// supportsTaints determines if the specified gravity package
// supports node taints.
func supportsTaints(gravityPackage loc.Locator) (supports bool, err error) {
	ver, err := gravityPackage.SemVer()
	if err != nil {
		return false, trace.Wrap(err)
	}
	return defaults.BaseTaintsVersion.Compare(*ver) <= 0, nil
}

func waitForEndpoints(ctx context.Context, client corev1.CoreV1Interface, server storage.Server) error {
	node := server.KubeNodeID()
	clusterLabels := labels.Set{"app": defaults.GravityClusterLabel}
	kubednsLegacyLabels := labels.Set{"k8s-app": "kube-dns"}
	kubednsLabels := labels.Set{"k8s-app": defaults.KubeDNSLabel}
	matchesNode := matchesNode(node)
	err := retry(ctx, func() error {
		if (hasEndpoints(client, clusterLabels, existingEndpoint) == nil) &&
			(hasEndpoints(client, kubednsLabels, matchesNode) == nil ||
				hasEndpoints(client, kubednsLegacyLabels, matchesNode) == nil) {
			return nil
		}
		return trace.NotFound("endpoints not ready")
	}, defaults.EndpointsWaitTimeout)
	return trace.Wrap(err)
}

func hasEndpoints(client corev1.CoreV1Interface, labels labels.Set, fn endpointMatchFn) error {
	list, err := client.Endpoints(metav1.NamespaceSystem).List(
		metav1.ListOptions{
			LabelSelector: labels.String(),
		},
	)
	if err != nil {
		log.Warnf("failed to query endpoints: %v", err)
		return trace.Wrap(err, "failed to query endpoints")
	}
	for _, endpoint := range list.Items {
		for _, subset := range endpoint.Subsets {
			for _, addr := range subset.Addresses {
				log.Debugf("trying %v", addr)
				if fn(addr) {
					return nil
				}
			}
		}
	}
	log.Warnf("no active endpoints found for query %q", labels)
	return trace.NotFound("no active endpoints found for query %q", labels)
}

// matchesNode is a predicate that matches an endpoint address to the specified
// node name
func matchesNode(node string) endpointMatchFn {
	return func(addr v1.EndpointAddress) bool {
		// Abort if the node name is not populated.
		// There is no need to wait for endpoints we cannot
		// match to a node.
		return addr.NodeName == nil || *addr.NodeName == node
	}
}

// existingEndpoint is a trivial predicate that matches for any endpoint.
func existingEndpoint(v1.EndpointAddress) bool {
	return true
}

func retry(ctx context.Context, fn func() error, timeout time.Duration) error {
	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = timeout
	return trace.Wrap(utils.RetryWithInterval(ctx, b, fn))
}

// endpointMatchFn matches an endpoint address using custom criteria.
type endpointMatchFn func(addr v1.EndpointAddress) bool

type addTaint bool
