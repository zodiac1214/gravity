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
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	appapi "github.com/gravitational/gravity/lib/app"
	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/fsm"
	"github.com/gravitational/gravity/lib/httplib"
	"github.com/gravitational/gravity/lib/install"
	"github.com/gravitational/gravity/lib/localenv"
	"github.com/gravitational/gravity/lib/process"
	"github.com/gravitational/gravity/lib/schema"
	"github.com/gravitational/gravity/lib/systemservice"
	"github.com/gravitational/gravity/lib/utils"

	"github.com/gravitational/configure/cstrings"
	teleutils "github.com/gravitational/teleport/lib/utils"
	"github.com/gravitational/trace"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithField(trace.Component, "cli")

// ConfigureEnvironment updates PATH environment variable to include
// gravity binary search locations
func ConfigureEnvironment() error {
	path := os.Getenv(defaults.PathEnv)
	return trace.Wrap(os.Setenv(defaults.PathEnv, fmt.Sprintf("%v:%v",
		path, defaults.PathEnvVal)))
}

// Run parses CLI arguments and executes an appropriate gravity command
func Run(g *Application) error {
	log.Debugf("Executing: %v.", os.Args)
	err := ConfigureEnvironment()
	if err != nil {
		return trace.Wrap(err)
	}

	args, extraArgs := cstrings.SplitAt(os.Args[1:], "--")
	cmd, err := g.Parse(args)
	if err != nil {
		return trace.Wrap(err)
	}

	if *g.UID != -1 || *g.GID != -1 {
		return SwitchPrivileges(*g.UID, *g.GID)
	}
	err = InitAndCheck(g, cmd)
	if err != nil {
		return trace.Wrap(err)
	}
	return Execute(g, cmd, extraArgs)
}

// InitAndCheck initializes the CLI application according to the provided
// flags and checks that the command is being executed in an appropriate
// environmnent
func InitAndCheck(g *Application, cmd string) error {
	trace.SetDebug(*g.Debug)
	level := logrus.InfoLevel
	if *g.Debug {
		level = logrus.DebugLevel
	}
	switch cmd {
	case g.SiteStartCmd.FullCommand():
		teleutils.InitLogger(teleutils.LoggingForDaemon, level)
	case g.RPCAgentDeployCmd.FullCommand(),
		g.RPCAgentInstallCmd.FullCommand(),
		g.RPCAgentRunCmd.FullCommand(),
		g.PlanCmd.FullCommand(),
		g.UpgradeCmd.FullCommand(),
		g.RollbackCmd.FullCommand(),
		g.ResourceCreateCmd.FullCommand():
		if *g.Debug {
			teleutils.InitLogger(teleutils.LoggingForDaemon, level)
		}
	default:
		teleutils.InitLogger(teleutils.LoggingForCLI, level)
	}
	logrus.SetFormatter(&trace.TextFormatter{})

	// the following commands write logs to the system log file (in
	// addition to journald)
	switch cmd {
	case g.InstallCmd.FullCommand(),
		g.WizardCmd.FullCommand(),
		g.JoinCmd.FullCommand(),
		g.AutoJoinCmd.FullCommand(),
		g.UpdateTriggerCmd.FullCommand(),
		g.UpgradeCmd.FullCommand(),
		g.RPCAgentRunCmd.FullCommand(),
		g.LeaveCmd.FullCommand(),
		g.RemoveCmd.FullCommand(),
		g.OpsAgentCmd.FullCommand():
		install.InitLogging(*g.SystemLogFile)
		// install and join command also duplicate their logs to the file in
		// the current directory for convenience, unless the user set their
		// own location
		switch cmd {
		case g.InstallCmd.FullCommand(), g.JoinCmd.FullCommand():
			if *g.SystemLogFile == defaults.TelekubeSystemLog {
				install.InitLogging(defaults.TelekubeSystemLogFile)
			}
		}
	}

	if *g.ProfileEndpoint != "" {
		err := process.StartProfiling(context.TODO(), *g.ProfileEndpoint, *g.ProfileTo)
		if err != nil {
			log.Warningf("Failed to setup profiling: %v.", trace.DebugReport(err))
		}
	}

	utils.DetectPlanetEnvironment()

	// the following commands must be run inside deployed cluster
	switch cmd {
	case g.UpdateCompleteCmd.FullCommand(),
		g.UpdateTriggerCmd.FullCommand(),
		g.RemoveCmd.FullCommand():
		localEnv, err := g.LocalEnv(cmd)
		if err != nil {
			return trace.Wrap(err)
		}
		if err := checkInCluster(localEnv.DNS.Addr()); err != nil {
			return trace.Wrap(err)
		}
	}

	// the following commands must be run as root
	switch cmd {
	case g.SystemUpdateCmd.FullCommand(),
		g.UpgradeCmd.FullCommand(),
		g.SystemRollbackCmd.FullCommand(),
		g.SystemUninstallCmd.FullCommand(),
		g.RollbackCmd.FullCommand(),
		g.UpdateSystemCmd.FullCommand(),
		g.RPCAgentShutdownCmd.FullCommand(),
		g.RPCAgentInstallCmd.FullCommand(),
		g.RPCAgentRunCmd.FullCommand(),
		g.SystemServiceInstallCmd.FullCommand(),
		g.SystemServiceUninstallCmd.FullCommand(),
		g.EnterCmd.FullCommand(),
		g.PlanetEnterCmd.FullCommand(),
		g.PlanCmd.FullCommand(),
		g.InstallCmd.FullCommand(),
		g.JoinCmd.FullCommand(),
		g.AutoJoinCmd.FullCommand(),
		g.SystemDevicemapperMountCmd.FullCommand(),
		g.SystemDevicemapperUnmountCmd.FullCommand(),
		g.BackupCmd.FullCommand(),
		g.RestoreCmd.FullCommand(),
		g.GarbageCollectCmd.FullCommand(),
		g.SystemGCRegistryCmd.FullCommand(),
		g.CheckCmd.FullCommand():
		if err := checkRunningAsRoot(); err != nil {
			return trace.Wrap(err)
		}
	}

	// following commands must be run outside the planet container
	switch cmd {
	case g.SystemUpdateCmd.FullCommand(),
		g.UpdateSystemCmd.FullCommand(),
		g.UpgradeCmd.FullCommand(),
		g.SystemGCRegistryCmd.FullCommand(),
		g.PlanetEnterCmd.FullCommand(),
		g.EnterCmd.FullCommand():
		if utils.CheckInPlanet() {
			return trace.BadParameter("this command must be run outside of planet container")
		}
	}

	// following commands must be run inside the planet container
	switch cmd {
	case g.SystemEnablePromiscModeCmd.FullCommand(),
		g.SystemDisablePromiscModeCmd.FullCommand(),
		g.SystemGCJournalCmd.FullCommand():
		if !utils.CheckInPlanet() {
			return trace.BadParameter("this command must be run inside planet container")
		}
	}

	return nil
}

