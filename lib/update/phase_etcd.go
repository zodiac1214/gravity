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
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/fsm"
	"github.com/gravitational/gravity/lib/kubernetes"
	"github.com/gravitational/gravity/lib/state"
	"github.com/gravitational/gravity/lib/storage"
	"github.com/gravitational/gravity/lib/utils"
	"github.com/gravitational/trace"
	log "github.com/sirupsen/logrus"
	kubeapi "k8s.io/client-go/kubernetes"
)

// Upgrade ETCD
// Upgrading etcd to etcd 3 is a somewhat complicated process.
// According to the etcd documentation, upgrades of a cluster are only supported one release at a time. Since we are
// several versions behind, coordinate several upgrades in succession has a certain amount of risk and may also be
// time consuming.
//
// The chosen approach to upgrades of etcd is as follows
// 1. Planet will ship with each version of etcd we support upgrades from
// 2. Planet when started, will determine the version of etcd to use (planet etcd init)
//      This is done by assuming the oldest possible etcd release
//      During an upgrade, the verison of etcd to use is written to the etcd data directory
// 3. Backup all etcd data via API
// 4. Shutdown etcd (all servers) // API outage starts
// 6. Start the cluster masters, but with clients bound to an alternative address (127.0.0.2) and using new data dir
//      The data directory is chosen as /ext/etcd/<version>, so when upgrading, etcd will start with a blank database
//      To rollback, we start the old version of etcd, pointed to the data directory that it used
//      We also delete the data from a previous upgrade, so we can only roll back once
// 7. Restore the etcd data using the API to the new version, and migrate /registry (kubernetes) data to v3 datastore
// 8. Restart etcd on the correct ports// API outage ends
// 9. Restart gravity-site to fix elections
//
//
// Rollback
// Stop etcd (all servers)
// Set the version to use to be the previous version
// Restart etcd using the old version, pointed at the old data directory
// Start etcd (all servers)

const (
	etcdServiceName        = "etcd"
	etcdUpgradeServiceName = "etcd-upgrade"
	etcdPhaseName          = "etcd"
)

func (r phaseBuilder) etcdPlan(
	leadMaster storage.Server,
	otherMasters []storage.Server,
	workers []storage.Server,
	currentVersion string,
	desiredVersion string) *phase {

	root := root(phase{
		ID:          etcdPhaseName,
		Description: fmt.Sprintf("Upgrade etcd %v to %v", currentVersion, desiredVersion),
	})

	// Backup etcd on each master server
	// Do each master, just in case
	backupEtcd := phase{
		ID:          root.ChildLiteral("backup"),
		Description: "Backup etcd data",
	}
	backupEtcd.AddParallel(r.etcdBackupNode(leadMaster, backupEtcd))

	for _, server := range otherMasters {
		p := r.etcdBackupNode(server, backupEtcd)
		backupEtcd.AddParallel(p)
	}

	root.AddSequential(backupEtcd)

	// Shutdown etcd
	// Move data directory to backup location
	shutdownEtcd := phase{
		ID:          root.ChildLiteral("shutdown"),
		Description: "Shutdown etcd cluster",
	}
	shutdownEtcd.AddParallel(r.etcdShutdownNode(leadMaster, shutdownEtcd, true))

	for _, server := range otherMasters {
		p := r.etcdShutdownNode(server, shutdownEtcd, false)
		shutdownEtcd.AddParallel(p)
	}
	for _, server := range workers {
		p := r.etcdShutdownNode(server, shutdownEtcd, false)
		shutdownEtcd.AddParallel(p)
	}

	root.AddSequential(shutdownEtcd)

	// Upgrade servers
	// Replace configuration and data directories, for new version of etcd
	// relaunch etcd on temporary port
	upgradeServers := phase{
		ID:          root.ChildLiteral("upgrade"),
		Description: "Upgrade etcd servers",
	}
	upgradeServers.AddParallel(r.etcdUpgrade(leadMaster, upgradeServers))

	for _, server := range otherMasters {
		p := r.etcdUpgrade(server, upgradeServers)
		upgradeServers.AddParallel(p)
	}
	for _, server := range workers {
		p := r.etcdUpgrade(server, upgradeServers)
		upgradeServers.AddParallel(p)
	}
	root.AddSequential(upgradeServers)

	// Restore kubernetes data
	// migrate to etcd3 store
	// clear kubernetes data from etcd2 store
	restoreData := phase{
		ID:          root.ChildLiteral("restore"),
		Description: "Restore etcd data from backup",
		Executor:    updateEtcdRestore,
		Data: &storage.OperationPhaseData{
			Server: &leadMaster,
		},
	}
	root.AddSequential(restoreData)

	// restart master servers
	// Rolling restart of master servers to listen on normal ports. ETCD outage ends here
	restartMasters := phase{
		ID:          root.ChildLiteral("restart"),
		Description: "Restart etcd servers",
	}
	restartMasters.AddSequential(r.etcdRestart(leadMaster, restartMasters))

	for _, server := range otherMasters {
		p := r.etcdRestart(server, restartMasters)
		restartMasters.AddSequential(p)
	}
	for _, server := range workers {
		p := r.etcdRestart(server, restartMasters)
		restartMasters.AddParallel(p)
	}

	// also restart gravity-site, so that elections get unbroken
	restartMasters.AddParallel(phase{
		ID:          restartMasters.ChildLiteral(constants.GravityServiceName),
		Description: fmt.Sprint("Restart ", constants.GravityServiceName, " service"),
		Executor:    updateEtcdRestartGravity,
		Data: &storage.OperationPhaseData{
			Server: &leadMaster,
		},
	})
	root.AddSequential(restartMasters)

	return &root
}

