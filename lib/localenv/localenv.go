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

package localenv

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	appbase "github.com/gravitational/gravity/lib/app"
	appclient "github.com/gravitational/gravity/lib/app/client"
	"github.com/gravitational/gravity/lib/app/docker"
	appservice "github.com/gravitational/gravity/lib/app/service"
	"github.com/gravitational/gravity/lib/blob"
	"github.com/gravitational/gravity/lib/blob/fs"
	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/httplib"
	"github.com/gravitational/gravity/lib/ops/opsclient"
	"github.com/gravitational/gravity/lib/pack"
	"github.com/gravitational/gravity/lib/pack/localpack"
	"github.com/gravitational/gravity/lib/pack/webpack"
	"github.com/gravitational/gravity/lib/state"
	"github.com/gravitational/gravity/lib/storage"
	"github.com/gravitational/gravity/lib/storage/keyval"
	"github.com/gravitational/gravity/lib/users"
	"github.com/gravitational/gravity/lib/users/usersservice"
	"github.com/gravitational/gravity/lib/utils"
	"github.com/gravitational/gravity/tool/common"

	"github.com/gravitational/roundtrip"
	"github.com/gravitational/trace"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
)

var log = logrus.WithField(trace.Component, "local")

// LocalEnvironmentArgs holds configuration values for opening or creating a LocalEnvironment
type LocalEnvironmentArgs struct {
	// LocalKeyStoreDir specifies an optional directory in which to place the LocalKeyStore
	// for holding user and auth state
	LocalKeyStoreDir string
	// StateDir specifes the directory in which state (gravity db, packages) will be placed
	StateDir string
	// Insecure indicates whether or not to perform TLS name verification
	Insecure bool
	// Silent indicates whether or not LocalEnvironment operations will log or not
	Silent
	// Debug indicates whether or not the command is run in debug mode
	Debug bool
	// EtcdRetryTimeout specifies the timeout on ETCD transient errors.
	// Defaults to EtcdRetryInterval if unspecified
	EtcdRetryTimeout time.Duration
	// Reporter controls progress output
	Reporter pack.ProgressReporter
	// DNS is the local cluster DNS server configuration
	DNS DNSConfig
}

// Addr returns the first listen address of the DNS server
func (r DNSConfig) Addr() string {
	if len(r.Addrs) == 0 {
		return storage.DefaultDNSConfig.Addr()
	}
	return (storage.DNSConfig)(r).Addr()
}

// IsEmpty returns whether this DNS configuration is empty
func (r DNSConfig) IsEmpty() bool {
	return (storage.DNSConfig)(r).IsEmpty()
}

// DNSConfig is the DNS configuration with a fallback to storage.DefaultDNSConfig
type DNSConfig storage.DNSConfig

// LocalEnvironment sets up local gravity environment
// and services that make sense for it:
//
// * local package service
// * local site service
// * access to local OpsCenter
type LocalEnvironment struct {
	LocalEnvironmentArgs

	// Backend is the local backend client
	Backend storage.Backend
	// Objects is the local objects storage client
	Objects blob.Objects
	// Packages is the local package service
	Packages *localpack.PackageServer
	// Apps is the local application service
	Apps appbase.Applications
	// Creds is the local key store
	Creds *users.KeyStore
}

// GetLocalKeyStore opens a key store in the specified directory dir. If one does
// not exist, it will be created. If dir is empty, a default key store location is
// used.
func GetLocalKeyStore(dir string) (*users.KeyStore, error) {
	configPath := ""
	if dir != "" {
		configPath = path.Join(dir, defaults.LocalConfigFile)
	}

	keys, err := usersservice.NewLocalKeyStore(configPath)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return keys, nil
}

// New is a shortcut that creates a local environment from provided state directory
func New(stateDir string) (*LocalEnvironment, error) {
	return NewLocalEnvironment(LocalEnvironmentArgs{StateDir: stateDir})
}