// Execute executes the gravity command given with cmd
func Execute(g *Application, cmd string, extraArgs []string) error {
	switch cmd {
	case g.VersionCmd.FullCommand():
		return printVersion(*g.VersionCmd.Output)
	case g.SiteStartCmd.FullCommand():
		return startSite(*g.SiteStartCmd.ConfigPath, *g.SiteStartCmd.InitPath)
	case g.SiteInitCmd.FullCommand():
		return initCluster(*g.SiteInitCmd.ConfigPath, *g.SiteInitCmd.InitPath)
	case g.SiteStatusCmd.FullCommand():
		return statusSite()
	}

	localEnv, err := g.LocalEnv(cmd)
	if err != nil {
		return trace.Wrap(err)
	}
	defer localEnv.Close()

	// create an environment used during upgrades
	var updateEnv *localenv.LocalEnvironment
	if g.isUpgradeCommand(cmd) {
		updateEnv, err = g.UpgradeEnv()
		if err != nil {
			return trace.Wrap(err)
		}
		defer updateEnv.Close()
	}

	// create an environment where join-specific data is stored
	var joinEnv *localenv.LocalEnvironment
	switch cmd {
	case g.JoinCmd.FullCommand(), g.AutoJoinCmd.FullCommand(), g.PlanCmd.FullCommand(), g.RollbackCmd.FullCommand():
		joinEnv, err = g.JoinEnv()
		if err != nil {
			return trace.Wrap(err)
		}
		defer joinEnv.Close()
	}

	switch cmd {
	case g.OpsAgentCmd.FullCommand():
		return agent(localEnv, agentConfig{
			systemLogFile: *g.SystemLogFile,
			userLogFile:   *g.UserLogFile,
			packageAddr:   *g.OpsAgentCmd.PackageAddr,
			advertiseAddr: g.OpsAgentCmd.AdvertiseAddr.String(),
			serverAddr:    *g.OpsAgentCmd.ServerAddr,
			token:         *g.OpsAgentCmd.Token,
			vars:          *g.OpsAgentCmd.Vars,
			serviceUID:    *g.OpsAgentCmd.ServiceUID,
			serviceGID:    *g.OpsAgentCmd.ServiceGID,
			cloudProvider: *g.OpsAgentCmd.CloudProvider,
		}, *g.OpsAgentCmd.ServiceName)
	case g.WizardCmd.FullCommand():
		return startInstall(localEnv, InstallConfig{
			Mode:          constants.InstallModeInteractive,
			Insecure:      *g.Insecure,
			ReadStateDir:  *g.InstallCmd.Path,
			UserLogFile:   *g.UserLogFile,
			SystemLogFile: *g.SystemLogFile,
			ServiceUID:    *g.WizardCmd.ServiceUID,
			ServiceGID:    *g.WizardCmd.ServiceGID,
		})
	case g.InstallCmd.FullCommand():
		if *g.InstallCmd.Resume {
			*g.InstallCmd.Phase = fsm.RootPhase
		}
		if *g.InstallCmd.Phase != "" {
			return executeInstallPhase(localEnv, PhaseParams{
				PhaseID: *g.InstallCmd.Phase,
				Force:   *g.InstallCmd.Force,
				Timeout: *g.InstallCmd.PhaseTimeout,
			})
		}
		return startInstall(localEnv, NewInstallConfig(g))
	case g.JoinCmd.FullCommand():
		if *g.JoinCmd.Resume {
			*g.JoinCmd.Phase = fsm.RootPhase
		}
		if *g.JoinCmd.Phase != "" || *g.JoinCmd.Complete {
			return executeJoinPhase(localEnv, joinEnv, PhaseParams{
				PhaseID:  *g.JoinCmd.Phase,
				Force:    *g.JoinCmd.Force,
				Timeout:  *g.JoinCmd.PhaseTimeout,
				Complete: *g.JoinCmd.Complete,
			})
		}
		return Join(localEnv, joinEnv, NewJoinConfig(g))
	case g.AutoJoinCmd.FullCommand():
		return autojoin(localEnv, joinEnv, autojoinConfig{
			systemLogFile: *g.SystemLogFile,
			userLogFile:   *g.UserLogFile,
			clusterName:   *g.AutoJoinCmd.ClusterName,
			role:          *g.AutoJoinCmd.Role,
			systemDevice:  *g.AutoJoinCmd.SystemDevice,
			dockerDevice:  *g.AutoJoinCmd.DockerDevice,
			mounts:        *g.AutoJoinCmd.Mounts,
		})
	case g.UpdateCheckCmd.FullCommand():
		return updateCheck(localEnv, *g.UpdateCheckCmd.App)
	case g.UpdateTriggerCmd.FullCommand():
		return updateTrigger(localEnv,
			updateEnv,
			*g.UpdateTriggerCmd.App,
			*g.UpdateTriggerCmd.Manual)
	case g.UpgradeCmd.FullCommand():
		if *g.UpgradeCmd.Resume {
			*g.UpgradeCmd.Phase = fsm.RootPhase
		}
		if *g.UpgradeCmd.Phase != "" {
			return executeUpgradePhase(localEnv, updateEnv,
				upgradePhaseParams{
					phaseID:          *g.UpgradeCmd.Phase,
					force:            *g.UpgradeCmd.Force,
					skipVersionCheck: *g.UpgradeCmd.SkipVersionCheck,
					timeout:          *g.UpgradeCmd.Timeout,
				})
		}
		if *g.UpgradeCmd.Complete {
			return completeUpgrade(localEnv, updateEnv)
		}
		return updateTrigger(localEnv,
			updateEnv,
			*g.UpgradeCmd.App,
			*g.UpgradeCmd.Manual)
	case g.RollbackCmd.FullCommand():
		return rollbackOperationPhase(localEnv,
			updateEnv,
			joinEnv,
			rollbackParams{
				phaseID:          *g.RollbackCmd.Phase,
				force:            *g.RollbackCmd.Force,
				skipVersionCheck: *g.RollbackCmd.SkipVersionCheck,
				timeout:          *g.RollbackCmd.PhaseTimeout,
			})
	case g.PlanCmd.FullCommand():
		if *g.PlanCmd.Init {
			return initOperationPlan(localEnv, updateEnv)
		}
		if *g.PlanCmd.Sync {
			return syncOperationPlan(localEnv, updateEnv)
		}
		return displayOperationPlan(localEnv, updateEnv, joinEnv, *g.PlanCmd.OperationID, *g.PlanCmd.Output)
	case g.LeaveCmd.FullCommand():
		return leave(localEnv, leaveConfig{
			force:     *g.LeaveCmd.Force,
			confirmed: *g.LeaveCmd.Confirm,
		})
	case g.RemoveCmd.FullCommand():
		return remove(localEnv, removeConfig{
			server:    *g.RemoveCmd.Node,
			force:     *g.RemoveCmd.Force,
			confirmed: *g.RemoveCmd.Confirm,
		})
	case g.StatusCmd.FullCommand():
		printOptions := printOptions{
			token:       *g.StatusCmd.Token,
			operationID: *g.StatusCmd.OperationID,
			quiet:       *g.Silent,
			format:      *g.StatusCmd.Output,
		}
		if *g.StatusCmd.Tail {
			return tailStatus(localEnv, *g.StatusCmd.OperationID)
		}
		if *g.StatusCmd.Seconds != 0 {
			return statusPeriodic(localEnv, printOptions, *g.StatusCmd.Seconds)
		} else {
			return status(localEnv, printOptions)
		}
	case g.UpdateUploadCmd.FullCommand():
		return uploadUpdate(localEnv, *g.UpdateUploadCmd.OpsCenterURL)
	case g.AppPackageCmd.FullCommand():
		return appPackage(localEnv)
	// app commands
	case g.AppImportCmd.FullCommand():
		if len(*g.AppImportCmd.SetImages) != 0 || len(*g.AppImportCmd.SetDeps) != 0 || *g.AppImportCmd.Version != "" {
			if !*g.AppImportCmd.Vendor {
				fmt.Printf("found one of --set-image, --set-dep or --version flags: turning on --vendor mode\n")
				*g.AppImportCmd.Vendor = true
			}
		}
		if *g.AppImportCmd.Vendor && *g.AppImportCmd.RegistryURL == "" {
			return trace.BadParameter("vendoring mode requires --registry-url")
		}
		req := &appapi.ImportRequest{
			Repository:             *g.AppImportCmd.Repository,
			PackageName:            *g.AppImportCmd.Name,
			PackageVersion:         *g.AppImportCmd.Version,
			Vendor:                 *g.AppImportCmd.Vendor,
			Force:                  *g.AppImportCmd.Force,
			ExcludePatterns:        *g.AppImportCmd.Excludes,
			IncludePaths:           *g.AppImportCmd.IncludePaths,
			ResourcePatterns:       *g.AppImportCmd.VendorPatterns,
			IgnoreResourcePatterns: *g.AppImportCmd.VendorIgnorePatterns,
			SetImages:              *g.AppImportCmd.SetImages,
			SetDeps:                *g.AppImportCmd.SetDeps,
		}
		return importApp(localEnv,
			*g.AppImportCmd.RegistryURL,
			*g.AppImportCmd.DockerURL,
			*g.AppImportCmd.Source,
			req,
			*g.AppImportCmd.OpsCenterURL,
			*g.Silent,
			*g.AppImportCmd.Parallel)
	case g.AppExportCmd.FullCommand():
		return exportApp(localEnv,
			*g.AppExportCmd.Locator,
			*g.AppExportCmd.OpsCenterURL,
			*g.AppExportCmd.RegistryURL)
	case g.AppDeleteCmd.FullCommand():
		return deleteApp(localEnv,
			*g.AppDeleteCmd.Locator,
			*g.AppDeleteCmd.OpsCenterURL,
			*g.AppDeleteCmd.Force)
	case g.AppListCmd.FullCommand():
		return listApps(localEnv,
			*g.AppListCmd.Repository,
			*g.AppListCmd.Type,
			*g.AppListCmd.ShowHidden,
			*g.AppListCmd.OpsCenterURL)
	case g.AppStatusCmd.FullCommand():
		return statusApp(localEnv,
			*g.AppStatusCmd.Locator,
			*g.AppStatusCmd.OpsCenterURL)
	case g.AppUninstallCmd.FullCommand():
		return uninstallApp(localEnv,
			*g.AppUninstallCmd.Locator)
	case g.AppPullCmd.FullCommand():
		return pullApp(localEnv,
			*g.AppPullCmd.Package,
			*g.AppPullCmd.OpsCenterURL,
			*g.AppPullCmd.Labels,
			*g.AppPullCmd.Force)
	case g.AppPushCmd.FullCommand():
		return pushApp(localEnv,
			*g.AppPushCmd.Package,
			*g.AppPushCmd.OpsCenterURL)
	case g.AppHookCmd.FullCommand():
		req := appapi.HookRunRequest{
			Application: *g.AppHookCmd.Package,
			Hook:        schema.HookType(*g.AppHookCmd.HookName),
			Env:         *g.AppHookCmd.Env,
		}
		return outputAppHook(localEnv, req)
	case g.AppUnpackCmd.FullCommand():
		return unpackAppResources(localEnv,
			*g.AppUnpackCmd.Package,
			*g.AppUnpackCmd.Dir,
			*g.AppUnpackCmd.OpsCenterURL,
			*g.AppUnpackCmd.ServiceUID)
	// package commands
	case g.PackImportCmd.FullCommand():
		return importPackage(localEnv,
			*g.PackImportCmd.Path,
			*g.PackImportCmd.Locator,
			*g.PackImportCmd.CheckManifest,
			*g.PackImportCmd.OpsCenterURL,
			*g.PackImportCmd.Labels)
	case g.PackUnpackCmd.FullCommand():
		return unpackPackage(localEnv,
			*g.PackUnpackCmd.Locator,
			*g.PackUnpackCmd.Dir,
			*g.PackUnpackCmd.OpsCenterURL,
			nil)
	case g.PackExportCmd.FullCommand():
		mode, err := strconv.ParseUint(*g.PackExportCmd.FileMask, 8, 32)
		if err != nil {
			return trace.BadParameter("invalid file access mask %v: %v", *g.PackExportCmd.FileMask, err)
		}
		return exportPackage(localEnv,
			*g.PackExportCmd.Locator,
			*g.PackExportCmd.OpsCenterURL,
			*g.PackExportCmd.File,
			os.FileMode(mode))
	case g.PackListCmd.FullCommand():
		return listPackages(localEnv,
			*g.PackListCmd.Repository,
			*g.PackListCmd.OpsCenterURL)
	case g.PackDeleteCmd.FullCommand():
		return deletePackage(localEnv,
			*g.PackDeleteCmd.Locator,
			*g.PackDeleteCmd.Force,
			*g.PackDeleteCmd.OpsCenterURL)
	case g.PackConfigureCmd.FullCommand():
		return configurePackage(localEnv,
			*g.PackConfigureCmd.Package,
			*g.PackConfigureCmd.ConfPackage,
			*g.PackConfigureCmd.Args)
	case g.PackCommandCmd.FullCommand():
		return executePackageCommand(localEnv,
			*g.PackCommandCmd.Command,
			*g.PackCommandCmd.Package,
			g.PackCommandCmd.ConfPackage,
			*g.PackCommandCmd.Args)
	case g.PackPushCmd.FullCommand():
		return pushPackage(localEnv,
			*g.PackPushCmd.Package,
			*g.PackPushCmd.OpsCenterURL)
	case g.PackPullCmd.FullCommand():
		return pullPackage(localEnv,
			*g.PackPullCmd.Package,
			*g.PackPullCmd.OpsCenterURL,
			*g.PackPullCmd.Labels,
			*g.PackPullCmd.Force)
	case g.PackLabelsCmd.FullCommand():
		return updatePackageLabels(localEnv,
			*g.PackLabelsCmd.Package,
			*g.PackLabelsCmd.OpsCenterURL,
			*g.PackLabelsCmd.Add,
			*g.PackLabelsCmd.Remove)
		// OpsCenter commands
	case g.OpsConnectCmd.FullCommand():
		return connectToOpsCenter(localEnv,
			*g.OpsConnectCmd.OpsCenterURL,
			*g.OpsConnectCmd.Username,
			*g.OpsConnectCmd.Password)
	case g.OpsDisconnectCmd.FullCommand():
		return disconnectFromOpsCenter(localEnv,
			*g.OpsDisconnectCmd.OpsCenterURL)
	case g.OpsListCmd.FullCommand():
		return listOpsCenters(localEnv)
	case g.UserCreateCmd.FullCommand():
		return createUser(localEnv,
			*g.UserCreateCmd.OpsCenterURL,
			*g.UserCreateCmd.Email,
			*g.UserCreateCmd.Type,
			*g.UserCreateCmd.Password)
	case g.UserDeleteCmd.FullCommand():
		return deleteUser(localEnv,
			*g.UserDeleteCmd.OpsCenterURL,
			*g.UserDeleteCmd.Email)
	case g.APIKeyCreateCmd.FullCommand():
		return createAPIKey(localEnv,
			*g.APIKeyCreateCmd.OpsCenterURL,
			*g.APIKeyCreateCmd.Email)
	case g.APIKeyListCmd.FullCommand():
		return getAPIKeys(localEnv,
			*g.APIKeyListCmd.OpsCenterURL,
			*g.APIKeyListCmd.Email)
	case g.APIKeyDeleteCmd.FullCommand():
		return deleteAPIKey(localEnv,
			*g.APIKeyDeleteCmd.OpsCenterURL,
			*g.APIKeyDeleteCmd.Email,
			*g.APIKeyDeleteCmd.Token)
	case g.ReportCmd.FullCommand():
		return getClusterReport(localEnv, *g.ReportCmd.FilePath)
	// cluster commands
	case g.SiteListCmd.FullCommand():
		return listSites(localEnv, *g.SiteListCmd.OpsCenterURL)
	case g.SiteInfoCmd.FullCommand():
		return printLocalClusterInfo(localEnv,
			*g.SiteInfoCmd.Format)
	case g.SiteCompleteCmd.FullCommand():
		return completeInstallerStep(localEnv,
			*g.SiteCompleteCmd.Support)
	case g.SiteResetPasswordCmd.FullCommand():
		return resetPassword(localEnv)
	case g.StatusResetCmd.FullCommand():
		return resetClusterState(localEnv)
	case g.LocalSiteCmd.FullCommand():
		return getLocalSite(localEnv)
	// system service commands
	case g.SystemRotateCertsCmd.FullCommand():
		return rotateCertificates(localEnv, rotateOptions{
			clusterName: *g.SystemRotateCertsCmd.ClusterName,
			validFor:    *g.SystemRotateCertsCmd.ValidFor,
			caPath:      *g.SystemRotateCertsCmd.CAPath,
		})
	case g.SystemExportCACmd.FullCommand():
		return exportCertificateAuthority(localEnv,
			*g.SystemExportCACmd.ClusterName,
			*g.SystemExportCACmd.CAPath)
	case g.SystemReinstallCmd.FullCommand():
		return systemReinstall(localEnv,
			*g.SystemReinstallCmd.Package,
			*g.SystemReinstallCmd.ServiceName,
			*g.SystemReinstallCmd.Labels)
	case g.SystemHistoryCmd.FullCommand():
		return systemHistory(localEnv)
	case g.SystemPullUpdatesCmd.FullCommand():
		return systemPullUpdates(localEnv,
			*g.SystemPullUpdatesCmd.OpsCenterURL,
			*g.SystemPullUpdatesCmd.RuntimePackage)
	case g.SystemUpdateCmd.FullCommand():
		return systemUpdate(localEnv,
			*g.SystemUpdateCmd.ChangesetID,
			*g.SystemUpdateCmd.ServiceName,
			*g.SystemUpdateCmd.WithStatus,
			*g.SystemUpdateCmd.RuntimePackage)
	case g.UpdateSystemCmd.FullCommand():
		return systemUpdate(localEnv,
			*g.UpdateSystemCmd.ChangesetID,
			*g.UpdateSystemCmd.ServiceName,
			*g.UpdateSystemCmd.WithStatus,
			*g.UpdateSystemCmd.RuntimePackage)
	case g.SystemRollbackCmd.FullCommand():
		return systemRollback(localEnv,
			*g.SystemRollbackCmd.ChangesetID,
			*g.SystemRollbackCmd.ServiceName,
			*g.SystemRollbackCmd.WithStatus)
	case g.SystemStepDownCmd.FullCommand():
		return stepDown(localEnv)
	case g.BackupCmd.FullCommand():
		return backup(localEnv,
			*g.BackupCmd.Tarball,
			*g.BackupCmd.Timeout,
			*g.BackupCmd.Follow,
			*g.Silent)
	case g.RestoreCmd.FullCommand():
		return restore(localEnv,
			*g.RestoreCmd.Tarball,
			*g.RestoreCmd.Timeout,
			*g.RestoreCmd.Follow,
			*g.Silent)
	case g.SystemServiceInstallCmd.FullCommand():
		req := &systemservice.NewPackageServiceRequest{
			Package:       *g.SystemServiceInstallCmd.Package,
			ConfigPackage: *g.SystemServiceInstallCmd.ConfigPackage,
			ServiceSpec: systemservice.ServiceSpec{
				StartCommand:     *g.SystemServiceInstallCmd.StartCommand,
				StartPreCommand:  *g.SystemServiceInstallCmd.StartPreCommand,
				StartPostCommand: *g.SystemServiceInstallCmd.StartPostCommand,
				StopCommand:      *g.SystemServiceInstallCmd.StopCommand,
				StopPostCommand:  *g.SystemServiceInstallCmd.StopPostCommand,
				Timeout:          *g.SystemServiceInstallCmd.Timeout,
				Type:             *g.SystemServiceInstallCmd.Type,
				LimitNoFile:      *g.SystemServiceInstallCmd.LimitNoFile,
				Restart:          *g.SystemServiceInstallCmd.Restart,
				KillMode:         *g.SystemServiceInstallCmd.KillMode,
			},
		}
		return systemServiceInstall(localEnv, req)
	case g.SystemServiceUninstallCmd.FullCommand():
		return systemServiceUninstall(localEnv,
			*g.SystemServiceUninstallCmd.Package,
			*g.SystemServiceUninstallCmd.Name)
	case g.SystemServiceListCmd.FullCommand():
		return systemServiceList(localEnv)
	case g.SystemServiceStatusCmd.FullCommand():
		return systemServiceStatus(localEnv,
			*g.SystemServiceStatusCmd.Package,
			*g.SystemServiceStatusCmd.Name)
	case g.SystemUninstallCmd.FullCommand():
		return systemUninstall(localEnv, *g.SystemUninstallCmd.Confirmed)
	case g.SystemReportCmd.FullCommand():
		return systemReport(localEnv,
			*g.SystemReportCmd.Filter,
			*g.SystemReportCmd.Compressed)
	case g.SystemStateDirCmd.FullCommand():
		return printStateDir()
	case g.SystemEnablePromiscModeCmd.FullCommand():
		return enablePromiscMode(localEnv, *g.SystemEnablePromiscModeCmd.Iface)
	case g.SystemDisablePromiscModeCmd.FullCommand():
		return disablePromiscMode(localEnv, *g.SystemDisablePromiscModeCmd.Iface)
	case g.SystemExportRuntimeJournalCmd.FullCommand():
		return exportRuntimeJournal(localEnv, *g.SystemExportRuntimeJournalCmd.OutputFile)
	case g.SystemStreamRuntimeJournalCmd.FullCommand():
		return streamRuntimeJournal(localEnv)
	case g.GarbageCollectCmd.FullCommand():
		phase := *g.GarbageCollectCmd.Phase
		if *g.GarbageCollectCmd.Resume {
			phase = fsm.RootPhase
		}
		if phase != "" {
			return garbageCollectPhase(localEnv, phase, *g.GarbageCollectCmd.PhaseTimeout,
				*g.GarbageCollectCmd.Force)
		}
		return garbageCollect(localEnv, *g.GarbageCollectCmd.Manual, *g.GarbageCollectCmd.Confirmed)
	case g.SystemGCJournalCmd.FullCommand():
		return removeUnusedJournalFiles(localEnv,
			*g.SystemGCJournalCmd.MachineIDFile,
			*g.SystemGCJournalCmd.LogDir)
	case g.SystemGCPackageCmd.FullCommand():
		return removeUnusedPackages(localEnv,
			*g.SystemGCPackageCmd.DryRun,
			*g.SystemGCPackageCmd.Cluster)
	case g.SystemGCRegistryCmd.FullCommand():
		return removeUnusedImages(localEnv,
			*g.SystemGCRegistryCmd.DryRun,
			*g.SystemGCRegistryCmd.Confirm)
	case g.PlanetEnterCmd.FullCommand(), g.EnterCmd.FullCommand():
		return planetEnter(localEnv, extraArgs)
	case g.ExecCmd.FullCommand():
		return planetExec(localEnv,
			*g.ExecCmd.TTY,
			*g.ExecCmd.Stdin,
			*g.ExecCmd.Cmd,
			*g.ExecCmd.Args)
	case g.ShellCmd.FullCommand():
		return planetShell(localEnv)
	case g.PlanetStatusCmd.FullCommand():
		return getPlanetStatus(localEnv, extraArgs)
	case g.SystemDevicemapperMountCmd.FullCommand():
		return devicemapperMount(*g.SystemDevicemapperMountCmd.Disk)
	case g.SystemDevicemapperUnmountCmd.FullCommand():
		return devicemapperUnmount()
	case g.SystemDevicemapperSystemDirCmd.FullCommand():
		return devicemapperQuerySystemDirectory()
	case g.UsersInviteCmd.FullCommand():
		return inviteUser(localEnv,
			*g.UsersInviteCmd.Name,
			*g.UsersInviteCmd.Roles,
			*g.UsersInviteCmd.TTL)
	case g.UsersResetCmd.FullCommand():
		return resetUser(localEnv,
			*g.UsersResetCmd.Name,
			*g.UsersResetCmd.TTL)
	case g.ResourceCreateCmd.FullCommand():
		return createResource(localEnv,
			*g.ResourceCreateCmd.Filename,
			*g.ResourceCreateCmd.Upsert,
			*g.ResourceCreateCmd.User)
	case g.ResourceRemoveCmd.FullCommand():
		return removeResource(localEnv,
			*g.ResourceRemoveCmd.Kind,
			*g.ResourceRemoveCmd.Name,
			*g.ResourceRemoveCmd.Force,
			*g.ResourceRemoveCmd.User)
	case g.ResourceGetCmd.FullCommand():
		return getResources(localEnv,
			*g.ResourceGetCmd.Kind,
			*g.ResourceGetCmd.Name,
			*g.ResourceGetCmd.WithSecrets,
			*g.ResourceGetCmd.Format,
			*g.ResourceGetCmd.User)
	case g.RPCAgentDeployCmd.FullCommand():
		return rpcAgentDeploy(localEnv, updateEnv, *g.RPCAgentDeployCmd.Args)
	case g.RPCAgentInstallCmd.FullCommand():
		return rpcAgentInstall(localEnv, *g.RPCAgentInstallCmd.Args)
	case g.RPCAgentRunCmd.FullCommand():
		return rpcAgentRun(localEnv, updateEnv,
			*g.RPCAgentRunCmd.Args)
	case g.RPCAgentShutdownCmd.FullCommand():
		return rpcAgentShutdown(localEnv)
	case g.CheckCmd.FullCommand():
		return checkManifest(localEnv,
			*g.CheckCmd.ManifestFile,
			*g.CheckCmd.Profile,
			*g.CheckCmd.AutoFix)
	}
	return trace.NotFound("unknown command %v", cmd)
}