func (r phaseBuilder) etcdBackupNode(server storage.Server, parent phase) phase {
	return phase{
		ID:          parent.ChildLiteral(server.Hostname),
		Description: fmt.Sprintf("Backup etcd on node %q", server.Hostname),
		Executor:    updateEtcdBackup,
		Data: &storage.OperationPhaseData{
			Server: &server,
		},
	}
}

func (r phaseBuilder) etcdShutdownNode(server storage.Server, parent phase, isLeader bool) phase {
	return phase{
		ID:          parent.ChildLiteral(server.Hostname),
		Description: fmt.Sprintf("Shutdown etcd on node %q", server.Hostname),
		Executor:    updateEtcdShutdown,
		Data: &storage.OperationPhaseData{
			Server: &server,
			Data:   strconv.FormatBool(isLeader),
		},
	}
}

func (r phaseBuilder) etcdUpgrade(server storage.Server, parent phase) phase {
	return phase{
		ID:          parent.ChildLiteral(server.Hostname),
		Description: fmt.Sprintf("Upgrade etcd on node %q", server.Hostname),
		Executor:    updateEtcdMaster,
		Data: &storage.OperationPhaseData{
			Server: &server,
		},
	}
}

func (r phaseBuilder) etcdRestart(server storage.Server, parent phase) phase {
	return phase{
		ID:          parent.ChildLiteral(server.Hostname),
		Description: fmt.Sprintf("Restart etcd on node %q", server.Hostname),
		Executor:    updateEtcdRestart,
		Data: &storage.OperationPhaseData{
			Server: &server,
		},
	}
}

// PhaseUpgradeEtcdBackup backs up etcd data on all servers
type PhaseUpgradeEtcdBackup struct {
	log.FieldLogger
}

func NewPhaseUpgradeEtcdBackup(c FSMConfig, plan storage.OperationPlan, phase storage.OperationPhase, logger log.FieldLogger) (fsm.PhaseExecutor, error) {
	return &PhaseUpgradeEtcdBackup{
		FieldLogger: logger,
	}, nil
}

func backupFile() (string, error) {
	stateDir, err := state.GetStateDir()
	if err != nil {
		return "", trace.Wrap(err)
	}
	return filepath.Join(state.GravityUpdateDir(stateDir), defaults.EtcdUpgradeBackupFile), nil
}

func (p *PhaseUpgradeEtcdBackup) Execute(ctx context.Context) error {
	p.Info("Backup etcd.")
	backupFile, err := backupFile()
	if err != nil {
		return trace.Wrap(err)
	}
	_, err = utils.RunPlanetCommand(ctx, p.FieldLogger, "etcd", "backup", backupFile)
	if err != nil {
		return trace.Wrap(err, "failed to backup etcd")
	}
	return nil
}

func (p *PhaseUpgradeEtcdBackup) Rollback(context.Context) error {
	// NOOP, don't clean up backupfile during rollback, incase we still need it
	return nil
}

func (*PhaseUpgradeEtcdBackup) PreCheck(context.Context) error {
	// TODO(knisbet) should we check that there is enough free space available to hold the backup?
	return nil
}

func (*PhaseUpgradeEtcdBackup) PostCheck(context.Context) error {
	// NOOP
	return nil
}

// PhaseUpgradeEtcdShutdown shuts down etcd across the cluster
type PhaseUpgradeEtcdShutdown struct {
	log.FieldLogger
	Client   *kubeapi.Clientset
	isLeader bool
}