// NewLocalEnvironment creates a new LocalEnvironment given the specified configuration
// arguments.
// It is caller's responsibility to close the environment with Close after use
func NewLocalEnvironment(args LocalEnvironmentArgs) (*LocalEnvironment, error) {
	if args.StateDir == "" {
		return nil, trace.BadParameter("missing parameter StateDir")
	}

	log.Debugf("Creating local env: %#v.", args)

	var err error
	args.StateDir, err = filepath.Abs(args.StateDir)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	env := &LocalEnvironment{LocalEnvironmentArgs: args}
	if err = env.init(); err != nil {
		env.Close()
		return nil, trace.Wrap(err)
	}
	return env, nil
}

func (env *LocalEnvironment) init() error {
	err := os.MkdirAll(env.StateDir, defaults.PrivateDirMask)
	if err != nil {
		return trace.Wrap(err)
	}

	env.Backend, err = keyval.NewBolt(keyval.BoltConfig{
		Path:  filepath.Join(env.StateDir, defaults.GravityDBFile),
		Multi: true,
	})
	if err != nil {
		return trace.Wrap(err)
	}

	if env.DNS.IsEmpty() {
		dns, err := storage.GetDNSConfig(env.Backend, storage.LegacyDNSConfig)
		if err != nil {
			return trace.Wrap(err)
		}
		env.DNS = DNSConfig(*dns)
	}

	env.Objects, err = fs.New(filepath.Join(env.StateDir, defaults.PackagesDir))
	if err != nil {
		return trace.Wrap(err)
	}
	env.Packages, err = localpack.New(localpack.Config{
		UnpackedDir: filepath.Join(env.StateDir, defaults.PackagesDir, defaults.UnpackedDir),
		Backend:     env.Backend,
		Objects:     env.Objects,
	})
	if err != nil {
		return trace.Wrap(err)
	}
	env.Apps, err = env.AppServiceLocal(AppConfig{})
	if err != nil {
		return trace.Wrap(err)
	}
	env.Creds, err = users.NewCredsService(users.CredsConfig{
		Backend: env.Backend,
	})
	if err != nil {
		return trace.Wrap(err)
	}

	return nil
}

// Close closes backend and object storage used in LocalEnvironment
func (env *LocalEnvironment) Close() error {
	var errors []error
	if env.Backend != nil {
		errors = append(errors, env.Backend.Close())
		env.Backend = nil
	}
	if env.Objects != nil {
		errors = append(errors, env.Objects.Close())
		env.Objects = nil
	}
	env.Packages = nil
	env.Creds = nil
	return trace.NewAggregate(errors...)
}

func (env *LocalEnvironment) GetLoginEntry(opsCenterURL string) (*users.LoginEntry, error) {
	parsedOpsCenterURL := utils.ParseOpsCenterAddress(opsCenterURL, defaults.HTTPSPort)
	keys, err := GetLocalKeyStore(env.LocalKeyStoreDir)
	if err == nil {
		entry, err := keys.GetLoginEntry(parsedOpsCenterURL)
		if err == nil {
			log.Debugf("Found login entry for %v @ %v.", entry.Email, opsCenterURL)
			return entry, nil
		}
		entry, err = keys.GetLoginEntry(opsCenterURL)
		if err == nil {
			log.Debugf("Found login entry for %v @ %v.", entry.Email, opsCenterURL)
			return entry, nil
		}
	}
	entry, err := env.Creds.GetLoginEntry(opsCenterURL)
	if err != nil {
		if !trace.IsNotFound(err) {
			return nil, trace.Wrap(err)
		}
		if opsCenterURL == defaults.DistributionOpsCenter {
			return &users.LoginEntry{
				OpsCenterURL: opsCenterURL,
				Email:        defaults.DistributionOpsCenterUsername,
				Password:     defaults.DistributionOpsCenterPassword,
			}, nil
		}
		return nil, trace.NotFound("Please login to Ops Center: %v",
			opsCenterURL)
	}
	return entry, nil
}

