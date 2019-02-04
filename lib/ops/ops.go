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

package ops

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/gravitational/gravity/lib/checks"
	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/loc"
	"github.com/gravitational/gravity/lib/ops/monitoring"
	"github.com/gravitational/gravity/lib/pack"
	"github.com/gravitational/gravity/lib/schema"
	"github.com/gravitational/gravity/lib/storage"
	"github.com/gravitational/gravity/lib/utils"

	"github.com/cloudflare/cfssl/csr"
	"github.com/cloudflare/cfssl/signer"
	"github.com/gravitational/license"
	"github.com/gravitational/satellite/agent/proto/agentpb"
	teleauth "github.com/gravitational/teleport/lib/auth"
	teleclient "github.com/gravitational/teleport/lib/client"
	teleservices "github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/trace"
)

// TeleportProxyService is SSH proxy access portal - gives
// access to remote sites via SSH
type TeleportProxyService interface {
	// ReverseTunnelAddr is the address for
	// remote teleport cluster nodes to dial back
	ReverseTunnelAddr() string

	// CertAuthorities returns a list of certificate
	// authorities proxy wants remote teleport sites to trust.
	// withPrivateKey defines if the private key is also exported
	CertAuthorities(withPrivateKey bool) ([]teleservices.CertAuthority, error)

	// DeleteAuthority deletes teleport authorities for the provided
	// site name
	DeleteAuthority(domainName string) error

	// TrustCertAuthority sets up trust for certificate authority
	TrustCertAuthority(teleservices.CertAuthority) error

	// GetServers returns a list of servers matching particular label key value
	// pair expression and returns a list of servers
	// domainName is a site domain name
	GetServers(ctx context.Context, domainName string, labels map[string]string) ([]teleservices.Server, error)

	// GetServerCount returns a number of servers belonging to a particular site
	GetServerCount(ctx context.Context, domainName string) (int, error)

	// ExecuteCommand executes a command on a remote node addrress
	// for a given site domain
	ExecuteCommand(ctx context.Context, domainName, nodeAddr, command string, out io.Writer) error

	// GetClient returns admin client to local proxy
	GetClient() teleauth.ClientI

	// GenerateUserCert signs SSH public key with certificate authority of this proxy's user CA
	GenerateUserCert(pub []byte, user string, ttl time.Duration) ([]byte, error)

	// GetLocalAuthorityDomain returns domain for local CA authority
	GetLocalAuthorityDomain() string

	// GetCertAuthorities returns a list of cert authorities this proxy trusts
	GetCertAuthorities(caType teleservices.CertAuthType) ([]teleservices.CertAuthority, error)

	// GetPlanetLeaderIP returns the IP address of the active planet leader
	GetPlanetLeaderIP() string

	// GetProxyClient returns proxy client
	GetProxyClient(ctx context.Context, siteName string, labels map[string]string) (*teleclient.ProxyClient, error)
}

// Operator is capable of adding and deleting sites,
// updgrades and downgrades and modifying existing sites
type Operator interface {
	Accounts
	Applications
	Users
	APIKeys
	Sites
	Status
	Operations
	Validation
	LogForwarders
	Monitoring
	SMTP
	Endpoints
	Tokens
	Certificates
	Leader
	Install
	Updates
	Identity
}

// Accounts represents a collection of accounts in the portal
type Accounts interface {
	// GetAccount returns account by id
	GetAccount(accountID string) (*Account, error)

	// GetAccounts returns a list of accounts registered in the system
	GetAccounts() ([]Account, error)

	// CreateAccount creates a new account
	CreateAccount(NewAccountRequest) (*Account, error)
}

// UserInfo represents information about current user
type UserInfo struct {
	// User identifies the user
	User storage.User `json:"user"`
	// KubernetesGroups lists all groups the user has access to
	KubernetesGroups []string `json:"kubernetes_groups"`
}

// ToCSR returns a certificate signing request for this user
func (u UserInfo) ToCSR() csr.CertificateRequest {
	request := csr.CertificateRequest{
		CN: u.User.GetName(),
	}
	for _, group := range u.KubernetesGroups {
		request.Names = append(request.Names, csr.Name{O: group})
	}
	return request
}

// ToRaw returns wire-friendly representation of the request
// that does not uses any interfaces
func (u *UserInfo) ToRaw() (*UserInfoRaw, error) {
	raw := UserInfoRaw{
		KubernetesGroups: u.KubernetesGroups,
	}
	var err error
	raw.User, err = teleservices.GetUserMarshaler().MarshalUser(u.User.WithoutSecrets())
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &raw, nil
}

// UserInfoRaw defines a wire-friendly user representation
type UserInfoRaw struct {
	// User defines the user details in unstructured form
	User json.RawMessage `json:"user"`
	// KubernetesGroups lists all groups the user has access to
	KubernetesGroups []string `json:"kubernetes_groups"`
}

// ToNative converts back to request that has all interfaces inside
func (u *UserInfoRaw) ToNative() (*UserInfo, error) {
	native := UserInfo{
		KubernetesGroups: u.KubernetesGroups,
	}
	var err error
	native.User, err = storage.UnmarshalUser(u.User)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return &native, nil
}

// Users represents a collection of users in the portal
type Users interface {
	// CreateUser creates a new user
	CreateUser(NewUserRequest) error

	// DeleteLocalUser deletes a user by name
	DeleteLocalUser(name string) error

	// GetCurrentUser returns user that is currently logged in
	GetCurrentUser() (storage.User, error)

	// GetCurrentUserInfo returns extended information
	// about user
	GetCurrentUserInfo() (*UserInfo, error)

	// GetLocalUser returns the local gravity site user
	GetLocalUser(SiteKey) (storage.User, error)

	// ResetUserPassword resets the user password and returns the new one
	ResetUserPassword(ResetUserPasswordRequest) (string, error)

	// GetClusterAgent returns the specified cluster agent
	GetClusterAgent(ClusterAgentRequest) (*storage.LoginEntry, error)
}

// ResetUserPasswordRequest is a request to reset gravity site user password
type ResetUserPasswordRequest struct {
	// AccountID is the ID of the account the site belongs to
	AccountID string `json:"account_id"`
	// SiteDomain is the site name to deactivate
	SiteDomain string `json:"site_domain"`
	// Email is the email of the user to reset password for
	Email string `json:"email"`
}

// ClusterAgentRequest is a request to retrieve a cluster agent
type ClusterAgentRequest struct {
	// AccountID is the ID of the cluster account
	AccountID string `json:"account_id"`
	// ClusterName is the cluster name
	ClusterName string `json:"cluster_name"`
	// Admin is whether to retrieve a regular or admin agent
	Admin bool `json:"admin"`
}

// APIKeys represents a collection of user API keys
type APIKeys interface {
	// CreateAPIKey creates a new API key for a user
	CreateAPIKey(NewAPIKeyRequest) (*storage.APIKey, error)

	// GetAPIKeys returns API keys for the specified user
	GetAPIKeys(userEmail string) ([]storage.APIKey, error)

	// DeleteAPIKey deletes an API key
	DeleteAPIKey(userEmail, token string) error
}

// Tokens represents a token management layer
type Tokens interface {
	// CreateInstallToken creates a one-time install token
	CreateInstallToken(NewInstallTokenRequest) (*storage.InstallToken, error)
	// CreateProvisioningToken creates a new provisioning token
	CreateProvisioningToken(storage.ProvisioningToken) error
	// GetExpandToken returns the cluster's expand token
	GetExpandToken(SiteKey) (*storage.ProvisioningToken, error)
	// GetTrustedClusterToken returns the cluster's trusted cluster token
	GetTrustedClusterToken(SiteKey) (storage.Token, error)
}