// NewPhaseUpgradeEtcdShutdown creates a phase for shutting down etcd across the cluster
// 4. Shutdown etcd (all servers) // API outage starts
func NewPhaseUpgradeEtcdShutdown(c FSMConfig, plan storage.OperationPlan, phase storage.OperationPhase, logger log.FieldLogger) (fsm.PhaseExecutor, error) {
	return &PhaseUpgradeEtcdShutdown{
		FieldLogger: logger,
		Client:      c.Client,
		isLeader:    phase.Data.Data == "true",
	}, nil
}

func (p *PhaseUpgradeEtcdShutdown) Execute(ctx context.Context) error {
	p.Info("Shutdown etcd.")
	out, err := utils.RunPlanetCommand(ctx, p.FieldLogger, "etcd", "disable")
	if err != nil {
		return trace.Wrap(err)
	}
	p.Info("command output: ", string(out))
	return nil
}

func (p *PhaseUpgradeEtcdShutdown) Rollback(ctx context.Context) error {
	p.Info("Enable etcd.")
	out, err := utils.RunPlanetCommand(ctx, p.FieldLogger, "etcd", "enable")
	if err != nil {
		return trace.Wrap(err)
	}
	p.Info("command output: ", string(out))

	if p.isLeader {
		return trace.Wrap(restartGravitySite(ctx, p.Client, p.FieldLogger))
	}
	return nil
}

func (p *PhaseUpgradeEtcdShutdown) PreCheck(ctx context.Context) error {
	return nil
}

func (*PhaseUpgradeEtcdShutdown) PostCheck(context.Context) error {
	return nil
}

// PhaseUpgradeEtcd upgrades etcd specifically on the leader
type PhaseUpgradeEtcd struct {
	log.FieldLogger
	Server storage.Server
}

func NewPhaseUpgradeEtcd(c FSMConfig, plan storage.OperationPlan, phase storage.OperationPhase, logger log.FieldLogger) (fsm.PhaseExecutor, error) {
	return &PhaseUpgradeEtcd{
		FieldLogger: logger,
		Server:      *phase.Data.Server,
	}, nil
}

// Execute upgrades the leader
// Upgrade etcd by changing the launch version and data directory
// Launch the temporary etcd cluster to restore the database
func (p *PhaseUpgradeEtcd) Execute(ctx context.Context) error {
	p.Info("Upgrade etcd.")
	// TODO(knisbet) only wipe the etcd database when required
	out, err := utils.RunPlanetCommand(ctx, p.FieldLogger, "etcd", "upgrade")
	if err != nil {
		return trace.Wrap(err)
	}
	p.Info("command output: ", string(out))

	out, err = utils.RunPlanetCommand(ctx, p.FieldLogger, "etcd", "enable", "--upgrade")
	if err != nil {
		return trace.Wrap(err)
	}
	p.Info("command output: ", string(out))

	return nil
}

func (p *PhaseUpgradeEtcd) Rollback(ctx context.Context) error {
	p.Info("Rollback upgrade of etcd.")
	out, err := utils.RunPlanetCommand(ctx, p.FieldLogger, "etcd", "disable", "--upgrade")
	if err != nil {
		return trace.Wrap(err)
	}
	p.Info("command output: ", string(out))

	out, err = utils.RunPlanetCommand(ctx, p.FieldLogger, "etcd", "rollback")
	if err != nil {
		return trace.Wrap(err)
	}
	p.Info("command output: ", string(out))

	return nil
}

func (*PhaseUpgradeEtcd) PreCheck(context.Context) error {
	return nil
}

func (*PhaseUpgradeEtcd) PostCheck(context.Context) error {
	return nil
}

// PhaseUpgradeRestore restores etcd data from backup, if it was wiped by the upgrade stage
type PhaseUpgradeEtcdRestore struct {
	log.FieldLogger
	Server storage.Server
}

func NewPhaseUpgradeEtcdRestore(c FSMConfig, plan storage.OperationPlan, phase storage.OperationPhase, logger log.FieldLogger) (fsm.PhaseExecutor, error) {
	return &PhaseUpgradeEtcdRestore{
		FieldLogger: logger,
		Server:      *phase.Data.Server,
	}, nil
}

// Execute restores the etcd data from backup
// 7. Restore the /registry (kubernetes) data to etcd, including automatic migration to v3 datastore for kubernetes
// 10. Restart etcd on the correct ports on first node // API outage ends
func (p *PhaseUpgradeEtcdRestore) Execute(ctx context.Context) error {
	p.Info("Restore etcd data from backup.")
	backupFile, err := backupFile()
	if err != nil {
		return trace.Wrap(err)
	}
	_, err = utils.RunPlanetCommand(ctx, p.FieldLogger, "etcd", "restore", backupFile)
	if err != nil {
		return trace.Wrap(err)
	}

	return nil
}