func (env *LocalEnvironment) UpsertLoginEntry(opsCenterURL, username, password string) error {
	keys, err := GetLocalKeyStore(env.LocalKeyStoreDir)
	if err != nil {
		return trace.Wrap(err)
	}
	if username == "" && password == "" {
		username, password, err = common.ReadUserPass()
		if err != nil {
			return trace.Wrap(err)
		}
	}
	_, err = keys.UpsertLoginEntry(users.LoginEntry{
		OpsCenterURL: opsCenterURL,
		Email:        username,
		Password:     password,
	})
	return trace.Wrap(err)
}

func (env *LocalEnvironment) SelectOpsCenter(opsURL string) (string, error) {
	if opsURL != "" {
		return opsURL, nil
	}
	keys, err := GetLocalKeyStore(env.LocalKeyStoreDir)
	if err == nil {
		opsURL = keys.GetCurrentOpsCenter()
		if opsURL != "" {
			return opsURL, nil
		}
	}
	entries, err := env.Creds.GetLoginEntries()
	if err != nil && !trace.IsNotFound(err) {
		return "", trace.Wrap(err)
	}
	if len(entries) == 0 {
		return "", trace.AccessDenied("Please login to Ops Center: %v",
			opsURL)
	}
	if len(entries) != 1 {
		return "", trace.AccessDenied("Please login to Ops Center: %v",
			opsURL)
	}
	return entries[0].OpsCenterURL, nil
}

func (env *LocalEnvironment) SelectOpsCenterWithDefault(opsURL, defaultURL string) (string, error) {
	url, err := env.SelectOpsCenter(opsURL)
	if err != nil {
		if !trace.IsAccessDenied(err) {
			return "", trace.Wrap(err)
		}
		if defaultURL != "" {
			return defaultURL, nil
		}
		return "", trace.AccessDenied("Please login to Ops Center: %v",
			opsURL)
	}
	return url, nil
}

// Printf outputs specified arguments to stdout if the silent mode is not on.
func (env *LocalEnvironment) Printf(format string, args ...interface{}) (n int, err error) {
	log.Debugf(format, args...)
	if !env.Silent {
		return fmt.Printf(format, args...)
	}
	return 0, nil
}

// Println outputs specified arguments to stdout if the silent mode is not on.
func (env *LocalEnvironment) Println(args ...interface{}) (n int, err error) {
	log.Debugln(args...)
	if !env.Silent {
		return fmt.Println(args...)
	}
	return 0, nil
}

// PrintStep outputs the message with timestamp to stdout
func (env *LocalEnvironment) PrintStep(format string, args ...interface{}) (n int, err error) {
	log.Debugf(format, args...)
	if !env.Silent {
		return fmt.Printf("%v\t%v\n", time.Now().UTC().Format(
			constants.HumanDateFormatSeconds), fmt.Sprintf(format, args...))
	}
	return 0, nil
}

// Write outputs specified arguments to stdout if the silent mode is not on.
// Write implements io.Writer
func (env *LocalEnvironment) Write(p []byte) (n int, err error) {
	return env.Printf(string(p))
}

func (env *LocalEnvironment) HTTPClient(options ...httplib.ClientOption) *http.Client {
	return httplib.GetClient(env.Insecure, options...)
}

// PackageService returns a service managing gravity packages on the specified OpsCenter
// or the local packages if the OpsCenter has not been specified.
func (env *LocalEnvironment) PackageService(opsCenterURL string, options ...httplib.ClientOption) (pack.PackageService, error) {
	if opsCenterURL == "" { // assume local OpsCenter
		return env.Packages, nil
	}

	if opsCenterURL == defaults.GravityServiceURL {
		options = append(options, httplib.WithLocalResolver(env.DNS.Addr()))
	}

	// otherwise connect to remote OpsCenter
	entry, err := env.GetLoginEntry(opsCenterURL)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	httpClient := roundtrip.HTTPClient(env.HTTPClient(options...))
	client, err := newPackClient(*entry, opsCenterURL, httpClient)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return client, nil
}