// SwitchPrivileges switches user privileges and executes
// the same command but with different user id and group id
func SwitchPrivileges(uid, gid int) error {
	// see this for details: https://github.com/golang/go/issues/1435
	// setuid is broken, so we can't use it
	fullPath, err := exec.LookPath(os.Args[0])
	if err != nil {
		return trace.Wrap(err)
	}
	cred := &syscall.Credential{}
	if uid != -1 {
		cred.Uid = uint32(uid)
	}
	if gid != -1 {
		cred.Gid = uint32(gid)
	}
	args := cstrings.WithoutFlag(os.Args, "--uid")
	args = cstrings.WithoutFlag(args, "--gid")
	cmd := exec.Cmd{
		Path: fullPath,
		Args: args,
		SysProcAttr: &syscall.SysProcAttr{
			Credential: cred,
		},
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	return cmd.Run()
}

// pickSiteHost detects the currently active master host.
// It does this by probing master IP from /etc/container-environment and localhost.
func pickSiteHost() (string, error) {
	var hosts []string
	// master IP takes priority, as it contains IP of the k8s API server.
	// this is a temporary hack, need to figure out the proper way
	if f, err := os.Open(defaults.ContainerEnvironmentFile); err == nil {
		defer f.Close()
		r := bufio.NewReader(f)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			parts := strings.Split(line, "=")
			if len(parts) == 2 && parts[0] == "KUBE_APISERVER" {
				targetHost := strings.Trim(strings.TrimSpace(parts[1]), `"`)
				log.Infof("found apiserver: %v", targetHost)
				return targetHost, nil
			}
		}
	}
	hosts = append(hosts, "127.0.0.1")
	log.Infof("trying these hosts: %v", hosts)
	for _, host := range hosts {
		log.Infof("connecting to %s", host)
		r, err := http.Get(fmt.Sprintf("http://%s:8080", host))
		if err == nil && r != nil {
			log.Infof(r.Status)
			return host, nil
		} else if err != nil {
			log.Infof(err.Error())
		}
	}
	return "", trace.Errorf("failed to find a gravity site to connect to")
}

// checkInCluster checks if the command is invoked inside Gravity cluster
func checkInCluster(dnsAddr string) error {
	client := httplib.GetClient(true,
		httplib.WithLocalResolver(dnsAddr),
		httplib.WithTimeout(defaults.ClusterCheckTimeout))
	_, err := client.Get(defaults.GravityServiceURL)
	if err != nil {
		log.Warnf("Gravity controller is inaccessible: %v.", err)
		return trace.NotFound("No Gravity cluster detected. This failure could happen during failover, try again. Execute this command locally on one of the cluster nodes.")
	}
	return nil
}

func checkRunningAsRoot() error {
	if os.Geteuid() != 0 {
		return trace.BadParameter("this command should be run as root")
	}
	return nil
}