func (p *PhaseUpgradeEtcdRestore) Rollback(ctx context.Context) error {
	return nil
}

func (p *PhaseUpgradeEtcdRestore) PreCheck(ctx context.Context) error {
	// wait for etcd to form a cluster
	out, err := utils.RunCommand(ctx, p.FieldLogger,
		utils.PlanetCommandArgs(defaults.WaitForEtcdScript, "https://127.0.0.2:2379")...)
	if err != nil {
		return trace.Wrap(err)
	}
	p.Info("command output: ", string(out))
	return nil
}

func (*PhaseUpgradeEtcdRestore) PostCheck(context.Context) error {
	return nil
}

// PhaseUpgradeEtcdRestart disables the etcd-upgrade service, and starts the etcd service
type PhaseUpgradeEtcdRestart struct {
	log.FieldLogger
	Server storage.Server
}

func NewPhaseUpgradeEtcdRestart(c FSMConfig, plan storage.OperationPlan, phase storage.OperationPhase, logger log.FieldLogger) (fsm.PhaseExecutor, error) {
	return &PhaseUpgradeEtcdRestart{
		FieldLogger: logger,
		Server:      *phase.Data.Server,
	}, nil
}

func (p *PhaseUpgradeEtcdRestart) Execute(ctx context.Context) error {
	p.Info("Restart etcd after upgrade.")
	out, err := utils.RunPlanetCommand(ctx, p.FieldLogger, "etcd", "disable", "--upgrade")
	if err != nil {
		return trace.Wrap(err)
	}
	p.Info("command output: ", string(out))

	out, err = utils.RunPlanetCommand(ctx, p.FieldLogger, "etcd", "enable")
	if err != nil {
		return trace.Wrap(err)
	}
	p.Info("command output: ", string(out))
	return nil
}

func (p *PhaseUpgradeEtcdRestart) Rollback(ctx context.Context) error {
	p.Info("Reenable etcd upgrade service.")
	out, err := utils.RunPlanetCommand(ctx, p.FieldLogger, "etcd", "disable")
	if err != nil {
		return trace.Wrap(err)
	}
	p.Info("command output: ", string(out))

	out, err = utils.RunPlanetCommand(ctx, p.FieldLogger, "etcd", "enable", "--upgrade")
	if err != nil {
		return trace.Wrap(err)
	}
	p.Info("command output: ", string(out))
	return nil
}

func (*PhaseUpgradeEtcdRestart) PreCheck(context.Context) error {
	return nil
}

func (*PhaseUpgradeEtcdRestart) PostCheck(context.Context) error {
	// NOOP
	return nil
}

// PhaseUpgradeGravitySiteRestart restarts gravity-site pod
type PhaseUpgradeGravitySiteRestart struct {
	log.FieldLogger
	Client *kubeapi.Clientset
}

func NewPhaseUpgradeGravitySiteRestart(c FSMConfig, plan storage.OperationPlan, phase storage.OperationPhase, logger log.FieldLogger) (fsm.PhaseExecutor, error) {
	if c.Client == nil {
		return nil, trace.BadParameter("phase %q must be run from a master node (requires kubernetes client)", phase.ID)
	}

	return &PhaseUpgradeGravitySiteRestart{
		FieldLogger: logger,
		Client:      c.Client,
	}, nil
}

func (p *PhaseUpgradeGravitySiteRestart) Execute(ctx context.Context) error {
	return trace.Wrap(restartGravitySite(ctx, p.Client, p.FieldLogger))
}

func (p *PhaseUpgradeGravitySiteRestart) Rollback(context.Context) error {
	return nil
}

func (*PhaseUpgradeGravitySiteRestart) PreCheck(context.Context) error {
	return nil
}

func (*PhaseUpgradeGravitySiteRestart) PostCheck(context.Context) error {
	return nil
}

func restartGravitySite(ctx context.Context, client *kubeapi.Clientset, l log.FieldLogger) error {
	l.Info("Restart cluster controller.")
	// wait for etcd to form a cluster
	out, err := utils.RunCommand(ctx, l, utils.PlanetCommandArgs(defaults.WaitForEtcdScript)...)
	if err != nil {
		return trace.Wrap(err)
	}
	l.Info("command output: ", string(out))

	// delete the gravity-site pods, in order to force them to restart
	// This is because the leader election process seems to break during the etcd upgrade
	label := map[string]string{"app": constants.GravityServiceName}
	l.Infof("Deleting pods with label %v.", label)
	err = retry(ctx, func() error {
		return trace.Wrap(kubernetes.DeletePods(client, constants.KubeSystemNamespace, label))
	}, defaults.DrainErrorTimeout)
	return trace.Wrap(err)
}
