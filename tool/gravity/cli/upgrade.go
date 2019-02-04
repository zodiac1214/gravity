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

package cli

import (
	"context"
	"fmt"

	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/fsm"
	"github.com/gravitational/gravity/lib/localenv"
	"github.com/gravitational/gravity/lib/ops"
	"github.com/gravitational/gravity/lib/update"
	"github.com/gravitational/gravity/lib/utils"

	"github.com/gravitational/trace"
)

func executeUpgradePhase(localEnv, upgradeEnv *localenv.LocalEnvironment, p PhaseParams, operation *ops.SiteOperation) error {
	clusterEnv, err := localEnv.NewClusterEnvironment()
	if err != nil {
		return trace.Wrap(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.Timeout)
	defer cancel()

	progress := utils.NewProgress(ctx, fmt.Sprintf("phase %q execution", p.PhaseID), -1, false)
	defer progress.Stop()

	creds, err := fsm.GetClientCredentials()
	if err != nil {
		return trace.Wrap(err)
	}
	runner := fsm.NewAgentRunner(creds)

	err = update.ExecutePhase(ctx, update.FSMConfig{
		Backend:           clusterEnv.Backend,
		LocalBackend:      upgradeEnv.Backend,
		HostLocalBackend:  localEnv.Backend,
		HostLocalPackages: localEnv.Packages,
		Packages:          clusterEnv.Packages,
		ClusterPackages:   clusterEnv.ClusterPackages,
		Apps:              clusterEnv.Apps,
		Client:            clusterEnv.Client,
		Operator:          clusterEnv.Operator,
		Operation:         operation,
		Users:             clusterEnv.Users,
		Remote:            runner,
	}, fsm.Params{
		PhaseID:  p.PhaseID,
		Force:    p.Force,
		Progress: progress,
	}, p.SkipVersionCheck)

	return trace.Wrap(err)
}

func rollbackUpgradePhase(localEnv, updateEnv *localenv.LocalEnvironment, p PhaseParams, operation ops.SiteOperation) error {
	clusterEnv, err := localEnv.NewClusterEnvironment()
	if err != nil {
		return trace.Wrap(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.Timeout)
	defer cancel()

	progress := utils.NewProgress(ctx, fmt.Sprintf("phase %q rollback", p.PhaseID), -1, false)
	defer progress.Stop()

	creds, err := fsm.GetClientCredentials()
	if err != nil {
		return trace.Wrap(err)
	}
	runner := fsm.NewAgentRunner(creds)

	err = update.RollbackPhase(ctx, update.FSMConfig{
		Backend:           clusterEnv.Backend,
		LocalBackend:      updateEnv.Backend,
		HostLocalBackend:  localEnv.Backend,
		HostLocalPackages: localEnv.Packages,
		Packages:          clusterEnv.Packages,
		ClusterPackages:   clusterEnv.ClusterPackages,
		Apps:              clusterEnv.Apps,
		Client:            clusterEnv.Client,
		Operator:          clusterEnv.Operator,
		Operation:         &operation,
		Users:             clusterEnv.Users,
		Remote:            runner,
	}, fsm.Params{
		PhaseID:  p.PhaseID,
		Force:    p.Force,
		Progress: progress,
	}, p.SkipVersionCheck)

	return trace.Wrap(err)
}

func completeUpdatePlan(localEnv, updateEnv *localenv.LocalEnvironment, operation ops.SiteOperation) error {
	clusterEnv, err := localEnv.NewClusterEnvironment()
	if err != nil {
		return trace.Wrap(err)
	}

	creds, err := fsm.GetClientCredentials()
	if err != nil {
		return trace.Wrap(err)
	}
	runner := fsm.NewAgentRunner(creds)

	fsm, err := update.NewFSM(context.TODO(),
		update.FSMConfig{
			Backend:         clusterEnv.Backend,
			Packages:        clusterEnv.Packages,
			ClusterPackages: clusterEnv.ClusterPackages,
			Apps:            clusterEnv.Apps,
			Client:          clusterEnv.Client,
			Operator:        clusterEnv.Operator,
			Operation:       &operation,
			Users:           clusterEnv.Users,
			LocalBackend:    updateEnv.Backend,
			Remote:          runner,
		})
	if err != nil {
		return trace.Wrap(err)
	}

	err = fsm.Complete(nil)
	if err != nil {
		return trace.Wrap(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaults.RPCAgentShutdownTimeout)
	defer cancel()
	if err = update.ShutdownClusterAgents(ctx, runner); err != nil {
		log.Warnf("Failed to shutdown cluster agents: %v.", trace.DebugReport(err))
	}

	localEnv.Println("cluster has been activated")
	return nil
}