// Sites represents a collection of site records, where
// each site is a group of servers and installed application
type Sites interface {
	// CreateSite creates a new site record
	CreateSite(NewSiteRequest) (*Site, error)

	// DeleteSite deletes the site record without
	// uninstalling actual resources, the site must be
	// explicitly uninstalled for resources to be freed,
	// see SiteUninstallOperation methods
	DeleteSite(SiteKey) error

	// GetSiteByDomain returns site record by it's domain name for a given
	// account
	GetSiteByDomain(domainName string) (*Site, error)

	// GetSite returns site by it's key
	GetSite(SiteKey) (*Site, error)

	// GetLocalSite returns local site for this ops center
	GetLocalSite() (*Site, error)

	// GetSites sites lists all site records for account
	GetSites(accountID string) ([]Site, error)

	// DeactivateSite puts the site in the degraded state and, if requested,
	// stops an application
	DeactivateSite(DeactivateSiteRequest) error

	// ActivateSite moves site to the active state and, if requested, starts
	// an application
	ActivateSite(ActivateSiteRequest) error

	// CompleteFinalInstallStep marks the site as having completed the mandatory last installation step
	CompleteFinalInstallStep(CompleteFinalInstallStepRequest) error

	// GetSiteReport returns a tarball that contains all debugging information gathered for the site
	GetSiteReport(SiteKey) (io.ReadCloser, error)

	// SignTLSKey signs X509 Public Key with X509 certificate authority of this site
	SignTLSKey(TLSSignRequest) (*TLSSignResponse, error)

	// SignSSHKey signs SSH Public Key with teleport's certificate
	SignSSHKey(SSHSignRequest) (*SSHSignResponse, error)
}

// TLSSignRequest is a request to sign x509 PublicKey with site's local certificate authority
type TLSSignRequest struct {
	// AccountID is account id
	AccountID string `json:"account_id"`
	// SiteDomain is a site domain
	SiteDomain string `json:"site_domain"`
	// CSR is x509 CSR sign request
	CSR []byte `json:"csr"`
	// Subject is checked and set by Access Control Layer
	// if not provided, CSR values will be used
	Subject *signer.Subject `json:"-"`
	// TTL is a desired TTL, will be capped by server settings
	TTL time.Duration `json:"ttl"`
}

func (req *TLSSignRequest) SiteKey() SiteKey {
	return SiteKey{
		AccountID:  req.AccountID,
		SiteDomain: req.SiteDomain,
	}
}

// TLSSignResponse is the response to TLSSignRequest
type TLSSignResponse struct {
	// Cert is x509 Certificate
	Cert []byte `json:"cert"`
	// CACert is TLS CA certificate to trust
	CACert []byte `json:"ca_cert"`
}

// SSHSignRequest is a request to sign SSH public Key with teleport's certificate
type SSHSignRequest struct {
	// User is SSH user to get with certificate
	User string `json:"user"`
	// AccountID is Site Account ID
	AccountID string `json:"account_id"`
	// PublicKey is SSH public key to sign
	PublicKey []byte `json:"public_key"`
	// TTL is a desired TTL for the cert (max is still capped by server,
	// however user can shorten the time)
	TTL time.Duration `json:"ttl"`
	// AllowedLogins is a list of linux allowed logins
	// is set by access controller and is ignored from request
	AllowedLogins []string `json:"-"`
}

// SSHSignResponse is a response to SSHSignRequest
type SSHSignResponse struct {
	// Cert is a signed SSH certificate
	Cert []byte `json:"cert"`
	// TrustedHostAuthorities is a list of trusted host authorities of sites
	TrustedHostAuthorities []teleservices.CertAuthority `json:"trusted_authorities"`
}

// ToRaw returns wire-friendly representation of the request
// that does not uses any interfaces
func (s *SSHSignResponse) ToRaw() (*SSHSignResponseRaw, error) {
	raw := SSHSignResponseRaw{
		Cert:                   s.Cert,
		TrustedHostAuthorities: make([]json.RawMessage, 0, len(s.TrustedHostAuthorities)),
	}
	for i := range s.TrustedHostAuthorities {
		cert := s.TrustedHostAuthorities[i]
		data, err := teleservices.GetCertAuthorityMarshaler().MarshalCertAuthority(cert, teleservices.WithVersion(teleservices.V1))
		if err != nil {
			return nil, trace.Wrap(err)
		}
		raw.TrustedHostAuthorities = append(raw.TrustedHostAuthorities, data)
	}
	return &raw, nil
}

// SSHSignResponseRaw is a response to SSHSignRequest
// that has cert authorities marshaled in old format
type SSHSignResponseRaw struct {
	// Cert is a signed SSH certificate
	Cert []byte `json:"cert"`
	// TrustedHostAuthorities is a list of trusted host authorities of sites
	TrustedHostAuthorities []json.RawMessage `json:"trusted_authorities"`
}

// ToNative converts back to request that has all interfaces inside
func (s *SSHSignResponseRaw) ToNative() (*SSHSignResponse, error) {
	native := SSHSignResponse{
		Cert:                   s.Cert,
		TrustedHostAuthorities: make([]teleservices.CertAuthority, 0, len(s.TrustedHostAuthorities)),
	}
	for i := range s.TrustedHostAuthorities {
		ca, err := teleservices.GetCertAuthorityMarshaler().UnmarshalCertAuthority(s.TrustedHostAuthorities[i])
		if err != nil {
			return nil, trace.Wrap(err)
		}
		native.TrustedHostAuthorities = append(native.TrustedHostAuthorities, ca)
	}
	return &native, nil
}

// DeactivateSiteRequest describes a request to deactivate a site
type DeactivateSiteRequest struct {
	// AccountID is the ID of the account the site belongs to
	AccountID string `json:"account_id"`
	// SiteDomain is the site name to deactivate
	SiteDomain string `json:"site_domain"`
	// Reason is the deactivation reason
	Reason storage.Reason `json:"reason"`
	// StopApp controls whether the site's app should be stopped
	StopApp bool `json:"stop_app"`
}

// AcivateSiteRequest is a request to activate a site
type ActivateSiteRequest struct {
	// AccountID is the ID of the account the site belongs to
	AccountID string `json:"account_id"`
	// SiteDomain is the site name to activate
	SiteDomain string `json:"site_domain"`
	// StartApp controls whether the site's app should be started
	StartApp bool `json:"start_app"`
}

// CompleteFinalInstallStepRequest is a request to mark site final install step as completed
type CompleteFinalInstallStepRequest struct {
	// AccountID is the ID of the account the site belongs to
	AccountID string `json:"account_id"`
	// SiteDomain is the site name to activate
	SiteDomain string `json:"site_domain"`
	// WizardConnectionTTL is when to expire connection to wizard process
	WizardConnectionTTL time.Duration `json:"delay"`
}

// CheckAndSetDefaults validates the request and fills in default values
func (r *CompleteFinalInstallStepRequest) CheckAndSetDefaults() error {
	if r.AccountID == "" {
		return trace.BadParameter("missing AccountID")
	}
	if r.SiteDomain == "" {
		return trace.BadParameter("missing SiteDomain")
	}
	if r.WizardConnectionTTL == 0 {
		r.WizardConnectionTTL = defaults.WizardConnectionGraceTTL
	}
	return nil
}