// CurrentLogin returns the login entry for the cluster this environment
// is currently logged into
//
// If there are no entries or more than a single entry, it returns an error
func (env *LocalEnvironment) CurrentLogin() (*users.LoginEntry, error) {
	opsCenterURL, err := env.SelectOpsCenter("")
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return env.GetLoginEntry(opsCenterURL)
}

// CurrentOperator returns operator for the current login entry
func (env *LocalEnvironment) CurrentOperator(options ...httplib.ClientOption) (*opsclient.Client, error) {
	entry, err := env.CurrentLogin()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return NewOpsClient(*entry, entry.OpsCenterURL,
		opsclient.HTTPClient(env.HTTPClient(options...)))
}

// CurrentPackages returns package service for the current login entry
func (env *LocalEnvironment) CurrentPackages(options ...httplib.ClientOption) (pack.PackageService, error) {
	entry, err := env.CurrentLogin()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return newPackClient(*entry, entry.OpsCenterURL,
		roundtrip.HTTPClient(env.HTTPClient(options...)))
}

// CurrentApps returns app service for the current login entry
func (env *LocalEnvironment) CurrentApps(options ...httplib.ClientOption) (appbase.Applications, error) {
	entry, err := env.CurrentLogin()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return newAppsClient(*entry, entry.OpsCenterURL,
		appclient.HTTPClient(env.HTTPClient(options...)))
}

// CurrentUser returns name of the currently logged in user
func (env *LocalEnvironment) CurrentUser() string {
	login, err := env.CurrentLogin()
	if err != nil {
		if !trace.IsNotFound(err) {
			log.Errorf("Failed to get current login entry: %v.",
				trace.DebugReport(err))
		}
		return ""
	}
	return login.Email
}

// OperatorService provides access to remote sites and creates new sites
func (env *LocalEnvironment) OperatorService(opsCenterURL string, options ...httplib.ClientOption) (*opsclient.Client, error) {
	opsCenterURL, err := env.SelectOpsCenter(opsCenterURL)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	entry, err := env.GetLoginEntry(opsCenterURL)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	params := []opsclient.ClientParam{
		opsclient.HTTPClient(env.HTTPClient(options...)),
		opsclient.WithLocalDialer(httplib.LocalResolverDialer(env.DNS.Addr())),
	}
	client, err := NewOpsClient(*entry, opsCenterURL, params...)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return client, nil
}

// SiteOperator returns Operator for the local gravity site
func (env *LocalEnvironment) SiteOperator() (*opsclient.Client, error) {
	operator, err := env.OperatorService(
		defaults.GravityServiceURL, httplib.WithLocalResolver(env.DNS.Addr()), httplib.WithInsecure())
	return operator, trace.Wrap(err)
}

// SiteApps returns Apps service for the local gravity site
func (env *LocalEnvironment) SiteApps() (appbase.Applications, error) {
	apps, err := env.AppService(
		defaults.GravityServiceURL, AppConfig{}, httplib.WithLocalResolver(env.DNS.Addr()), httplib.WithInsecure())
	return apps, trace.Wrap(err)
}

// ClusterPackages returns package service for the local cluster
func (env *LocalEnvironment) ClusterPackages() (pack.PackageService, error) {
	return env.PackageService(defaults.GravityServiceURL,
		httplib.WithLocalResolver(env.DNS.Addr()), httplib.WithInsecure())
}

func (env *LocalEnvironment) AppService(opsCenterURL string, config AppConfig, options ...httplib.ClientOption) (appbase.Applications, error) {
	if opsCenterURL == "" {
		return env.AppServiceLocal(config)
	}
	entry, err := env.GetLoginEntry(opsCenterURL)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	client, err := newAppsClient(*entry, opsCenterURL,
		appclient.HTTPClient(env.HTTPClient(options...)),
		appclient.WithLocalDialer(httplib.LocalResolverDialer(env.DNS.Addr())))
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return client, nil
}

