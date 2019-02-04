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
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"strconv"
	"strings"

	appservice "github.com/gravitational/gravity/lib/app"
	"github.com/gravitational/gravity/lib/archive"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/fsm"
	"github.com/gravitational/gravity/lib/loc"
	"github.com/gravitational/gravity/lib/pack"
	"github.com/gravitational/gravity/lib/schema"
	"github.com/gravitational/gravity/lib/storage"

	"github.com/gravitational/trace"
	"github.com/sirupsen/logrus"
)

// GetOperationPlan returns an up-to-date operation plan
func GetOperationPlan(b storage.Backend) (*storage.OperationPlan, error) {
	op, err := storage.GetLastOperation(b)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	plan, err := b.GetOperationPlan(op.SiteDomain, op.ID)
	if err != nil && !trace.IsNotFound(err) {
		return nil, trace.Wrap(err)
	}

	if plan == nil {
		return nil, trace.NotFound(
			"%q does not have a plan, use 'gravity plan --init' to initialize it", op.Type)
	}

	changelog, err := b.GetOperationPlanChangelog(op.SiteDomain, op.ID)
	if err != nil {
		return nil, trace.Wrap(err)
	}

	plan = fsm.ResolvePlan(*plan, changelog)
	return plan, nil
}

// planetNeedsUpdate returns true if the planet version in the update application is
// greater than in the installed one for the specified node profile
func planetNeedsUpdate(profile string, installed, update appservice.Application) (needsUpdate bool, err error) {
	installedProfile, err := installed.Manifest.NodeProfiles.ByName(profile)
	if err != nil {
		return false, trace.Wrap(err)
	}

	updateProfile, err := update.Manifest.NodeProfiles.ByName(profile)
	if err != nil {
		return false, trace.Wrap(err)
	}

	updateRuntimePackage, err := update.Manifest.RuntimePackage(*updateProfile)
	if err != nil {
		return false, trace.Wrap(err)
	}

	updateVersion, err := updateRuntimePackage.SemVer()
	if err != nil {
		return false, trace.Wrap(err)
	}

	runtimePackage, err := getRuntimePackage(installed.Manifest, *installedProfile, schema.ServiceRoleMaster)
	if err != nil {
		return false, trace.Wrap(err)
	}

	version, err := runtimePackage.SemVer()
	if err != nil {
		return false, trace.Wrap(err)
	}

	logrus.Debugf("Runtime installed: %v, runtime to update to: %v.", runtimePackage, updateRuntimePackage)
	updateNewer := updateVersion.Compare(*version) > 0
	return updateNewer, nil
}

func getRuntimePackage(manifest schema.Manifest, profile schema.NodeProfile, clusterRole schema.ServiceRole) (*loc.Locator, error) {
	runtimePackage, err := manifest.RuntimePackage(profile)
	if err != nil && !trace.IsNotFound(err) {
		return nil, trace.Wrap(err)
	}
	if err == nil {
		return runtimePackage, nil
	}
	// Look for legacy package
	packageName := loc.LegacyPlanetMaster.Name
	if clusterRole == schema.ServiceRoleNode {
		packageName = loc.LegacyPlanetNode.Name
	}
	runtimePackage, err = manifest.Dependencies.ByName(packageName)
	if err != nil {
		logrus.Warnf("Failed to find the legacy runtime package in manifest "+
			"for profile %v and cluster role %v: %v.", profile.Name, clusterRole, err)
		return nil, trace.NotFound("runtime package for profile %v "+
			"(cluster role %v) not found in manifest",
			profile.Name, clusterRole)
	}
	return runtimePackage, nil
}

func getExistingDNSConfig(packages pack.PackageService) (*storage.DNSConfig, error) {
	_, configPackage, err := pack.FindAnyRuntimePackageWithConfig(packages)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	_, rc, err := packages.ReadPackage(*configPackage)
	if err != nil {
		return nil, trace.Wrap(err)
	}
	defer rc.Close()
	var configBytes []byte
	err = archive.TarGlob(tar.NewReader(rc), "", []string{"vars.json"}, func(_ string, r io.Reader) error {
		configBytes, err = ioutil.ReadAll(r)
		if err != nil {
			return trace.Wrap(err)
		}

		return archive.Abort
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	var runtimeConfig runtimeConfig
	if configBytes != nil {
		err = json.Unmarshal(configBytes, &runtimeConfig)
		if err != nil {
			return nil, trace.Wrap(err)
		}
	}
	dnsPort := defaults.DNSPort
	if len(runtimeConfig.DNSPort) != 0 {
		dnsPort, err = strconv.Atoi(runtimeConfig.DNSPort)
		if err != nil {
			return nil, trace.Wrap(err, "expected integer value but got %v", runtimeConfig.DNSPort)
		}
	}
	var dnsAddrs []string
	if runtimeConfig.DNSListenAddr != "" {
		dnsAddrs = append(dnsAddrs, runtimeConfig.DNSListenAddr)
	}
	dnsConfig := &storage.DNSConfig{
		Addrs: dnsAddrs,
		Port:  dnsPort,
	}
	if dnsConfig.IsEmpty() {
		*dnsConfig = storage.LegacyDNSConfig
	}
	logrus.Infof("Detected DNS configuration: %v.", dnsConfig)
	return dnsConfig, nil
}

type runtimeConfig struct {
	// DNSListenAddr specifies the configured DNS listen address
	DNSListenAddr string `json:"PLANET_DNS_LISTEN_ADDR"`
	// DNSPort specifies the configured DNS port
	DNSPort string `json:"PLANET_DNS_PORT"`
}

func formatServers(servers []storage.Server) string {
	var formats []string
	for _, server := range servers {
		formats = append(formats, formatServer(server))
	}
	return strings.Join(formats, ",")
}

func formatServer(server storage.Server) string {
	return fmt.Sprintf("node(addr=%v, hostname=%v, role=%v, cluster_role=%v)",
		server.AdvertiseIP,
		server.Hostname,
		server.Role,
		server.ClusterRole)
}