// Certificates contains methods for operating on cluster certificates
type Certificates interface {
	// GetClusterCertificate returns the cluster TLS certificate that is
	// presented by the cluster's local web endpoint
	GetClusterCertificate(key SiteKey, withSecrets bool) (*ClusterCertificate, error)
	// UpdateClusterCertificate updates the cluster TLS certificate that is
	// presented by the cluster's local web endpoint
	UpdateClusterCertificate(UpdateCertificateRequest) (*ClusterCertificate, error)
	// DeleteClusterCertificate deletes the cluster TLS certificate
	DeleteClusterCertificate(SiteKey) error
}

// ClusterCertificate represents the cluster certificate
type ClusterCertificate struct {
	// Certificate is the cluster certificate
	Certificate []byte `json:"certificate"`
	// PrivateKey is the private key
	PrivateKey []byte `json:"private_key"`
}

// UpdateCertificateRequest is the request to update the cluster certificate
type UpdateCertificateRequest struct {
	// AccountID is the cluster's account ID
	AccountID string `json:"account_id"`
	// SiteDomain is the cluster name
	SiteDomain string `json:"site_domain"`
	// Certificate is the new cluster certificate
	Certificate []byte `json:"certificate"`
	// PrivateKey is the certificate's private key
	PrivateKey []byte `json:"private_key"`
	// Intermediate is an optional certificate chain
	Intermediate []byte `json:"intermediate"`
}

// Check makes sure the update certificate request is valid
func (r UpdateCertificateRequest) Check() error {
	if r.AccountID == "" {
		return trace.BadParameter("missing parameter AccountID")
	}
	if r.SiteDomain == "" {
		return trace.BadParameter("missing parameter SiteDomain")
	}
	if len(r.Certificate) == 0 {
		return trace.BadParameter("missing parameter Certificate")
	}
	if len(r.PrivateKey) == 0 {
		return trace.BadParameter("missing parameter PrivateKey")
	}
	// make sure certificate and key are in correct format and match each other
	_, err := tls.X509KeyPair(append(r.Certificate, r.Intermediate...), r.PrivateKey)
	if err != nil {
		return trace.Wrap(err, "failed to parse certificate / key pair")
	}
	return nil
}

// Leader defines leadership-related operations
type Leader interface {
	// StepDown asks the process to pause its leader election heartbeat so it can
	// give up its leadership
	StepDown(SiteKey) error
}

// Status defines operations with site status
type Status interface {
	// CheckSiteStatus runs app status hook and updates site status appropriately
	CheckSiteStatus(key SiteKey) error
	// GetClusterNodes returns a real-time information about cluster nodes
	GetClusterNodes(SiteKey) ([]Node, error)
}

// Node represents a cluster node information
type Node struct {
	// Hostname is the node hostname
	Hostname string `json:"hostname"`
	// AdvertiseIP is the node advertise IP
	AdvertiseIP string `json:"advertise_ip"`
	// PublicIP is the node public IP
	PublicIP string `json:"public_ip"`
	// Profile is the node profile
	Profile string `json:"profile"`
	// InstanceType is the node instance type
	InstanceType string `json:"instance_type"`
}

// Operations installs and uninstalls gravity on a given site,
// it takes care of provisioning, configuring and deploying end user application
// as well as our system packages like planet and teleport
type Operations interface {
	// GetSiteInstructions returns shell script with instructions
	// to execute for particular install agent
	// params are url query parameters that are optional
	// and can specify selected interface, and other things
	GetSiteInstructions(token string, serverProfile string, params url.Values) (string, error)

	// GetSiteOperations returns a list of operations executed for this site
	GetSiteOperations(key SiteKey) (SiteOperations, error)

	// CreateSiteInstallOperation initiates install operation for the site
	// this operation can be currently run only once
	//
	// 1. This method is called as a first step to initiate install operation.
	CreateSiteInstallOperation(CreateSiteInstallOperationRequest) (*SiteOperationKey, error)

	// GetSiteInstallOperationAgentReport returns runtime information
	// about servers as reported by remote install agents
	//
	// 2. This method is called as a second step to get information
	// about servers participating in the operations
	GetSiteInstallOperationAgentReport(SiteOperationKey) (*AgentReport, error)

	// SiteInstallOperationStart begins actuall install using
	// the Operation plan configured as a previous step
	//
	// 3. This method is called as a third step to begin install
	SiteInstallOperationStart(SiteOperationKey) error

	// CreateSiteUninstallOperation initiates uninstall operation
	// for this site that will delete all machines and state inlcuding
	// it kicks off uninstall of the site immediatelly
	CreateSiteUninstallOperation(CreateSiteUninstallOperationRequest) (*SiteOperationKey, error)

	// CreateClusterGarbageCollectOperation creates a new garbage collection operation
	// in the cluster
	CreateClusterGarbageCollectOperation(CreateClusterGarbageCollectOperationRequest) (*SiteOperationKey, error)

	// GetsiteOperation returns the operation information based on it's key
	GetSiteOperation(SiteOperationKey) (*SiteOperation, error)

	// GetOperationLogs returns a stream of actions executed
	// in the context of this operation
	//
	// This method is called after operation start to retrieve a stream of logs
	// related to this operation periodically
	GetSiteOperationLogs(SiteOperationKey) (io.ReadCloser, error)

	// CreateLogEntry appends the provided log entry to the operation's log file
	CreateLogEntry(SiteOperationKey, LogEntry) error

	// GetSiteOperationProgress returns last progress entry of a given operation
	//
	// This method is called periodically after operation start
	// process to get the progress report
	GetSiteOperationProgress(SiteOperationKey) (*ProgressEntry, error)

	// CreateProgressEntry creates a new progress entry for the specified
	// operation
	CreateProgressEntry(SiteOperationKey, ProgressEntry) error

	// GetSiteOperationCrashReport returns a tarball with crash report
	// that contains all debugging information gathered during the operation
	//
	// This method can be called in case of progress report failed state
	// after operation start
	GetSiteOperationCrashReport(SiteOperationKey) (io.ReadCloser, error)

	// CreateSiteExpandOperation initiates operation that adds nodes
	// to the cluster
	//
	// 1. This method is called as a first step to initiate expand operation
	CreateSiteExpandOperation(CreateSiteExpandOperationRequest) (*SiteOperationKey, error)

	// GetSiteExpandOperationAgentReport returns runtime information
	// about servers as reported by remote install agents
	//
	// 2. This method is called as a second step to get information
	// about servers participating in the operations
	GetSiteExpandOperationAgentReport(SiteOperationKey) (*AgentReport, error)

	// SiteExpandOperationStart begins actuall expand using
	// the Operation plan configured as a previous step
	//
	// 3. This method is called as a third step to begin expansion
	SiteExpandOperationStart(SiteOperationKey) error

	// CreateSiteShrinkOperation initiates an operation that removes nodes
	// from the cluster
	CreateSiteShrinkOperation(CreateSiteShrinkOperationRequest) (*SiteOperationKey, error)

	// CreateSiteAppUpdateOpeation initiates an operation that updates an application
	// installed on a site to a new version
	CreateSiteAppUpdateOperation(CreateSiteAppUpdateOperationRequest) (*SiteOperationKey, error)

	// ResumeShrink resumes the started shrink operation if the node being shrunk gave up
	// its leadership
	ResumeShrink(key SiteKey) (*SiteOperationKey, error)

	// UpdateInstallOperationState updates the state of an install operation
	UpdateInstallOperationState(key SiteOperationKey, req OperationUpdateRequest) error

	// UpdateExpandOperationState updates the state of an expand operation
	UpdateExpandOperationState(key SiteOperationKey, req OperationUpdateRequest) error

	// DeleteSiteOperation removes an unstarted operation
	DeleteSiteOperation(SiteOperationKey) error

	// SetOperationState moves operation into specified state
	SetOperationState(key SiteOperationKey, req SetOperationStateRequest) error

	// CreateOperationPlan saves the provided operation plan
	CreateOperationPlan(SiteOperationKey, storage.OperationPlan) error

	// CreateOperationPlanChange creates a new changelog entry for a plan
	CreateOperationPlanChange(SiteOperationKey, storage.PlanChange) error

	// GetOperationPlan returns plan for the specified operation
	GetOperationPlan(SiteOperationKey) (*storage.OperationPlan, error)
}

