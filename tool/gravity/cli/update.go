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

	appservice "github.com/gravitational/gravity/lib/app"
	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/httplib"
	"github.com/gravitational/gravity/lib/loc"
	"github.com/gravitational/gravity/lib/localenv"
	"github.com/gravitational/gravity/lib/ops"
	"github.com/gravitational/gravity/lib/pack"
	"github.com/gravitational/gravity/lib/schema"

	"github.com/gravitational/trace"
)

func updateCheck(env *localenv.LocalEnvironment, appPackage string) error {
	operator, err := env.SiteOperator()
	if err != nil {
		return trace.Wrap(err)
	}

	site, err := operator.GetLocalSite()
	if err != nil {
		return trace.Wrap(err)
	}

	_, err = checkForUpdate(env, operator, site, appPackage)
	return trace.Wrap(err)
}

func updateTrigger(
	localEnv *localenv.LocalEnvironment,
	updateEnv *localenv.LocalEnvironment,
	appPackage string,
	manual bool,
) error {
	clusterEnv, err := localEnv.NewClusterEnvironment()
	if err != nil {
		return trace.Wrap(err)
	}

	if clusterEnv.Client == nil {
		return trace.BadParameter("this operation can only be executed on one of the master nodes")
	}
	operator := clusterEnv.Operator

	cluster, err := operator.GetLocalSite()
	if err != nil {
		return trace.Wrap(err)
	}

	teleportClient, err := localEnv.TeleportClient(constants.Localhost)
	if err != nil {
		return trace.Wrap(err, "failed to create a teleport client")
	}

	proxy, err := teleportClient.ConnectToProxy()
	if err != nil {
		return trace.Wrap(err, "failed to connect to teleport proxy")
	}

	app, err := checkForUpdate(localEnv, operator, cluster, appPackage)
	if err != nil {
		return trace.Wrap(err)
	}

	err = checkCanUpdate(*cluster, operator, app.Manifest)
	if err != nil {
		return trace.Wrap(err)
	}

	opKey, err := operator.CreateSiteAppUpdateOperation(ops.CreateSiteAppUpdateOperationRequest{
		AccountID:  cluster.AccountID,
		SiteDomain: cluster.Domain,
		App:        app.Package.String(),
	})
	if err != nil {
		return trace.Wrap(err)
	}

	defer func() {
		r := recover()
		triggered := err == nil && r == nil
		if !triggered {
			if errDelete := operator.DeleteSiteOperation(*opKey); errDelete != nil {
				log.Warnf("Failed to clean up update operation %v: %v.",
					opKey, trace.DebugReport(errDelete))
			}
		}
		if r != nil {
			panic(r)
		}
	}()

	req := deployAgentsRequest{
		clusterState: cluster.ClusterState,
		clusterName:  cluster.Domain,
		clusterEnv:   clusterEnv,
		proxy:        proxy,
		nodeParams:   []string{constants.RPCAgentSyncPlanFunction},
	}

	if !manual {
		req.leaderParams = []string{constants.RPCAgentUpgradeFunction}
		// Force this node to be the operation leader
		req.leader, err = findLocalServer(*cluster)
		if err != nil {
			log.Warnf("Failed to find local node in cluster state: %v.",
				trace.DebugReport(err))
			return trace.Wrap(err, "failed to find local node in cluster state.\n"+
				"Make sure you start the operation from one of the cluster master nodes.")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaults.AgentDeployTimeout)
	defer cancel()
	err = deployUpdateAgents(ctx, localEnv, updateEnv, req)
	if err != nil {
		return trace.Wrap(err)
	}

	if localEnv.Silent {
		fmt.Printf("%v", opKey.OperationID)
		return nil
	}

	localEnv.Printf("update operation (%v) has been started\n", opKey.OperationID)

	if !manual {
		localEnv.Println("the cluster is updating in background")
		return nil
	}

	localEnv.Println(`
The update operation has been created in manual mode.

To view the operation plan, run:

$ gravity plan

To perform the upgrade, execute all upgrade phases in the order they appear in
the plan by running:

$ sudo gravity upgrade --phase=<phase-id>

To rollback an unsuccessful phase, you can run:

$ sudo gravity rollback --phase=<phase-id>

Once all phases have been successfully completed, run the following command to
mark the operation as "completed" and return the cluster to the "active" state:

$ gravity upgrade --complete

To abort an unsuccessful operation, rollback all completed/failed phases and
run the same command. The operation will be marked as "failed" and the cluster
will be returned to the "active" state.`)

	return nil
}

func checkCanUpdate(cluster ops.Site, operator ops.Operator, manifest schema.Manifest) error {
	existingGravityPackage, err := cluster.App.Manifest.Dependencies.ByName(constants.GravityPackage)
	if err != nil {
		return trace.Wrap(err)
	}

	supportsUpdate, err := supportsUpdate(*existingGravityPackage)
	if err != nil {
		return trace.Wrap(err)
	}
	if !supportsUpdate {
		return trace.BadParameter(`
Installed runtime version (%q) is too old and cannot be updated by this package.
Please update this installation to a minimum required runtime version (%q) before using this update.`,
			existingGravityPackage.Version, defaults.BaseUpdateVersion)
	}

	return nil
}

// checkForUpdate determines if there is an updatePackage for the cluster's application
// and returns a reference to it if available.
// updatePackage specifies an optional (potentially incomplete) package name of the update package.
// If unspecified, the currently installed application package is used.
func checkForUpdate(env *localenv.LocalEnvironment, operator ops.Operator, site *ops.Site, updatePackage string) (*appservice.Application, error) {
	// if app package was not provided, default to the latest version of
	// the currently installed app
	if updatePackage == "" {
		updatePackage = site.App.Package.Name
	}

	updateLoc, err := pack.MakeLocator(updatePackage)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	apps, err := env.AppService(
		defaults.GravityServiceURL,
		localenv.AppConfig{},
		httplib.WithLocalResolver(env.DNS.Addr()),
		httplib.WithInsecure())
	if err != nil {
		return nil, trace.Wrap(err)
	}

	update, err := apps.GetApp(*updateLoc)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	err = pack.CheckUpdatePackage(site.App.Package, update.Package)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	env.Printf("updating %v from %v to %v\n",
		update.Package.Name, site.App.Package.Version, update.Package.Version)

	return update, nil
}

func supportsUpdate(gravityPackage loc.Locator) (supports bool, err error) {
	ver, err := gravityPackage.SemVer()
	if err != nil {
		return false, trace.Wrap(err)
	}
	return defaults.BaseUpdateVersion.Compare(*ver) <= 0, nil
}
