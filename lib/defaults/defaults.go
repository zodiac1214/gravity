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

package defaults

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gravitational/gravity/lib/constants"

	"github.com/coreos/go-semver/semver"
	"k8s.io/api/core/v1"
)

const (
	// SignupTokenTTL is a default signup token expiry time
	SignupTokenTTL = 8 * time.Hour

	// MaxSignupTokenTTL is a maximum TTL for a web signup one time token
	// clients can reduce this time, not increase it
	MaxSignupTokenTTL = 48 * time.Hour

	// UserResetTokenTTL is a default password reset token expiry time
	UserResetTokenTTL = 8 * time.Hour

	// MaxUserResetTokenTTL is a maximum TTL for password reset token
	MaxUserResetTokenTTL = 24 * time.Hour

	// AgentTokenBytes is a default length in bytes of random auth token
	// generated for agent
	AgentTokenBytes = 32

	// SignupTokenBytes is length in bytes for crypto random generated signup tokens
	SignupTokenBytes = 32

	// MinPasswordLength is minimum password length
	MinPasswordLength = 6

	// MaxPasswordLength is maximum password length (for sanity)
	MaxPasswordLength = 128

	// ResetPasswordLength is the length of the reset user password
	ResetPasswordLength = 10

	// HOTPTokenDigits is the amount of digits in HOTP token
	HOTPTokenDigits = 6

	// HOTPFirstTokensRange is amount of lookahead tokens we remember
	// for sync purposes
	HOTPFirstTokensRange = 4

	// GCPeriod sets default garbage collection period
	GCPeriod = 5 * time.Second

	// WaitForEventMaxAttempts is the maximum number of attempts to query
	// k8s Events API when waiting for a certain event to happen
	WaitForEventMaxAttempts = 500

	// WaitForEventInterval indicates the delay between above attempts
	WaitForEventInterval = 5 * time.Second

	// Default retry settings
	RetryInterval           = 5 * time.Second
	RetryAttempts           = 100
	RetryLessAttempts       = 20
	RetrySmallerMaxInterval = RetryLessAttempts * RetryInterval

	// EtcdRetryInterval is the retry interval for some etcd commands
	EtcdRetryInterval = 3 * time.Second

	// InstallApplicationTimeout is the max allowed time for k8s application to install
	InstallApplicationTimeout = 90 * time.Minute // 1.5 hours

	// PhaseTimeout is the default phase execution timeout
	PhaseTimeout = "1h"

	// UpdateTimeout is the max allowed time for system update
	UpdateTimeout = 30 * time.Minute

	// InstallSystemServiceTimeout specifies the maximum time to wait for system install service to complete
	InstallSystemServiceTimeout = 5 * time.Minute

	// LabelRetryAttempts specifies the maximum number of attempts to label a node
	LabelRetryAttempts = 10

	// ExponentialRetryInitialDelay is the interval between the first and second retry attempts
	ExponentialRetryInitialDelay = 5 * time.Second
	// ExponentialRetryMaxDelay is the maximum delay between retry attempts
	ExponentialRetryMaxDelay = 30 * time.Second

	// ProvisionRetryInterval is the interval between provisioning attempts
	ProvisionRetryInterval = 1 * time.Second
	// ProvisionRetryAttempts is the number of provisioning attempts
	ProvisionRetryAttempts = 5

	// ResumeRetryInterval specifies the frequency of attempts to resume last operation
	ResumeRetryInterval = 10 * time.Second

	// ResumeRetryAttempts specifies the total number of attempts to resume last operation
	ResumeRetryAttempts = 20

	// ProvisioningTokenBytes is the length of the provisioning token
	// generated during installs
	ProvisioningTokenBytes = 32

	// InstallTokenBytes is the length of the token generated for a one-time installation
	InstallTokenBytes = 16

	// InstallTokenTTL is the TTL for the install token after the installation
	// has been completed/or failed
	InstallTokenTTL = time.Hour

	// MaxOperationConcurrency defines a number of servers an operation can run on concurrently
	MaxOperationConcurrency = 5

	// MaxValidationConcurrency defines a number of validation requests to run concurrently
	MaxValidationConcurrency = 5

	// MaxExpandConcurrency is the number of servers that can be joining the cluster concurrently
	MaxExpandConcurrency = 5

	// DownloadRetryPeriod is the period between failed retry attempts
	DownloadRetryPeriod = 5 * time.Second

	// DownloadRetryAttempts is the number of attempts to download package/file before giving up
	DownloadRetryAttempts = 20

	// ProgressPollTimeout defines the timeout between progress polling attempts
	ProgressPollTimeout = 500 * time.Millisecond

	// HookJobDeadline sets the default limit on the hook job running time
	HookJobDeadline = 20 * time.Minute

	// CertTTL is Teleport's SSH cert default TTL
	CertTTL = 10 * time.Hour

	// CertRenewPeriod is how often the certificate is renewed
	CertRenewPeriod = time.Minute

	// PathEnvVal is a default value for PATH environment variable
	PathEnvVal = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/writable/bin"

	// PathEnv is a name for standard linux path environment variable
	PathEnv = "PATH"

	// DevicemapperAutoextendThreshold is the percentage of space used before LVM automatically
	// attempts to extend the available space (100=disabled)
	DevicemapperAutoextendThreshold = 80
	// DevicemapperAutoextendStep defines the devicemapper extension step in percent
	DevicemapperAutoextendStep = 20

	// DatabaseSchemaVersion is a running counter for the current version of the database schema.
	// The version is used when generating an empty database as a stamp for a subsequent migration step.
	// It is important to keep the schema version up-to-date with the tip version of the migration state.
	DatabaseSchemaVersion = 5

	// GravityYAMLFile is a default filename for gravity config file
	GravityYAMLFile = "gravity.yaml"

	// TeleportYAMLFile is a default filename for teleport config file
	TeleportYAMLFile = "teleport.yaml"

	// LocalGravityDir is the path to local gravity package state
	LocalGravityDir = "/var/lib/gravity/local"

	// SiteGravityDir is where local gravity site stores all its data
	SiteGravityDir = "/var/lib/gravity/site"

	// GravityDir is where all root state of Gravity is stored
	GravityDir = "/var/lib/gravity"

	// GravityUpdateDir specifies the directory used by the update process
	GravityUpdateDir = "/var/lib/gravity/site/update"

	// GravityRPCAgentPort defines which port RPC agent is listening on
	GravityRPCAgentPort = 3012

	// GravityRPCAgentServiceName defines systemd unit service name
	GravityRPCAgentServiceName = "gravity-agent.service"

	// AgentValidationTimeout specifies the maximum amount of time for a remote validation
	// request during the preflight test
	AgentValidationTimeout = 1 * time.Minute

	// AgentHealthCheckTimeout specifies the maximum amount of time for a health check
	AgentHealthCheckTimeout = 5 * time.Second

	// AgentReconnectTimeout specifies the timeout for attempt to reconnect
	AgentReconnectTimeout = 15 * time.Second

	// AgentConnectTimeout specifies the timeout for the initial connect
	AgentConnectTimeout = 1 * time.Minute

	// AgentStopTimeout is amount of time agent gets to gracefully shut down
	AgentStopTimeout = 10 * time.Second

	// PeerConnectTimeout is the timeout of an RPC agent connecting to its peer
	PeerConnectTimeout = 10 * time.Second

	// GravityPackagePrefix defines base prefix of gravity package
	GravityPackagePrefix = "gravitational.io/gravity"

	// TelekubeSystemLogFile is the system log file name
	TelekubeSystemLogFile = "telekube-system.log"

	// TelekubeUserLogFile is the user log file name
	TelekubeUserLogFile = "telekube-install.log"

	// SystemLogDir is the directory where gravity logs go
	SystemLogDir = "/var/log"

	// TelekubePackage is the Telekube application package name
	TelekubePackage = "telekube"

	// EnvironmentPath is the path to the environment file
	EnvironmentPath = "/etc/environment"

	// ProfilingInterval defines the frequency of taking state snapshots (debugging)
	ProfilingInterval = 1 * time.Minute

	// HumanReasonableTimeout is amount of time certain command can run without producing any output
	HumanReasonableTimeout = 3 * time.Second

	// ClusterCheckTimeout is amount of time allotted to the test that verifies if cluster controller
	// is accessible
	ClusterCheckTimeout = 5 * time.Second

	// SatelliteRPCAgentPort is port used by satellite agent to expose its status
	SatelliteRPCAgentPort = 7575

	// GravityWebAssetsDir is the directory where gravity stores assets (including web)
	// depending on the work mode.
	// In development mode, the assets are looked up in web/dist relative to the current directory.
	// In wizard or site mode - they are looked up from this directory
	GravityWebAssetsDir = "/usr/local/share/gravity"

	// GravityMountService defines the name of the service for the gravity state directory
	// to handle filesystem mounts
	// Important: keep this mount service in sync with the value of GravityDir
	// Source: https://www.freedesktop.org/software/systemd/man/systemd.mount.html
	GravityMountService = "var-lib-gravity.mount"

	// SecretsDir is the place for gravity TLS secrets to be
	SecretsDir = "secrets"

	// HostBin is the /usr/bin directory on host
	HostBin = "/usr/bin"

	// EtcDir is the /etc directory on host
	EtcDir = "/etc"

	// WritableDir is the /writable directory on host (e.g. on Ubuntu Core)
	WritableDir = "/writable"

	// EtcWritableDir is the /etc/writable directory on host (e.g. on Ubuntu Core)
	EtcWritableDir = "/etc/writable"

	// GravityBin is a default location of gravity binary
	GravityBin = "/usr/bin/gravity"

	// GravityBinAlternate is an alternative location of gravity binary on systems
	// where /usr/bin is not writable (e.g. on Ubuntu Core)
	GravityBinAlternate = "/writable/bin/gravity"

	// KubectlBin is the default location of kubectl binary
	KubectlBin = "/usr/bin/kubectl"

	// KubectlBinAlternate is an alternative location of kubectl binary on systems
	// where /usr/bin is not writable (e.g. on Ubuntu Core)
	KubectlBinAlternate = "/writable/bin/kubectl"

	// KubectlScript is the location of kubectl script, which host's kubectl
	// is symlinked to, inside the planet
	KubectlScript = "/usr/local/bin/kubectl"

	// HelmBin is the location of helm binary inside planet
	HelmBin = "/usr/bin/helm"

	// PlanetBin is the default location of planet binary
	PlanetBin = "/usr/bin/planet"

	// WaitForEtcdScript is the path to the planet wait for etcd to be available script
	WaitForEtcdScript = "/usr/bin/scripts/wait-for-etcd.sh"

	// SerfBin is the default location of the serf binary
	SerfBin = "/usr/bin/serf"

	// JournalctlBin is the default location of the journalctl inside planet
	JournalctlBin = "/usr/bin/journalctl"

	// SystemctlBin is systemctl executable inside planet
	SystemctlBin = "/bin/systemctl"

	// StatBin is stat executable path inside planet
	StatBin = "/usr/bin/stat"

	// SystemdLogDir specifies the default location of the systemd journal files
	SystemdLogDir = "/var/log/journal"

	// SystemdMachineIDFile specifies the default location of the systemd machine-id file
	SystemdMachineIDFile = "/etc/machine-id"

	// GravityEphemeralDir is used to store short-lived data (for example,
	// that's only needed for the duration of the operation) that can't be
	// stored in a regular state directory (for example, during initial
	// installation or join the state directory can be formatted)
	GravityEphemeralDir = "/usr/local/share/gravity"

	// GravityConfigFilename is the name of the file with gravity configuration
	GravityConfigFilename = ".gravity.config"

	// PlanetKubeConfigPath is the location of kube config inside planet's filesystem
	PlanetKubeConfigPath = "/etc/kubernetes/kubectl.kubeconfig"

	// CertsDir is where all certificates are stored on the host machine
	CertsDir = "/etc/ssl/certs"

	// App defines the application to create if not specified
	App = "gravitational.io/telekube:0.0.0+latest"

	// SiteDomainName is used by site create tool
	SiteDomainName = "dev.local"

	// TestPostgres environment variable turns on or off PostgreSQL tests
	TestPostgres = "PQ"

	// TestPostgresURI is a test URI connector for PostgreSQL tests
	TestPostgresURI = "PQ_URI"

	// TestETCD instructs us to test Etcd backend
	TestETCD = "TEST_ETCD"

	// TestETCDConfig is a JSON BLOB with etcd config
	TestETCDConfig = "TEST_ETCD_CONFIG"

	// TestK8s controls whether k8s tests are run
	TestK8s = "TEST_K8S"

	// LocalDir is the gravity subdirectory where local data is stored
	LocalDir = "local"

	// SiteDir is the gravity subdirectory where cluster data is stored
	SiteDir = "site"

	// UnpackedDir is the default named of the directory with
	// unpacked package archives
	UnpackedDir = "unpacked"

	// PackagesDir is the place where we put all local packages
	PackagesDir = "packages"

	// UpdateDir is the gravity subdirectory where update related data is stored
	UpdateDir = "update"

	// AgentDir is the gravity subdirectory where update agent stores its data
	AgentDir = "agent"

	// ImportDir is the place for app import state
	ImportDir = "import"

	// TempDir is the place for temp files and folders
	TempDir = "tmp"

	// ResourcesDir is the name of the directory where apps store their resources such as app manifest
	ResourcesDir = "resources"

	// PlanetDir is the name of the planet directory
	PlanetDir = "planet"

	// ShareDir is the name of the share directory
	ShareDir = "share"

	// LogDir is the name of the log directory
	LogDir = "log"

	// StateRegistryDir is the name of the docker registry directory inside the planet state directory
	StateRegistryDir = "registry"

	// ResourcesFile is the default name of the file with application k8s resources
	ResourcesFile = "resources.yaml"

	// PlanetShareDir is the in-planet share directory
	PlanetShareDir = "/ext/share"

	// SharedDirMask is a mask for shared directories
	SharedDirMask = 0755

	// SharedExecutableMask is a mask for shared executable file
	SharedExecutableMask = 0755

	// SharedReadMask is a mask for a shared file with read access for everyone
	SharedReadMask = 0644

	// GroupReadMask is a mask with group read access
	GroupReadMask = 0640

	// SharedReadWriteMask is a mask for a shared file with read/write access for everyone
	SharedReadWriteMask = 0666

	// PrivateDirMask is a mask for private directories
	PrivateDirMask = 0700

	// PrivateFileMask is a mask for private files
	PrivateFileMask = 0600

	// GravityDBFile is a default file name for gravity sqlite DB file
	GravityDBFile = "gravity.db"

	// SystemAccountID is the ID of the system account
	SystemAccountID = "00000000-0000-0000-0000-000000000001"
	// SystemAccountOrg is the default name of Gravitational organization
	SystemAccountOrg = "gravitational.io"

	// WizardUser is a default auto-created user used in wizard mode
	WizardUser = "wizard@gravitational.io"

	// WizardPassword is a default password used for wizard
	WizardPassword = "gravity!"

	// WizardLinkTTL is the interval the remote access link to interactive wizard
	// expires after in case of successful installation
	WizardLinkTTL = 4 * time.Hour

	// WizardConnectionGraceTTL is the time interval after which reverse tunnel
	// from cluster to the installer process expires after completing the
	// final installation step
	WizardConnectionGraceTTL = 60 * time.Second

	// FetchLimit is a default fetch limit for range objects
	FetchLimit = 100

	// DialTimeout is a default TCP dial timeout we set for our
	// connection attempts
	DialTimeout = 30 * time.Second

	// ConnectionDeadlineTimeout specifies the connection deadline timeout for use
	// with the vhost muxer.
	// The muxer uses specified deadline for the duration of its routing decision and resets
	// it afterwards
	ConnectionDeadlineTimeout = 20 * time.Second

	// ConnectionIdleTimeout is a default connection timeout used to extend
	// idle connection deadline
	ConnectionIdleTimeout = 2 * time.Minute

	// ReadHeadersTimeout is a default TCP timeout when we wait
	// for the response headers to arrive
	ReadHeadersTimeout = 30 * time.Second

	// KeepAliveTimeout tells for how long keep the connection alive with no activity
	KeepAliveTimeout = 30 * time.Second

	// MaxIdleConnsPerHost specifies the max amount of idle HTTP connections to keep
	MaxIdleConnsPerHost = 500

	// DBOpenTimeout is a default timeout for opening the DB
	DBOpenTimeout = 30 * time.Second

	// AgentRequestTimeout defines the maximum amount of time an agent is blocked on a request
	AgentRequestTimeout = 10 * time.Second

	// HeartbeatPeriod specifies default heartbeat period
	HeartbeatPeriod = 3 * time.Second

	// MissedHeartbeats is the amount of missed heartbeats that will be considered as node failure
	MissedHeartbeats = 30

	// GracePeriod is a period for GC not to delete undetected files
	// to prevent accidental deletion
	GracePeriod = 24 * time.Hour

	// APIPrefix defines the URL prefix for kubernetes-related queries tunneled from a master node
	APIPrefix = "/k8s"
	// APIServerPort defines the port of the kubernetes API server
	APIServerPort = 8080
	// APIServerSecurePort is api server secure port
	APIServerSecurePort = 6443

	// KubeForwarderUser is the identity used to generate a certificate
	// for access to kubernetes API server on secure port.
	// It is used to provide compatibility for older versions of kubernetes
	KubeForwarderUser = "kubelet"

	// LogServicePrefix defines the URL prefix for the internal log tailing and configuration service
	// tunneled from a master node
	LogServicePrefix = "/logs"
	// LogServicePort defines the port the logging service is listening on
	LogServicePort = 8083
	// LogServiceName defines the name the internal logging service is accessible on
	LogServiceName = "log-collector"
	// LogServiceAPIVersion defines the current version of the log service API
	LogServiceAPIVersion = "v1"

	// LogForwardersConfigMap is the name of the config map that contains log forwarders configuration
	LogForwardersConfigMap = "log-forwarders"

	// GrafanaServiceName is the name of Grafana service
	GrafanaServiceName = "grafana"
	// GrafanaServicePort is the port Grafana service is listening on
	GrafanaServicePort = 3000

	// InfluxDBServiceAddr is the address of InfluxDB service
	InfluxDBServiceAddr = "influxdb.monitoring.svc.cluster.local"
	// InfluxDBServicePort is the API port of InfluxDB service
	InfluxDBServicePort = 8086
	// InfluxDBAdminUser is the InfluxDB admin user name
	InfluxDBAdminUser = "root"
	// InfluxDBAdminPassword is the InfluxDB admin user password
	InfluxDBAdminPassword = "root"

	// WriteFactor is a default amount of acknowledged writes for object storage
	// to be considered successfull
	WriteFactor = 1

	// ElectionTerm is a leader election term for multiple gravity instances
	ElectionTerm = 10 * time.Second

	// HealthListenAddr is a default healthcheck address
	HealthListenAddr = "0.0.0.0:33010"

	// LocalPublicAddr is address of the local server that serves user traffic
	// behind SNI router
	LocalPublicAddr = "127.0.0.1:3011"
	// LocalAgentsAddr is address of the local server that serves cluster
	// traffic behind SNI router
	LocalAgentsAddr = "127.0.0.1:3012"

	// ManifestFileName is the name of the application manifest
	ManifestFileName = "app.yaml"

	// RegistryDir is the name of the layers directory inside an application tarball
	RegistryDir = "registry"

	// CheckForUpdatesInterval is how often local gravity site will attempt to check
	// for new app versions with OpsCenter
	CheckForUpdatesInterval = 10 * time.Second

	// LicenseCheckInterval is how often local gravity site will check the installed license
	LicenseCheckInterval = 1 * time.Minute

	// SiteStatusCheckInterval is how often local gravity site will invoke app status hook
	SiteStatusCheckInterval = 1 * time.Minute

	// OfflineCheckInterval is how often OpsCenter checks whether its sites are online/offline
	OfflineCheckInterval = 10 * time.Second

	// RegistrySyncInterval is how often app's images are synced with the local registry
	RegistrySyncInterval = 20 * time.Second

	// KubeSystemNamespace is the name of k8s namespace where all our system stuff goes
	KubeSystemNamespace = "kube-system"
	// MonitoringNamespace is the name of k8s namespace for the monitoring-related resources
	MonitoringNamespace = "monitoring"

	// SystemServiceWantedBy sets default target for system services installed by gravity
	SystemServiceWantedBy = "multi-user.target"
	// SystemServiceRestartSec is a default restart period for system services installed by gravity
	SystemServiceRestartSec = 5
	// SystemServiceTasksMax is a default amount of tasks allowed in systemd unit
	SystemServiceTasksMax = "infinity"
	// SystemdTasksMinVersion is the version of systemd that added support for TasksMax setting
	SystemdTasksMinVersion = 227

	// GravityServiceHost defines the address internal gravity site is located at
	GravityServiceHost = "gravity-site.kube-system.svc.cluster.local"

	// GravityServicePort defines the address internal gravity site is located at
	GravityServicePort = 3009

	// GravityListenPort is the port number where gravity process serves its API
	GravityListenPort = 3009
	// GravityPublicListenPort is the port number where gravity process serves
	// user traffic (such as UI and web API) if it's separated in Ops Center mode
	GravityPublicListenPort = 3007

	// ServiceAddrSuffix is the DNS name suffix appended to service addresses
	ServiceAddrSuffix = ".svc.cluster.local"

	// KubeletURL defines the default address of the local instance of the k8s kubelet
	KubeletURL = "https://localhost:10250"

	// ClientCacheSize is the size of the RPC clients expiring cache
	ClientCacheSize = 1024

	// ClientCacheTTL is ttl for clients cache expiration
	ClientCacheTTL = 60 * time.Second

	// MaxSiteLabels is the maximum number of labels allowed per site
	MaxSiteLabels = 40
	// MaxSiteLabelKeyLength is the maximum length of a label key
	MaxSiteLabelKeyLength = 127
	// MaxSiteLabelValLength is the maximum length of a label value
	MaxSiteLabelValLength = 255

	// MaxMasterNodes defines the maximum number of master nodes in the cluster.
	// Nodes beyond this number are created as regular nodes
	MaxMasterNodes = 3

	// ReportTarball is the name of the gzipped tarball with collected site report information
	ReportTarball = "report.tar.gz"

	// ServiceSubnet is a subnet dedicated to the services in cluster
	ServiceSubnet = "10.100.0.0/16"
	// PodSubnet is a subnet dedicated to the pods in the cluster
	PodSubnet = "10.244.0.0/16"

	// MaxRouterIdleConnsPerHost defines tha maximum number of idle connections for "opsroute" transport
	MaxRouterIdleConnsPerHost = 5

	// KubernetesHostnameLabel is the name of kubernetes label what contains host's IP
	KubernetesHostnameLabel = "kubernetes.io/hostname"

	// KubernetesRoleLabel is the Kubernetes node label with system role
	KubernetesRoleLabel = "gravitational.io/k8s-role"

	// KubernetesAdvertiseIPLabel is the kubernetes node label of the advertise IP address
	KubernetesAdvertiseIPLabel = "gravitational.io/advertise-ip"

	// RunLevelLabel is the Kubernetes node taint label representing a run-level
	RunLevelLabel = "gravitational.io/runlevel"

	// RunLevelSystem is the Kubernetes run-level for system applications
	RunLevelSystem = "system"

	// RoleMaster is the master nodes label
	RoleMaster = "master"

	// DockerDeviceCapacity defines the baseline size for the docker devicemapper device
	// used by default if no backend and no size has been explicitely specified
	DockerDeviceCapacity = "4GB"

	// DockerBridge specifies the default name of the docker bridge
	DockerBridge = "docker0"

	// DockerCertsDir is the directory where Docker looks for certs
	DockerCertsDir = "/etc/docker/certs.d"

	// HairpinMode specifies the default hairpin mode
	HairpinMode = constants.HairpinModePromiscuousBridge

	// VendorPattern is the default app vendor pattern that matches all yaml files
	VendorPattern = "**/*.yaml"

	// LocalDataDir is a default directory where gravity stores its local data
	LocalDataDir = ".gravity"

	// DistributionOpsCenter is the address of OpsCenter used for distributing dependencies for app builds
	DistributionOpsCenter = "https://get.gravitational.io"
	// DistributionOpsCenterUsername is the read-only disribution OpsCenter username
	DistributionOpsCenterUsername = "reader@gravitational.com"
	// DistributionOpsCenterPassword is the password for the distribution OpsCenter user
	DistributionOpsCenterPassword = "knowL3dge?"

	// GravitySiteNodePort is a default site NodePort load balancer port
	GravitySiteNodePort = 32009

	// OIDCConnectorID is a default OIDC connector to use
	OIDCConnectorID = "google"

	// OIDCCallbackTimeout is timeout waiting for OIDC callback
	OIDCCallbackTimeout = 60 * time.Second

	// TeleportServerQueryTimeout specifies the maximum amount of time allotted to query
	// teleport servers
	TeleportServerQueryTimeout = 30 * time.Second

	// APIVersion is a current version of APIs
	APIVersion = "v1"

	// LocalConfigDir is a default directory where gravity stores its user local config
	LocalConfigDir = ".gravity"

	// LocalConfigFile is a default filename where gravity stores its user local config
	LocalConfigFile = "config"

	// KubeConfigDir is a default directory where k8s stores its user local config
	KubeConfigDir = ".kube"

	// KubeConfigFile is a default filename where k8s stores its user local config
	KubeConfigFile = "config"

	// HomeDir is the default home dir
	HomeDir = "/home"

	// SSHDir is the .ssh directory
	SSHDir = ".ssh"

	// SSHUser is a default user used in SSH sessions
	// TODO(klizhentas) what user to choose, this should be site-specific and use principle of least privilege
	SSHUser = "root"

	// HTTPSPort is a default HTTPS port
	HTTPSPort = "443"

	// WizardAuthServerPort defines alternative port for auth server
	WizardAuthServerPort = 61025

	// WizardProxyServerPort defines alternative port for proxy server
	WizardProxyServerPort = 61023

	// WizardReverseTunnelPort defines alternative port for reverse tunnel service
	WizardReverseTunnelPort = 61024

	// WizardSSHServerPort defines alternative port for SSH server
	WizardSSHServerPort = 61022

	// WizardWebProxyPort defines alternative port for web proxy server
	WizardWebProxyPort = 61080

	// WizardPackServerPort defines alternative port for package service
	WizardPackServerPort = 61009

	// WizardHealthPort defines alternative port for gravity's health endpoint
	WizardHealthPort = 61010

	// TeleportPublicIPv4Label is the name of teleport command label containing server's public IP
	TeleportPublicIPv4Label = "public-ipv4"

	// TeleportCommandLabelInterval is the interval for teleport command labels
	TeleportCommandLabelInterval = 5 * time.Second

	// PeriodicUpdatesMinInterval is the minimum allowed interval for periodic updates checks
	PeriodicUpdatesMinInterval = 10 * time.Second

	// PeriodicUpdatesInterval is the default periodic updates check interval
	PeriodicUpdatesInterval = time.Hour

	// PeriodicUpdatesTickInterval is the periodic updates ticker interval
	PeriodicUpdatesTickInterval = 1 * time.Minute

	// LocalResolverAddr is address of local DNS resolver
	LocalResolverAddr = "127.0.0.1:53"

	// DecoderBufferSize is the size of the buffer used when decoding YAML resources
	DecoderBufferSize = 1024 * 1024

	// DiskCapacity is the minimum required free disk space for some default directories
	DiskCapacity = "5GB"
	// DiskTransferRate is the minimum required disk speed for some default locations
	DiskTransferRate = "10MB/s"

	// PingPongDuration is the duration of a ping-pong game agents play
	PingPongDuration = 10 * time.Second
	// BandwidthTestPort is the port for the bandwidth test agents do
	BandwidthTestPort = 4242
	// BandwidthTestDuration is the duration of a bandwidth test agents do
	BandwidthTestDuration = 20 * time.Second
	// BandwidthTestMaxServers is the maximum amount of servers participating in the bandwidth test
	BandwidthTestMaxServers = 3
	// BandwidthMaxSpeedBytes is the theoretical upper bound on the amount of types transferred per
	// second during bandwidth test, which is used in HDR histogram
	BandwidthMaxSpeedBytes = 100000000000 // 100GB

	// Runtime is the name of default runtime application
	Runtime = "kubernetes"

	// UsedSecondFactorTokenTTL is the time we keep used second factor token
	// to avoid reusing it on replay attacks
	UsedSecondFactorTokenTTL = 30 * time.Second

	// Namespace is a default namespace
	Namespace = "default"

	// AWSRegion is the default AWS region
	AWSRegion = "us-east-1"
	// AWSVPCCIDR is the default AWS VPC CIDR
	AWSVPCCIDR = "10.100.0.0/16"
	// AWSSubnetCIDR is the default AWS subnet CIDR
	AWSSubnetCIDR = "10.100.0.0/24"

	// ApplicationLabel defines the label used to annotate kubernetes resources
	// to group them together
	ApplicationLabel = "app"

	// MaxOutOfSyncTimeDelta allows maximum out of sync time
	MaxOutOfSyncTimeDelta = 300 * time.Millisecond

	// GravityServiceHostEnv defines the gravity service host in environment
	GravityServiceHostEnv = "GRAVITY_SITE_SERVICE_HOST"

	// GravityServicePortEnv defines the gravity service port in environment
	GravityServicePortEnv = "GRAVITY_SITE_SERVICE_PORT_WEB"

	// GravityClusterLabel defines the label to select cluster controller Pods
	GravityClusterLabel = "gravity-site"

	// GravityOpsCenterLabel defines the label for Ops Center related resources
	GravityOpsCenterLabel = "gravity-opscenter"

	// KubeDNSLabel defines the label to select cluster DNS service Pods
	KubeDNSLabel = "kubedns"

	// ShrinkAgentServiceName specifies the name of the systemd unit file
	// that executes a shrink agent on a remote node
	ShrinkAgentServiceName = "shrink-agent.service"

	// SystemUnitDir specifies the location of user-specific service units
	SystemUnitDir = "/etc/systemd/system"

	// EtcdLocalAddr is the local etcd address
	EtcdLocalAddr = "https://127.0.0.1:2379"
	// EtcdKey is the key under which gravity data is stored in etcd
	EtcdKey = "/gravity/local"
	// EtcdKeyFilename is the etcd private key filename
	EtcdKeyFilename = "etcd.key"
	// EtcdCertFilename is the etcd certificate filename
	EtcdCertFilename = "etcd.cert"
	// EtcdCtlBin is /usr/bin/etcdctl
	EtcdCtlBin = "/usr/bin/etcdctl"

	// EtcdUpgradeBackupFile is the filename to store a temporary backup of the etcd database when recreating the etcd datastore
	EtcdUpgradeBackupFile = "etcd.bak"

	// EtcdPeerPort is etcd inter-cluster communication port
	EtcdPeerPort = 2380
	// EtcdAPIPort is etcd client API port
	EtcdAPIPort = 2379

	// SchedulerKeyFilename is the kube-scheduler private key filename
	SchedulerKeyFilename = "scheduler.key"
	// SchedulerCertFilename is the kube-scheduler certificate filename
	SchedulerCertFilename = "scheduler.cert"

	// KubeletKeyFilename is the kubelet private key filename
	KubeletKeyFilename = "kubelet.key"
	// KubeletCertFilename is the kubelet certificate filename
	KubeletCertFilename = "kubelet.cert"

	// RootCertFilename is the certificate authority certificate filename
	RootCertFilename = "root.cert"

	// RPCAgentBackoffThreshold defines max communication delay before retrying connection to remote agent node
	RPCAgentBackoffThreshold = 1 * time.Minute

	// RPCAgentShutdownTimeout defines the timeout to wait for agents to shutdown
	// upon completing an operation
	RPCAgentShutdownTimeout = 1 * time.Minute

	// RPCAgentSecretsPackage specifies the name of the RPC credentials package
	RPCAgentSecretsPackage = "rpcagent-secrets"

	// ArchiveUID specifies the user ID to use for tarball items that do not exist on disk
	ArchiveUID = 1000

	// ArchiveGID specifies the group ID to use for tarball items that do not exist on disk
	ArchiveGID = 1000

	// EndpointsWaitTimeout specifies the timeout for waiting for system service endpoints
	EndpointsWaitTimeout = 5 * time.Minute

	// DrainErrorTimeout specifies the timeout for the initial failures of drain operation.
	// Drain operation might experience transient errors (e.g. api server connect failures)
	// in which case the timeout defines the maximum time frame to retry such failed attempts.
	// After the operation has started, it can take much longer than the specified timeout
	// to complete.
	DrainErrorTimeout = 15 * time.Minute

	// DrainTimeout defines the total drain operation timeout
	DrainTimeout = 1 * time.Hour

	// TerminationWaitTimeout defines an amount of time above the Kubernetes
	// TerminationGracePeriod to wait for a pod to be terminated. Kubernetes
	// may take some amount of time to force kill a pod, which we want to
	// allow for.
	TerminationWaitTimeout = 30 * time.Second

	// WaitStatusInterval specifies the frequency of status checking in wait a operation
	WaitStatusInterval = 1 * time.Second

	// ResourceGracePeriod forces a kubernetes operation to use the default grace period defined
	// for a resource
	ResourceGracePeriod = -1

	// KubeletUpdatePermissionsRole defines the names of clusterrole/clusterrolebinding
	// for kubelet used to add missing permissions during upgrade of the first
	// master node
	KubeletUpdatePermissionsRole = "kubelet-upgrade"

	// SSHInstallPort is default port for SSH install agents
	SSHInstallPort = "33008"

	// SMTPPort defines the SMTP service port
	SMTPPort = 465

	// ServiceUser specifies the name of the user used as a service user in planet
	// as well as for unprivileged (system) kubernetes resources.
	ServiceUser = "planet"
	// ServiceUserGroup specifies the name of the user group used for a service user in planet.
	ServiceUserGroup = "planet"
	// ServiceUserID specifies the ID of the service user created by default.
	// This is the value used in previous versions and serves the purpose of keeping
	// the same defaults if the user is not overridden.
	ServiceUserID = "1000"
	// ServiceUID is a numeric default service user ID
	ServiceUID = 1000
	// ServiceGroupID specifies the ID of the service group created by default.
	// This is the value used in previous versions and serves the purpose of keeping
	// the same defaults if the user is not overridden.
	ServiceGroupID = "1000"
	// ServiceGID is a numeric default service group ID
	ServiceGID = 1000
	// PlaceholderUserID is a placeholder for a real user ID.
	// Used to differentiate a valid ID from an empty value
	PlaceholderUserID = -1
	// PlaceholderGroupID is a placeholder for a real group ID
	// Used to differentiate a valid ID from an empty value
	PlaceholderGroupID = -1

	// ImageServiceMaxThreads specifies the concurrency limit for I/O operations
	// of the distribution local filesystem driver
	ImageServiceMaxThreads = 100

	// HubBucket is the name of S3 bucket that stores binaries and artifacts
	HubBucket = "hub.gravitational.io"
	// HubTelekubePrefix is key prefix under which Telekube artifacts are stored
	HubTelekubePrefix = "gravity/oss"

	// ValidateCommand defines the command executed to verify the connectivity
	// with a remote node
	ValidateCommand = "/bin/true"

	// VxlanPort is the port used for overlay network
	VxlanPort = 8472

	// DNSListenAddr is the default address coredns will be configured to listen on
	DNSListenAddr = "127.0.0.2"

	// LegacyDNSListenAddr is the address coredns was configured to listen on
	// in older environments
	LegacyDNSListenAddr = "127.0.0.1"

	// DNSPort is the default DNS port coredns will be configured with
	DNSPort = 53

	// ModulesPath is the path to the list of gravity-specific kernel modules loaded at boot
	ModulesPath = "/etc/modules-load.d/gravity.conf"
	// SysctlPath is the path to gravity-specific kernel parameters configuration
	SysctlPath = "/etc/sysctl.d/50-gravity.conf"

	// RemoteClusterDialAddr is the "from" address used when dialing remote cluster
	RemoteClusterDialAddr = "127.0.0.1:3024"

	// ElectionWaitTimeout specifies the maximum amount of time to wait to resume elections
	// on a master node
	ElectionWaitTimeout = 1 * time.Minute

	// AgentDeployTimeout specifies the maximum amount of time to wait to deploy agents
	// for an operation that spans multiple nodes
	AgentDeployTimeout = 5 * time.Minute
)