// LogEntry represents a single log line for an operation
type LogEntry struct {
	// AccountID is the ID of the account for the operation
	AccountID string `json:"account_id"`
	// ClusterName is the name of the cluster for the operation
	ClusterName string `json:"cluster_name"`
	// OperationID is the ID of the operation the log entry is for
	OperationID string `json:"operation_id"`
	// Severity is the log entry severity: info, warning or error
	Severity string `json:"severity"`
	// Message is the log entry text message
	Message string `json:"message"`
	// Server is an optional server that generated the log entry
	Server *storage.Server `json:"server,omitempty"`
	// Created is the log entry timestamp
	Created time.Time `json:"created"`
}

// String formats the log entry as a string
func (l LogEntry) String() string {
	var server string
	if l.Server != nil {
		server = fmt.Sprintf(" [%v]", l.Server.Hostname)
	}
	return fmt.Sprintf("%v [%v]%v %v\n", l.Created.Format(
		constants.HumanDateFormatSeconds), strings.ToUpper(l.Severity), server,
		l.Message)
}

// Install provides install-specific methods
type Install interface {
	// ConfigurePackages configures packages for the specified operation
	ConfigurePackages(SiteOperationKey) error
	// StreamOperationLogs appends the logs from the provided reader to the
	// specified operation (user-facing) log file
	StreamOperationLogs(SiteOperationKey, io.Reader) error
}

// Updates enables manual cluster update management
type Updates interface {
	// RotateSecrets rotates secrets package for the server specified in the request
	RotateSecrets(RotateSecretsRequest) (*RotatePackageResponse, error)

	// RotatePlanetConfig rotates planet configuration package for the server specified in the request
	RotatePlanetConfig(RotatePlanetConfigRequest) (*RotatePackageResponse, error)

	// ConfigureNode prepares the node for the upgrade
	ConfigureNode(ConfigureNodeRequest) error
}

// RotatePackageResponse describes a response to generate a new package for an existing one.
type RotatePackageResponse struct {
	// Locator identifies the package
	loc.Locator `json:"locator"`
	// Reader is the package's contents
	io.Reader `json:"-"`
	// Labels specifies the labels for the new package
	Labels map[string]string `json:"labels"`
}

// ConfigureNodeRequest is a request to prepare a node for the upgrade
type ConfigureNodeRequest struct {
	// AccountID is the account id of the local cluster
	AccountID string `json:"account_id"`
	// ClusterName is the local cluster name
	ClusterName string `json:"cluster_name"`
	// OperationID is the id of the operation
	OperationID string `json:"operation_id"`
	// Server is the server to configure
	Server storage.Server `json:"server"`
}

// SiteOperationKey returns operation key for this request
func (r ConfigureNodeRequest) SiteOperationKey() SiteOperationKey {
	return SiteOperationKey{
		AccountID:   r.AccountID,
		SiteDomain:  r.ClusterName,
		OperationID: r.OperationID,
	}
}

// SiteKey returns cluster key for this request
func (r ConfigureNodeRequest) SiteKey() SiteKey {
	return SiteKey{
		AccountID:  r.AccountID,
		SiteDomain: r.ClusterName,
	}
}

// RotateSecretsRequest is a request to rotate server's secrets package
type RotateSecretsRequest struct {
	// AccountID is the account id of the local cluster
	AccountID string `json:"account_id"`
	// ClusterName is the local cluster name
	ClusterName string `json:"cluster_name"`
	// Server is the server to rotate secrets for
	Server storage.Server `json:"server"`
}

// SiteKey returns a cluster key from this request
func (r RotateSecretsRequest) SiteKey() SiteKey {
	return SiteKey{
		AccountID:  r.AccountID,
		SiteDomain: r.ClusterName,
	}
}

// RotatePlanetConfigRequest is a request to rotate server's planet configuration package
type RotatePlanetConfigRequest struct {
	// AccountID is the account id of the local cluster
	AccountID string `json:"account_id"`
	// ClusterName is the local cluster name
	ClusterName string `json:"cluster_name"`
	// OperationID is the id of the operation
	OperationID string `json:"operation_id"`
	// Server is the server to rotate configuration for
	Server storage.Server `json:"server"`
	// Servers is all cluster servers
	Servers []storage.Server `json:"servers"`
}

// SiteKey returns a cluster key from this request
func (r RotatePlanetConfigRequest) SiteKey() SiteKey {
	return SiteKey{
		AccountID:  r.AccountID,
		SiteDomain: r.ClusterName,
	}
}

// SiteOperationKey returns an operation key from this request
func (r RotatePlanetConfigRequest) SiteOperationKey() SiteOperationKey {
	return SiteOperationKey{
		AccountID:   r.AccountID,
		SiteDomain:  r.ClusterName,
		OperationID: r.OperationID,
	}
}

// Proxy helps to manage connections and clients to remote ops centers
type Proxy interface {
	GetService(storage.OpsCenterLink) (Operator, error)
}

// Applications interface handles application-specific tasks
type Applications interface {
	// GetAppInstaller generates an application installer tarball and returns
	// a binary data stream
	GetAppInstaller(AppInstallerRequest) (io.ReadCloser, error)
}

//
type AppInstallerRequest struct {
	AccountID     string
	Application   loc.Locator
	CACert        string
	EncryptionKey string
}

// SiteOperation represents any operation that is performed on the site
// e.g. installing and uninstalling applications, adding and removing nodes
// performing rolling updates
type SiteOperation storage.SiteOperation

// SiteOperations groups several site operations
type SiteOperations []storage.SiteOperation

// GetVars returns operation specific variables
func (s *SiteOperation) GetVars() storage.OperationVariables {
	if s.InstallExpand != nil {
		return s.InstallExpand.Vars
	}
	if s.Shrink != nil {
		return s.Shrink.Vars
	}
	if s.Uninstall != nil {
		return s.Uninstall.Vars
	}
	return storage.OperationVariables{}
}

// IsFailed returns whether operation is failed
func (s *SiteOperation) IsFailed() bool {
	return s.State == OperationStateFailed
}

// IsCompleted returns whether the operation has completed successfully
func (s *SiteOperation) IsCompleted() bool {
	return s.State == OperationStateCompleted
}

// IsFinished returns true if the operation has finished (succeeded or failed)
func (s *SiteOperation) IsFinished() bool {
	return s.State == OperationStateCompleted || s.State == OperationStateFailed
}

// IsAWS returns true if the operation has AWS provisioner
func (s *SiteOperation) IsAWS() bool {
	return utils.StringInSlice([]string{
		schema.ProvisionerAWSTerraform,
		schema.ProviderAWS,
	}, s.Provisioner)
}

// Key returns key structure that can uniquely identify this operation
func (s *SiteOperation) Key() SiteOperationKey {
	return SiteOperationKey{
		AccountID:   s.AccountID,
		OperationID: s.ID,
		SiteDomain:  s.SiteDomain,
	}
}

// ClusterKey returns the cluster key for this operation
func (s *SiteOperation) ClusterKey() SiteKey {
	return s.Key().SiteKey()
}

