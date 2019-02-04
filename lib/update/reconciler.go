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
	"os/exec"

	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/fsm"
	"github.com/gravitational/gravity/lib/storage"
	"github.com/gravitational/gravity/lib/utils"

	"github.com/gravitational/trace"
	"github.com/sirupsen/logrus"
)

// Reconciler can sync plan changes between backends
type Reconciler interface {
	// ReconcilePlan syncs changes for the specified plan and returns the updated plan
	ReconcilePlan(context.Context, storage.OperationPlan) (*storage.OperationPlan, error)
}

// NewDefaultReconciler returns an implementation of Reconciler that syncs changes between
// a local and primary backends
func NewDefaultReconciler(primary, localBackend storage.Backend, cluster, operationID string, logger logrus.FieldLogger) *reconciler {
	return &reconciler{
		FieldLogger:  logger,
		backend:      primary,
		localBackend: localBackend,
		cluster:      cluster,
		operationID:  operationID,
	}
}

// ReconcilePlan syncs changes for the specified plan and returns the updated plan
func (r *reconciler) ReconcilePlan(ctx context.Context, plan storage.OperationPlan) (updated *storage.OperationPlan, err error) {
	err = r.trySyncChangelogToEtcd(ctx)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	err = r.trySyncChangelogFromEtcd(ctx)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	// Always use the local plan as authoritative
	local, err := r.localBackend.GetOperationPlanChangelog(r.cluster, r.operationID)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return fsm.ResolvePlan(plan, local), nil
}

func (r *reconciler) trySyncChangelogToEtcd(ctx context.Context) error {
	shouldSync, err := isEtcdAvailable(ctx, r.FieldLogger)
	if err != nil {
		return trace.Wrap(err)
	}

	if shouldSync {
		return trace.Wrap(r.syncChangelog(r.localBackend, r.backend))
	}

	return nil
}

func (r *reconciler) trySyncChangelogFromEtcd(ctx context.Context) error {
	shouldSync, err := isEtcdAvailable(ctx, r.FieldLogger)
	if err != nil {
		return trace.Wrap(err)
	}

	if shouldSync {
		return trace.Wrap(r.syncChangelog(r.backend, r.localBackend))
	}

	return nil
}

// syncChangelog will sync changelog entries from src to dst storage
func (r *reconciler) syncChangelog(src storage.Backend, dst storage.Backend) error {
	return trace.Wrap(syncChangelog(src, dst, r.cluster, r.operationID))
}

type reconciler struct {
	logrus.FieldLogger
	backend, localBackend storage.Backend
	cluster               string
	operationID           string
}

// syncChangelog will sync changelog entries from src to dst storage
func syncChangelog(src storage.Backend, dst storage.Backend, clusterName string, operationID string) error {
	srcChangeLog, err := src.GetOperationPlanChangelog(clusterName, operationID)
	if err != nil {
		return trace.Wrap(err)
	}

	dstChangeLog, err := dst.GetOperationPlanChangelog(clusterName, operationID)
	if err != nil {
		return trace.Wrap(err)
	}

	diff := fsm.DiffChangelog(srcChangeLog, dstChangeLog)
	for _, entry := range diff {
		_, err = dst.CreateOperationPlanChange(entry)
		if err != nil {
			return trace.Wrap(err)
		}
	}

	return nil
}

// isEtcdAvailable verifies that the etcd cluster is healthy
func isEtcdAvailable(ctx context.Context, logger logrus.FieldLogger) (bool, error) {
	_, err := utils.RunCommand(ctx, logger, utils.PlanetCommandArgs(defaults.EtcdCtlBin, "cluster-health")...)
	if err != nil {
		// etcdctl uses an exit code if the health cannot be checked
		// so we don't need to return an error
		if _, ok := trace.Unwrap(err).(*exec.ExitError); ok {
			return false, nil
		}
		return false, trace.Wrap(err)
	}
	return true, nil
}