var (
	// GravityServiceURL defines the address the internal gravity site is located
	GravityServiceURL = fmt.Sprintf("https://%s:%d", GravityServiceHost, GravityServicePort)

	// KubernetesAPIAddress is the Kubernetes API address
	KubernetesAPIAddress = fmt.Sprintf("%s:%d", constants.APIServerDomainName, APIServerSecurePort)
	// KubernetesAPIURL is the Kubernetes API URL
	KubernetesAPIURL = fmt.Sprintf("https://%s", KubernetesAPIAddress)

	// GravityRPCAgentDir specifies the directory used by the RPC agent
	GravityRPCAgentDir = filepath.Join(GravityUpdateDir, "agent")

	// GravityConfigDirs specify default locations for gravity configuration search
	GravityConfigDirs = []string{GravityDir, "assets/local"}

	// GravityJoinDir is where join FSM stores its information on the joining node
	GravityJoinDir = filepath.Join(GravityEphemeralDir, "join")

	// RPCAgentSecretsDir specifies the location of the unpacked credentials
	RPCAgentSecretsDir = filepath.Join(GravityEphemeralDir, "rpcsecrets")

	// WizardDir is where wizard login information is stored during install
	WizardDir = filepath.Join(GravityEphemeralDir, "wizard")

	// LocalCacheDir is the location where gravity stores downloaded packages
	LocalCacheDir = filepath.Join(LocalDataDir, "cache")

	// UsedNamespaces lists the Kubernetes namespaces used by default
	UsedNamespaces = []string{"default", "kube-system"}

	// KubernetesReportResourceTypes lists the kubernetes resource types used in diagnostics report
	KubernetesReportResourceTypes = []string{"pods", "jobs", "services", "daemonsets", "deployments",
		"endpoints", "replicationcontrollers", "replicasets"}

	// LogServiceURL is the URL of logging app API running in the cluster
	LogServiceURL = fmt.Sprintf("http://%v:%v",
		fmt.Sprintf(ServiceAddr, LogServiceName, KubeSystemNamespace), LogServicePort)

	// RSAPrivateKeyBits is default bits for RSA private key
	RSAPrivateKeyBits = 4096

	// HookContainerNameTag identifies the container image used for application hooks
	HookContainerNameTag = "gravitational/debian-tall:0.0.1"

	// UpdateAppSyncTimeout defines the maximum amount of time to sync application
	// state with an updated node during update
	UpdateAppSyncTimeout = 5 * time.Minute

	// ContainerEnvironmentFile specifies the location of the file for container environment
	ContainerEnvironmentFile = "/etc/container-environment"

	// BandwagonPackageName is the name of bandwagon app package
	BandwagonPackageName = "bandwagon"
	// BandwagonServiceName is the name of the default setup endpoint service
	BandwagonServiceName = "bandwagon"

	// KubeletArgs is a list of default command line options for kubelet
	KubeletArgs = []string{
		`--eviction-hard="nodefs.available<5%,imagefs.available<5%,nodefs.inodesFree<5%,imagefs.inodesFree<5%"`,
		`--eviction-soft="nodefs.available<10%,imagefs.available<10%,nodefs.inodesFree<10%,imagefs.inodesFree<10%"`,
		`--eviction-soft-grace-period="nodefs.available=1h,imagefs.available=1h,nodefs.inodesFree=1h,imagefs.inodesFree=1h"`,
	}

	// InstallGroupTTL is for how long installer IP is kept in a TTL map in
	// an install group
	InstallGroupTTL = 10 * time.Second

	// LocalWizardURL is the local URL of the wizard process API
	LocalWizardURL = fmt.Sprintf("https://%v:%v", constants.Localhost,
		WizardPackServerPort)

	// GravitySiteSelector is a label for a gravity-site pod
	GravitySiteSelector = map[string]string{
		ApplicationLabel: GravityClusterLabel,
	}

	// LBIdleTimeout is the idle timeout for AWS load balancers
	LBIdleTimeout = "3600"

	// DiscoveryPublishInterval specifies the frequency to update cluster discovery
	// details
	DiscoveryPublishInterval = 5 * time.Second

	// CACertificateExpiry is the validity period of self-signed CA generated
	// for clusters during installation
	CACertificateExpiry = 20 * 365 * 24 * time.Hour // 20 years
	// CertificateExpiry is the validity period of certificates generated
	// during cluster installation (such as apiserver, etcd, kubelet, etc.)
	CertificateExpiry = 10 * 365 * 24 * time.Hour // 10 years

	// TelekubeSystemLog defines the default location for the system log
	TelekubeSystemLog = filepath.Join(SystemLogDir, TelekubeSystemLogFile)

	// TelekubeUserLog the default location for user-facing log file
	TelekubeUserLog = filepath.Join(SystemLogDir, TelekubeUserLogFile)

	// TransientErrorTimeout specifies the maximum amount of time to attempt
	// an operation experiencing transient errors
	TransientErrorTimeout = 15 * time.Minute
)