// ClusterState returns the respective cluster state based on the operation progress
func (s *SiteOperation) ClusterState() (string, error) {
	var state string
	var ok bool

	if !s.IsFinished() {
		state, ok = OperationStartedToClusterState[s.Type]
	} else if s.IsFailed() {
		state, ok = OperationFailedToClusterState[s.Type]
	} else {
		state, ok = OperationSucceededToClusterState[s.Type]
	}

	if !ok {
		return "", trace.BadParameter("unknown operation type %q", s.Type)
	}

	return state, nil
}

// String returns the textual representation of this operation
func (s *SiteOperation) String() string {
	var typeS string
	switch s.Type {
	case OperationInstall:
		typeS = "install"
	case OperationExpand:
		typeS = "expand"
	case OperationUpdate:
		typeS = "update"
	case OperationShrink:
		typeS = "shrink"
	case OperationUninstall:
		typeS = "uninstall"
	case OperationGarbageCollect:
		typeS = "garbage collect"
	}
	return fmt.Sprintf("operation(%v, cluster=%v, state=%s)", typeS, s.SiteDomain, s.State)
}

// SiteOperationKey identifies key to retrieve an opertaion
type SiteOperationKey struct {
	// AccountID is account id of this operation
	AccountID string `json:"account_id"`
	// SiteDomain is a site id of the operation
	SiteDomain string `json:"site_domain"`
	// OperationID is a unique id of the operation
	OperationID string `json:"operation_id"`
}

// SiteKey extracts site key from the operation key
func (s SiteOperationKey) SiteKey() SiteKey {
	return SiteKey{
		AccountID:  s.AccountID,
		SiteDomain: s.SiteDomain,
	}
}

// Check makes sure the key is valid
func (s SiteOperationKey) Check() error {
	if s.AccountID == "" {
		return trace.BadParameter("empty AccountID")
	}
	if s.SiteDomain == "" {
		return trace.BadParameter("empty SiteDomain")
	}
	if s.OperationID == "" {
		return trace.BadParameter("empty OperationID")
	}
	return nil
}

// CreateSiteInstallOperationRequest is a request to create
// install operation - the operation that provisions servers, gravity software
// and sets up everything
type CreateSiteInstallOperationRequest struct {
	// AccountID is account id of this operation
	AccountID string `json:"account_id"`
	// SiteID is a site of the operation
	SiteDomain string `json:"site_domain"`
	// Variables are used to set up operation specific parameters,
	// e.g. AWS image flavor for AWS install
	Variables storage.OperationVariables `json:"variables"`
	// Provisioner defines the provisioner for this operation
	Provisioner string `json:"provisioner"`
	// Profiles specifies server (role -> server profile) requirements
	Profiles map[string]storage.ServerProfileRequest `json:"profiles"`
}