func (env *LocalEnvironment) AppServiceLocal(config AppConfig) (service appbase.Applications, err error) {
	var imageService docker.ImageService
	var dockerClient docker.DockerInterface
	if config.RegistryURL != "" {
		imageService, err = docker.NewImageService(docker.RegistryConnectionRequest{
			RegistryAddress: config.RegistryURL,
		})
		if err != nil {
			return nil, trace.Wrap(err)
		}
	}
	if config.DockerURL != "" {
		dockerClient, err = docker.NewClient(config.DockerURL)
		if err != nil {
			return nil, trace.Wrap(err)
		}
	}
	var packages pack.PackageService
	if config.Packages != nil {
		packages = config.Packages
	} else {
		packages = env.Packages
	}

	return appservice.New(appservice.Config{
		Backend:      env.Backend,
		Packages:     packages,
		DockerClient: dockerClient,
		ImageService: imageService,
		StateDir:     filepath.Join(env.StateDir, "import"),
		Devmode:      env.Debug,
		UnpackedDir:  filepath.Join(env.StateDir, defaults.PackagesDir, defaults.UnpackedDir),
		GetClient:    env.getKubeClient,
	})
}

// GravityCommandInPlanet builds gravity command that runs inside planet
func (env *LocalEnvironment) GravityCommandInPlanet(args ...string) []string {
	command := []string{defaults.GravityBin}
	if env.Debug {
		command = append(command, "--debug")
	}
	if env.Insecure {
		command = append(command, "--insecure")
	}
	return append(command, args...)
}

// GravityCommand builds gravity command
func (env *LocalEnvironment) GravityCommand(gravityPath string, args ...string) []string {
	command := []string{gravityPath}
	if env.Debug {
		command = append(command, "--debug")
	}
	if env.Insecure {
		command = append(command, "--insecure")
	}
	return append(command, args...)
}

func (env *LocalEnvironment) getKubeClient() (*kubernetes.Clientset, error) {
	_, err := os.Stat(constants.PrivilegedKubeconfig)
	if err == nil {
		return utils.GetKubeClientFromPath(constants.PrivilegedKubeconfig)
	}
	log.Warnf("Privileged kubeconfig unavailable, falling back to cluster client: %v.", err)

	if env.DNS.IsEmpty() {
		return nil, nil
	}

	client, err := httplib.GetClusterKubeClient(env.DNS.Addr())
	if err != nil {
		log.Warnf("Failed to create cluster kube client: %v.", err)
		return nil, trace.Wrap(err)
	}

	return client, nil
}

// AppConfig is applications-specific configuration
type AppConfig struct {
	// DockerURL specifies the address of the docker daemon
	DockerURL string
	// RegistryURL is the address of the private docker registry
	// running inside a kubernetes cluster.
	//
	// This attribute is only applicable in a local planet environment
	RegistryURL string
	// Packages allow to override default env.Packages when creating
	// an app service
	Packages pack.PackageService
}

// NewOpsClient creates a new client to Operator service using the specified
// login entry, address of the Ops Center and a set of optional connection
// options
func NewOpsClient(entry users.LoginEntry, opsCenterURL string, params ...opsclient.ClientParam) (client *opsclient.Client, err error) {
	if entry.Email != "" {
		client, err = opsclient.NewAuthenticatedClient(
			opsCenterURL, entry.Email, entry.Password, params...)
	} else {
		client, err = opsclient.NewBearerClient(opsCenterURL, entry.Password, params...)
	}
	return client, trace.Wrap(err)
}

func newPackClient(entry users.LoginEntry, opsCenterURL string, params ...roundtrip.ClientParam) (client pack.PackageService, err error) {
	if entry.Email != "" {
		client, err = webpack.NewAuthenticatedClient(
			opsCenterURL, entry.Email, entry.Password, params...)
	} else {
		client, err = webpack.NewBearerClient(opsCenterURL, entry.Password, params...)
	}
	return client, trace.Wrap(err)
}