// HookSecurityContext returns default securityContext for hook pods
func HookSecurityContext() *v1.PodSecurityContext {
	var (
		runAsNonRoot bool  = false
		runAsUser    int64 = 0
		fsGroup      int64 = 0
	)

	return &v1.PodSecurityContext{
		RunAsNonRoot: &runAsNonRoot,
		RunAsUser:    &runAsUser,
		FSGroup:      &fsGroup,
	}
}

// InGravity builds a directory path within gravity working directory
func InGravity(parts ...string) string {
	return filepath.Join(append([]string{GravityDir}, parts...)...)
}

// Secret returns full path to the specified secret file
func Secret(filename string) string {
	return InGravity(SecretsDir, filename)
}

// AWSPublicIPv4Command is a command to query AWS metadata for an instance's public IP address
var AWSPublicIPv4Command = []string{"/usr/bin/bash", "-c", `curl -s -f -m 4 http://169.254.169.254/latest/meta-data/public-ipv4 || true`}

// ContainerImage is the image implicitly bundled with any application package.
// It is used for init container in hooks as well as update controller
var ContainerImage = fmt.Sprintf("quay.io/%v", HookContainerNameTag)

// ServiceAddr is the template for a Kubernetes service address
var ServiceAddr = fmt.Sprintf("%%v.%%v%v", ServiceAddrSuffix)

// BaseTaintsVersion sets the minimum version with support
// for node taints and tolerations in system applications
var BaseTaintsVersion = semver.Must(semver.NewVersion("4.36.0"))

// BaseUpdateVersion sets the minimum version that this binary
// can update
var BaseUpdateVersion = semver.Must(semver.NewVersion("3.51.0"))

// DockerRegistryAddr returns the address of docker registry running on server
func DockerRegistryAddr(server string) string {
	return fmt.Sprintf("%v:%v", server, constants.DockerRegistryPort)
}

// InSystemUnitDir returns the path of the user service given with serviceName
func InSystemUnitDir(serviceName string) string {
	return filepath.Join(SystemUnitDir, serviceName)
}

// InTempDir returns the specified subpath inside default tmp directory
func InTempDir(path ...string) string {
	return filepath.Join(append([]string{"/tmp"}, path...)...)
}

// GravityRPCAgentAddr returns default RPC agent advertise address
func GravityRPCAgentAddr(host string) string {
	return fmt.Sprintf("%v:%v", host, GravityRPCAgentPort)
}

// WithTimeout returns a default timeout context
func WithTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, RetryAttempts*RetryInterval)
}