// CheckAndSetDefaults validates the request and provides defaults to unset fields
func (r *CreateSiteInstallOperationRequest) CheckAndSetDefaults() error {
	if r.AccountID == "" {
		return trace.BadParameter("missing AccountID")
	}
	if r.SiteDomain == "" {
		return trace.BadParameter("missing SiteDomain")
	}
	if r.Provisioner == "" {
		return trace.BadParameter("missing Provisioner")
	}
	if r.Provisioner == schema.ProvisionerAWSTerraform {
		r.Variables.AWS.SetDefaults()
	}
	err := utils.ValidateKubernetesSubnets(r.Variables.OnPrem.PodCIDR, r.Variables.OnPrem.ServiceCIDR)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

// CreateSiteUninstallOperationRequest creates uninstall operation
// entry, it does not kick off the operation
type CreateSiteUninstallOperationRequest struct {
	// AccountID is id of the account
	AccountID string `json:"account_id"`
	// SiteDomain is the site id
	SiteDomain string `json:"site_domain"`
	// Force forces gravity to unprovision site without uninstall
	// used in development in case of broken installs
	Force bool `json:"force"`
	// Variables are used to set up operation specific parameters,
	// e.g. AWS image flavor for AWS install
	Variables storage.OperationVariables `json:"variables"`
}

// CreateSiteExpandOperationRequest is a request to add new nodes
// to the cluster
type CreateSiteExpandOperationRequest struct {
	// AccountID is account id of this operation
	AccountID string `json:"account_id"`
	// SiteDomain is a site of the operation
	SiteDomain string `json:"site_domain"`
	// Variables are used to set up operation specific parameters,
	// e.g. AWS image flavor for AWS install
	Variables storage.OperationVariables `json:"variables"`
	// Servers specifies how many server of each role this operation adds,
	// e.g. {"master": 1, "database": 2}
	Servers map[string]int `json:"servers"`
	// Provisioner to use for this operation
	Provisioner string `json:"provisioner"`
}

// CheckAndSetDefaults makes sure the request is correct and fills in some unset
// fields with default values if they have them
func (r *CreateSiteExpandOperationRequest) CheckAndSetDefaults() error {
	if r.AccountID == "" {
		return trace.BadParameter("missing AccountID")
	}
	if r.SiteDomain == "" {
		return trace.BadParameter("missing SiteDomain")
	}
	if r.Provisioner == "" {
		return trace.BadParameter("missing Provisioner")
	}
	if r.Provisioner == schema.ProvisionerAWSTerraform {
		r.Variables.AWS.SetDefaults()
	}
	return nil
}

// CreateSiteShrinkOperationRequest is a request to remove nodes from the cluster
type CreateSiteShrinkOperationRequest struct {
	// AccountID is account id of this operation
	AccountID string `json:"account_id"`
	// SiteDomain is a site of the operation
	SiteDomain string `json:"site_domain"`
	// Variables are used to set up operation specific parameters, e.g. AWS keys
	Variables storage.OperationVariables `json:"variables"`
	// Servers specifies server names to remove
	Servers []string `json:"servers"`
	// Provisioner to use for this operation
	Provisioner string `json:"provisioner"`
	// Force allows to remove offline nodes
	Force bool `json:"force"`
	// NodeRemoved indicates whether the node has already been removed from the cluster
	// Used in cases where we recieve an event where the node is being terminated, but may
	// not have disconnected from the cluster yet.
	NodeRemoved bool `json:node_removed`
}

// CheckAndSetDefaults makes sure the request is correct and fills in some unset
// fields with default values if they have them
func (r *CreateSiteShrinkOperationRequest) CheckAndSetDefaults() error {
	if r.AccountID == "" {
		return trace.BadParameter("missing AccountID")
	}
	if r.SiteDomain == "" {
		return trace.BadParameter("missing SiteDomain")
	}
	if len(r.Servers) == 0 {
		return trace.BadParameter("expected a server to remove")
	}
	if len(r.Servers) != 1 {
		return trace.BadParameter("can delete only one server at a time, got: %v", r.Servers)
	}
	return nil
}

// CreateSiteAppUpdateOperationRequest is a request to update an application
// installed on a site to a new version
type CreateSiteAppUpdateOperationRequest struct {
	// AccountID is the ID of the account the site belongs to
	AccountID string `json:"account_id"`
	// SiteDomain is the ID of the site that should be updated
	SiteDomain string `json:"site_domain"`
	// App specifies a new application package in the "locator" form, e.g. gravitational.io/mattermost:1.2.3
	App string `json:"package"`
	// StartAgents specifies whether the operation will automatically start the update agents
	StartAgents bool `json:"start_agents"`
}

// Check validates this request
func (r CreateClusterGarbageCollectOperationRequest) Check() error {
	if r.AccountID == "" {
		return trace.BadParameter("missing AccountID")
	}
	if r.ClusterName == "" {
		return trace.BadParameter("missing ClusterName")
	}
	return nil
}

// CreateClusterGarbageCollectOperationRequest is a request
// to start garbage collection in the cluster
type CreateClusterGarbageCollectOperationRequest struct {
	// AccountID is id of the account
	AccountID string `json:"account_id"`
	// ClusterName is the name of the cluster
	ClusterName string `json:"cluster_name"`
}

// AgentService coordinates install agents that are started on every server
// and report system information as well as receive instructions from
// the operator service
type AgentService interface {
	// ServerAddr returns the address of the server for agents
	// to connect to
	ServerAddr() string

	// GetServerInfos returns a list of server information objects
	GetServerInfos(ctx context.Context, key SiteOperationKey) (checks.ServerInfos, error)

	// Exec executes command on a remote server
	// that is identified by meeting point and agent's address addr
	Exec(ctx context.Context, opKey SiteOperationKey, addr string, args []string, out io.Writer) error

	// Validate executes preflight checks on the node specified with addr
	// against the specified manifest and profile.
	Validate(ctx context.Context, opKey SiteOperationKey, addr string,
		manifest schema.Manifest, profileName string) ([]*agentpb.Probe, error)

	// Wait blocks until the specified number of agents have connected for the
	// the given operation. Context can be used for canceling the operation.
	Wait(ctx context.Context, key SiteOperationKey, numAgents int) error

	// CheckPorts executes port availability test in agent cluster
	CheckPorts(context.Context, SiteOperationKey, checks.PingPongGame) (checks.PingPongGameResults, error)

	// CheckBandwidth executes bandwidth network test in agent cluster
	CheckBandwidth(context.Context, SiteOperationKey, checks.PingPongGame) (checks.PingPongGameResults, error)

	// StopAgents instructs all remote agents to stop operation
	// and rejects all consequitive requests to connect for any agent
	// for this site
	StopAgents(context.Context, SiteOperationKey) error
}

// NewAccountRequest is a request to create a new account
type NewAccountRequest struct {
	// ID is an optional account ID.
	// If specified, account with this ID will be created
	ID string `json:"id"`
	// Org is a unique organisation name
	Org string `json:"org"`
}

// NewUserRequest is a request to create a new user
type NewUserRequest struct {
	// Name is the user name
	Name string `json:"email"`
	// Type is the type of user to create (e.g. agent or admin)
	Type string `json:"type"`
	// Password is the password to set for the created user
	Password string `json:"password"`
}

func (r NewUserRequest) Check() error {
	if err := utils.CheckUserName(r.Name); err != nil {
		return trace.Wrap(err)
	}

	if r.Type != storage.AgentUser && r.Password == "" {
		return trace.BadParameter("missing parameter Password")
	}
	return nil
}

// NewAPIKeyRequest is a request to create a new api key
type NewAPIKeyRequest struct {
	// Expires is the key expiration time
	Expires time.Time `json:"expires"`
	// UserEmail is the username to create a new key for
	UserEmail string `json:"user_email"`
	// Token is an optional predefined API key value, will be
	// generated if not provided
	Token string `json:"token"`
	// Upsert controls whether existing key should be updated
	Upsert bool `json:"upsert"`
}

// NewInstallTokenRequest is a request to generate a one-time install token
type NewInstallTokenRequest struct {
	// AccountID links this token to the specified account
	AccountID string `json:"account"`
	// Application references an optional application package to associate
	// with the install token
	Application string `json:"app"`
	// UserType defines the type of user to associate with this token
	UserType string `json:"type"`
	// UserEmail defines the existing user to associate with this install token.
	// If unspecified, a new user will be created
	UserEmail string `json:"email"`
	// Token is an optional predefined token value, if not passed,
	// will be generated
	Token string `json:"token"`
}

func (r NewInstallTokenRequest) Check() error {
	if r.AccountID == "" {
		return trace.BadParameter("missing parameter AccountID")
	}
	if r.UserType == "" {
		return trace.BadParameter("missing parameter UserType")
	}
	return nil
}

// AccountKey used to identify account
type AccountKey struct {
	// AccountID is id of the account
	AccountID string `json:"account_id"`
}

// String represents debug-friendly representation of AccountKey
func (k AccountKey) String() string {
	return fmt.Sprintf(
		"account(account_id=%v)", k.AccountID)
}

// UserInviteRequest is a request to create a user invite
type UserInviteRequest struct {
	// Name is the new user name
	Name string `json:"name"`
	// Roles is the new user roles
	Roles []string `json:"roles"`
	// TTL is this request TTL
	TTL time.Duration `json:"ttl"`
}

// UserResetRequest is a request to reset user credentials
type UserResetRequest struct {
	// Name is a user name
	Name string `json:"name"`
	// TTL is this request TTL
	TTL time.Duration `json:"ttl"`
}

// NewSiteRequest is a request to create a new site entry
type NewSiteRequest struct {
	// AppPackage is application package, e.g. `gravitaional.io/mattermost:1.2.1`
	AppPackage string `json:"app_package"`
	// AccountID  is the id of the account
	AccountID string `json:"account_id"`
	// Email is the email address of a user who created the site
	Email string `json:"email"`
	// Provider, e.g. 'aws_terraform' or 'onprem'
	Provider string `json:"provider"`
	// DomainName is a name that uniquely identifies the installation
	DomainName string `json:"domain_name"`
	// License is the license that will be installed on site
	License string `json:"license"`
	// Labels is a custom key/value metadata to attach to a new site
	Labels map[string]string `json:"labels"`
	// Resources is a string with additional K8s resources injected at a runtime
	Resources []byte `json:"resources"`
	// Location describes the location where a new site is about to be deployed,
	// for example AWS region name
	Location string `json:"location"`
	// InstallToken is install token for site to create for agents
	InstallToken string `json:"install_token"`
	// ServiceUser specifies the user to use for planet container services
	// and unprivileged kubernetes resources
	ServiceUser storage.OSUser `json:"service_user"`
	// CloudConfig describes additional cloud configuration
	CloudConfig storage.CloudConfig `json:"cloud_config"`
	// DNSOverrides specifies DNS host/zone overrides for the cluster
	DNSOverrides storage.DNSOverrides `json:"dns_overrides"`
	// DNSConfig specifies the cluster local DNS server configuration
	DNSConfig storage.DNSConfig `json:"dns_config"`
	// Docker specifies the cluster Docker configuration
	Docker storage.DockerConfig `json:"docker"`
}

// SiteKey is a key used to identify site
type SiteKey struct {
	// AccountID is a unique id of the account this site belongs to
	AccountID string `json:"account_id"`
	// SiteDomain is a unique domain name of this site
	SiteDomain string `json:"site_domain"`
}

// Check checks parameters
func (k *SiteKey) Check() error {
	if k.AccountID == "" {
		return trace.BadParameter("missing parameter AccountID")
	}
	if k.SiteDomain == "" {
		return trace.BadParameter("missing parameter SiteDomain")
	}
	return nil
}

// String returns log and debug friendly representation of SiteKey
func (k SiteKey) String() string {
	return fmt.Sprintf(
		"site(account_id=%v, site_domain=%v)", k.AccountID, k.SiteDomain)
}

// AgentCreds represent install agent username and password used
// to identify install agents for the site
type AgentCreds struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Account is a collection of sites and represents some company
type Account storage.Account

// Site represents portal site entry - a collection of servers used
// to support one particular application
type Site struct {
	// Created records site creation time
	Created time.Time `json:"created"`
	// CreatedBy is the email of a user who created the site
	CreatedBy string `json:"created_by"`
	// Domain is a site specific unique domain name (e.g. site.example.com)
	Domain string `json:"domain"`
	// AccountID is the id of the account this site belongs to
	AccountID string `json:"account_id"`
	// State is a runtime site of this installation
	State string `json:"state"`
	// Reason is the code describing the state the site is currently in
	Reason storage.Reason `json:"reason"`
	// App provides application information
	App Application `json:"app"`
	// Local specifies whether this site is local to the running
	// process (opscenter or site)
	Local bool `json:"local"`
	// Provider defines the provider this site is created with
	// Provider is either a cloud provider - i.e. AWS or Azure, a VM provider
	// such as bare-metal
	Provider string `json:"provider"`
	// Resources is additional Kubernetes resources injected at site creation
	Resources []byte `json:"resources"`
	// License is the license currently installed on this site
	License *License `json:"license,omitempty"`
	// Labels is a custom key/value metadata attached to the site
	Labels map[string]string `json:"labels"`
	// FinalInstallStepComplete indicates whether the site has completed its final installation step
	FinalInstallStepComplete bool `json:"final_install_step_complete"`
	// Location is a location where the site is deployed, for example AWS region name
	Location string `json:"location"`
	// UpdateInterval is how often the site checks for and downloads newer versions of the
	// installed application
	UpdateInterval time.Duration `json:"update_interval"`
	// NextUpdateCheck is the timestamp of the upcoming updates check for the site
	NextUpdateCheck time.Time `json:"next_update_check"`
	// ClusterState contains a list of servers in the running cluster
	ClusterState storage.ClusterState `json:"cluster_state"`
	// ServiceUser specifies the user to use for planet container services
	// and unprivileged kubernetes resources
	ServiceUser storage.OSUser `json:"service_user"`
	// CloudConfig describes additional cloud configuration
	CloudConfig storage.CloudConfig `json:"cloud_config"`
	// DNSOverrides contains DNS overrides for this cluster
	DNSOverrides storage.DNSOverrides `json:"dns_overrides"`
	// DNSConfig specifies the cluster local DNS server configuration
	DNSConfig storage.DNSConfig `json:"dns_config"`
}

// IsOnline returns whether this site is online
func (s *Site) IsOnline() bool {
	switch s.State {
	case SiteStateActive, SiteStateUpdating, SiteStateExpanding, SiteStateShrinking, SiteStateUninstalling, SiteStateDegraded:
		return true
	}
	return false
}

// IsAWS returns true if the cluster is installed using AWS provisioner
func (s *Site) IsAWS() bool {
	return utils.StringInSlice([]string{
		schema.ProvisionerAWSTerraform,
		schema.ProviderAWS,
	}, s.Provider)
}

// Masters returns a list of master nodes from the cluster's state
func (s *Site) Masters() (masters []storage.Server) {
	for _, node := range s.ClusterState.Servers {
		if node.ClusterRole == string(schema.ServiceRoleMaster) {
			masters = append(masters, node)
		}
	}
	return masters
}

// Application holds information about application, such
// as package name and version, manifest and runtime information
type Application struct {
	// Package is application package information
	Package loc.Locator `json:"package"`
	// PackageEnvelope provides complete information about the underlying package
	PackageEnvelope pack.PackageEnvelope `json:"envelope"`
	// Manifest is a site install manifest that specifies it's configuration
	Manifest schema.Manifest `json:"manifest"`
}

// License represents a license installed on site
type License struct {
	// Raw is a raw license string, be it our certificate or JSON-based customer license
	Raw string `json:"raw"`
	// Payload is the parsed license payload
	Payload license.Payload `json:"payload"`
}

// Key is a helper function to return site key from a site
func (s *Site) Key() SiteKey {
	return SiteKey{AccountID: s.AccountID, SiteDomain: s.Domain}
}

// OperationKey constructs an operation key for this site and provided operation ID
func (s *Site) OperationKey(operationID string) SiteOperationKey {
	return SiteOperationKey{AccountID: s.AccountID, SiteDomain: s.Domain, OperationID: operationID}
}

// String is a debug friendly representation of the site
func (s *Site) String() string {
	return fmt.Sprintf("cluster(name=%v)", s.Domain)
}

// ProgressEntry is a log entry indicating operation progress
//
// ProgressEntry state goes through the following transitions:
//
// in_progress ->
//   failed
//   or
//   completed
type ProgressEntry storage.ProgressEntry

// IsCompleted returns whether this progress entry identifies a completed
// (successful or failed) operation
func (r ProgressEntry) IsCompleted() bool {
	return r.Completion == constants.Completed
}

// IsEqual determines if this progress entry equals to other
func (r ProgressEntry) IsEqual(other ProgressEntry) bool {
	return r.Completion == other.Completion && r.Message == other.Message
}

// Validation defines a set of data validation primitives
type Validation interface {
	// ValidateDomainName validates that the chosen domain name is unique
	ValidateDomainName(domainName string) error
	// ValidateServers runs pre-installation checks
	ValidateServers(ValidateServersRequest) error
	// ValidateRemoteAccess verifies that the cluster nodes are accessible remotely
	ValidateRemoteAccess(ValidateRemoteAccessRequest) (*ValidateRemoteAccessResponse, error)
}

// ValidateServersRequest is a request to run pre-installation checks
type ValidateServersRequest struct {
	// AccountID is the site's account ID
	AccountID string `json:"account_id"`
	// SiteDomain is the site domain name
	SiteDomain string `json:"site_domain"`
	// Servers is onprem servers to run checks for
	Servers []storage.Server `json:"servers"`
	// OperationID identifies the operation
	OperationID string `json:"operation_id"`
}

// Check validates this request
func (r ValidateServersRequest) Check() error {
	if r.AccountID == "" {
		return trace.BadParameter("account ID is required")
	}
	if r.SiteDomain == "" {
		return trace.BadParameter("cluster name is required")
	}
	if r.OperationID == "" {
		return trace.BadParameter("operation ID is required")
	}
	return nil
}

// SiteKey returns a site key from this request
func (r ValidateServersRequest) SiteKey() SiteKey {
	return SiteKey{
		AccountID:  r.AccountID,
		SiteDomain: r.SiteDomain,
	}
}

// OperationKey returns the operation key from this request
func (r ValidateServersRequest) OperationKey() SiteOperationKey {
	return SiteOperationKey{
		AccountID:   r.AccountID,
		SiteDomain:  r.SiteDomain,
		OperationID: r.OperationID,
	}
}

// ValidateRemoteAccessRequest describes a request to run a set of commands on
// nodes in the cluster
type ValidateRemoteAccessRequest struct {
	// AccountID is the site's account ID
	AccountID string `json:"account_id"`
	// SiteDomain is the site domain name
	SiteDomain string `json:"site_domain"`
	// NodeLabels specifies an optional set of labels to filter nodes with.
	// If empty, all nodes are used
	NodeLabels map[string]string `json:"labels"`
}

// SiteKey returns a site key from this request
func (r ValidateRemoteAccessRequest) SiteKey() SiteKey {
	return SiteKey{
		AccountID:  r.AccountID,
		SiteDomain: r.SiteDomain,
	}
}

// ValidateRemoteAccessResponse describes a request to run a set of commands on
// nodes in the cluster
type ValidateRemoteAccessResponse struct {
	// Results lists results from nodes
	Results []NodeResponse `json:"results"`
}

// NodeResponse defines the result of executing a remote command on a node
type NodeResponse struct {
	// Name identifies a node
	Name string `json:"name"`
	// Output is the output from the executed command
	Output []byte `json:"output"`
}

// OperationUpdateRequest defines the user-customized subset of the provisioner configuration
type OperationUpdateRequest struct {
	// Profiles updates server profiles (role -> server profile)
	Profiles map[string]storage.ServerProfileRequest `json:"profiles"`
	// Servers sets a list of running user-configured server instances
	Servers []storage.Server `json:"servers"`
	// ValidateServers specifies whether the update should validate the servers
	ValidateServers bool `json:"validate,omitempty"`
}

// SetOperationStateRequest specifies the request to update operation with a given state
type SetOperationStateRequest struct {
	// State defines the new state of the operation
	State string `json:"state"`
	// Progress is an optional progress entry to create
	Progress *ProgressEntry `json:"progress,omitempty"`
}

// LogForwarders defines the interface to manage log forwarders
type LogForwarders interface {
	// GetLogForwarders retrieves the list of active log forwarders
	GetLogForwarders(key SiteKey) ([]storage.LogForwarder, error)
	// CreateLogForwarder creates a new log forwarder
	CreateLogForwarder(key SiteKey, forwarder storage.LogForwarder) error
	// UpsertLogForwarder updates an existing log forwarder
	UpdateLogForwarder(key SiteKey, forwarder storage.LogForwarder) error
	// DeleteLogForwarder deletes a log forwarder
	DeleteLogForwarder(key SiteKey, name string) error
}

// SMTP defines the interface to manage cluster SMTP configuration
type SMTP interface {
	// GetSMTPConfig returns the cluster SMTP configuration
	GetSMTPConfig(SiteKey) (storage.SMTPConfig, error)
	// UpdateSMTPConfig updates the cluster SMTP configuration
	UpdateSMTPConfig(SiteKey, storage.SMTPConfig) error
	// DeleteSMTPConfig deletes the cluster STMP configuration
	DeleteSMTPConfig(SiteKey) error
}

// Monitoring defines the interface to manage monitoring and metrics
type Monitoring interface {
	// GetRetentionPolicies returns a list of retention policies for the site
	GetRetentionPolicies(SiteKey) ([]monitoring.RetentionPolicy, error)
	// UpdateRetentionPolicy updates one of site's retention policies
	UpdateRetentionPolicy(UpdateRetentionPolicyRequest) error
	// GetAlerts returns the list of configured monitoring alerts
	GetAlerts(SiteKey) ([]storage.Alert, error)
	// UpdateAlert updates the specified monitoring alert
	UpdateAlert(SiteKey, storage.Alert) error
	// DeleteAlert deletes the monitoring alert specified with name
	DeleteAlert(key SiteKey, name string) error
	// GetAlertTargets returns the list of configured monitoring alert targets
	GetAlertTargets(SiteKey) ([]storage.AlertTarget, error)
	// UpdateAlertTarget updates cluster's alert target to the specified
	UpdateAlertTarget(SiteKey, storage.AlertTarget) error
	// DeleteAlertTarget deletes the monitoring alert target
	DeleteAlertTarget(SiteKey) error
}

// UpdateRetentionPolicyRequest is a request to update retention policy
type UpdateRetentionPolicyRequest struct {
	// AccountID is the site account ID
	AccountID string `json:"account_id"`
	// SiteDomain is the site domain name
	SiteDomain string `json:"site_domain"`
	// Name is the retention policy to update
	Name string `json:"name"`
	// Duration is the new retention duration
	Duration time.Duration `json:"duration"`
}

// Check makes sure the request is correct
func (r UpdateRetentionPolicyRequest) Check() error {
	if !utils.StringInSlice(AllRetentions, r.Name) {
		return trace.BadParameter("unsupported retention %q, supported are: %v",
			r.Name, AllRetentions)
	}
	if r.Duration <= 0 {
		return trace.BadParameter("duration must be > 0")
	}
	if r.Duration > RetentionLimits[r.Name] {
		return trace.BadParameter("max allowed duration for retention %q is %v, got: %v",
			r.Name, RetentionLimits[r.Name], r.Duration)
	}
	return nil
}

// Endpoints defines cluster and application endpoints management interface
type Endpoints interface {
	// GetApplicationEndpoints returns a list of application endpoints of
	// the specified cluster
	GetApplicationEndpoints(SiteKey) ([]Endpoint, error)
}

// Endpoint respresents an application endpoint
type Endpoint struct {
	// Name is a display name of the endpoint
	Name string `json:"name"`
	// Description is a verbose description of the endpoint
	Description string `json:"description"`
	// Addresses if a list of URLs for the endpoint
	Addresses []string `json:"addresses"`
}

// SeedConfig defines optional configuration to apply on OpsCenter start
type SeedConfig struct {
	// Account defines an optional account to create on OpsCenter start
	Account *storage.Account `yaml:"account,omitempty"`
	// TrustedClusters is a list of externally supplied trusted clusters
	TrustedClusters []storage.TrustedCluster `yaml:"trusted_clusters,omitempty"`
	// SNIHost is the Ops Center SNI host (i.e. public endpoint hostname)
	SNIHost string `yaml:"sni_host,omitempty"`
}

// SNIHosts returns a list of deduplicated Ops Center SNI hosts extracted
// from trusted clusters
func (c SeedConfig) SNIHosts() []string {
	hostnamesMap := make(map[string]struct{})
	if c.SNIHost != "" {
		hostnamesMap[c.SNIHost] = struct{}{}
	}
	for _, tc := range c.TrustedClusters {
		hostnamesMap[tc.GetSNIHost()] = struct{}{}
	}
	var hostnames []string
	for k := range hostnamesMap {
		hostnames = append(hostnames, k)
	}
	return hostnames
}

// String returns a string representation of a seed config
func (c SeedConfig) String() string {
	return fmt.Sprintf("SeedConfig(Account=%s, TrustedClusters=%s, SNIHost=%s)",
		c.Account, c.TrustedClusters, c.SNIHost)
}

// FindServerByInstanceID finds server in the cluster state by instance ID
// if not found, returns NotFound error
func FindServerByInstanceID(cluster *Site, instanceID string) (*storage.Server, error) {
	for _, server := range cluster.ClusterState.Servers {
		if instanceID == server.InstanceID {
			return &server, nil
		}
	}
	return nil, trace.NotFound("no server with instance ID %q found", instanceID)
}

// Identity provides methods for managing users, roles and authentication settings
type Identity interface {
	// UpsertUser creates or updates a user
	UpsertUser(key SiteKey, user teleservices.User) error
	// GetUser returns a user by name
	GetUser(key SiteKey, name string) (teleservices.User, error)
	// GetUsers returns all users
	GetUsers(key SiteKey) ([]teleservices.User, error)
	// DeleteUser deletes a user by name
	DeleteUser(key SiteKey, name string) error
	// UpsertClusterAuthPreference updates cluster authentication preference
	UpsertClusterAuthPreference(key SiteKey, auth teleservices.AuthPreference) error
	// GetClusterAuthPreference returns cluster authentication preference
	GetClusterAuthPreference(key SiteKey) (teleservices.AuthPreference, error)
	// UpsertGithubConnector creates or updates a Github connector
	UpsertGithubConnector(key SiteKey, conn teleservices.GithubConnector) error
	// GetGithubConnector returns a Github connector by its name
	GetGithubConnector(key SiteKey, name string, withSecrets bool) (teleservices.GithubConnector, error)
	// GetGithubConnectors returns all Github connectors
	GetGithubConnectors(key SiteKey, withSecrets bool) ([]teleservices.GithubConnector, error)
	// DeleteGithubConnector deletes a Github connector by name
	DeleteGithubConnector(key SiteKey, name string) error
}