func newAppsClient(entry users.LoginEntry, opsCenterURL string, params ...appclient.ClientParam) (client appbase.Applications, err error) {
	if entry.Email != "" {
		client, err = appclient.NewAuthenticatedClient(
			opsCenterURL, entry.Email, entry.Password, params...)
	} else {
		client, err = appclient.NewBearerClient(
			opsCenterURL, entry.Password, params...)
	}
	return client, trace.Wrap(err)
}

// ClusterPackages returns the local cluster packages service
func ClusterPackages() (pack.PackageService, error) {
	stateDir, err := LocalGravityDir()
	if err != nil {
		return nil, trace.Wrap(err)
	}

	env, err := NewLocalEnvironment(LocalEnvironmentArgs{
		StateDir: stateDir,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	defer env.Close()

	packages, err := env.PackageService(
		defaults.GravityServiceURL, httplib.WithLocalResolver(env.DNS.Addr()), httplib.WithInsecure())
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return packages, nil
}

// ClusterOperator returns the local cluster ops service
func ClusterOperator() (*opsclient.Client, error) {
	stateDir, err := LocalGravityDir()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	env, err := NewLocalEnvironment(LocalEnvironmentArgs{
		StateDir: stateDir,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	defer env.Close()
	operator, err := env.SiteOperator()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return operator, nil
}

// InGravity returns full path to specified subdirectory of local state dir
func InGravity(dir ...string) (string, error) {
	stateDir, err := state.GetStateDir()
	if err != nil {
		return "", trace.Wrap(err)
	}
	return filepath.Join(append([]string{stateDir}, dir...)...), nil
}

// LocalGravityDir returns host directory where local environment stores its data on this node
func LocalGravityDir() (string, error) {
	dir, err := InGravity(defaults.LocalDir)
	if err != nil {
		return "", trace.Wrap(err)
	}
	return dir, nil
}

// SiteDir returns host directory where gravity site stores its data on this node
func SiteDir() (string, error) {
	dir, err := InGravity(defaults.SiteDir)
	if err != nil {
		return "", trace.Wrap(err)
	}
	return dir, nil
}

// SitePackagesDir returns host directory where packages are stored on this node
func SitePackagesDir() (string, error) {
	dir, err := InGravity(defaults.SiteDir, defaults.PackagesDir)
	if err != nil {
		return "", trace.Wrap(err)
	}
	return dir, nil
}

// SiteUnpackedDir returns host directory where unpacked packages are stored on this node
func SiteUnpackedDir() (string, error) {
	dir, err := InGravity(defaults.SiteDir, defaults.PackagesDir, defaults.UnpackedDir)
	if err != nil {
		return "", trace.Wrap(err)
	}
	return dir, nil
}

// Printf outputs specified arguments to stdout if the silent mode is not on.
func (r Silent) Printf(format string, args ...interface{}) (n int, err error) {
	if !r {
		return fmt.Printf(format, args...)
	}
	return 0, nil
}

// Print outputs specified arguments to stdout if the silent mode is not on.
func (r Silent) Print(args ...interface{}) (n int, err error) {
	if !r {
		return fmt.Print(args...)
	}
	return 0, nil
}

// Println outputs specified arguments to stdout if the silent mode is not on.
func (r Silent) Println(args ...interface{}) (n int, err error) {
	if !r {
		return fmt.Println(args...)
	}
	return 0, nil
}

// Write outputs specified arguments to stdout if the silent mode is not on.
// Write implements io.Writer
func (r Silent) Write(p []byte) (n int, err error) {
	return r.Printf(string(p))
}

// Silent implements a silent flag and controls console output.
// Implements Printer
type Silent bool

// Printer describes a capability to output to standard output
type Printer interface {
	io.Writer
	Printf(format string, args ...interface{}) (int, error)
	Print(args ...interface{}) (int, error)
	Println(args ...interface{}) (int, error)
}
