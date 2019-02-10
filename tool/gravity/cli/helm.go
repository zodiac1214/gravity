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
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/gravitational/gravity/lib/catalog"
	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/helm"
	"github.com/gravitational/gravity/lib/loc"
	"github.com/gravitational/gravity/lib/localenv"
	"github.com/gravitational/gravity/lib/pack"
	"github.com/gravitational/gravity/lib/schema"
	helmutils "github.com/gravitational/gravity/lib/utils/helm"

	"github.com/ghodss/yaml"
	"github.com/gravitational/trace"
)

type releaseInstallConfig struct {
	// Image is an application image to install, can be path or locator.
	Image string
	// Name is an optional release name.
	Name string
	// Namespace is a namespace to install release into.
	Namespace string
	// valuesConfig combines values set on the CLI.
	valuesConfig
	// registryConfig is registry configuration.
	registryConfig
}

func (c *releaseInstallConfig) setDefaults(env *localenv.LocalEnvironment) error {
	err := c.valuesConfig.setDefaults(env)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

type releaseUpgradeConfig struct {
	// Release is a name of release to upgrade.
	Release string
	// Image is an application image to upgrade to, can be path or locator.
	Image string
	// valuesConfig combines values set on the CLI.
	valuesConfig
	// registryConfig is registry configuration.
	registryConfig
}

func (c *releaseUpgradeConfig) setDefaults(env *localenv.LocalEnvironment) error {
	err := c.valuesConfig.setDefaults(env)
	if err != nil {
		return trace.Wrap(err)
	}
	return nil
}

type releaseRollbackConfig struct {
	// Release is a name of release to rollback.
	Release string
	// Revision is a version number to rollback to.
	Revision int
}

type releaseUninstallConfig struct {
	// Release is a release name to uninstall.
	Release string
}

type releaseHistoryConfig struct {
	// Release is a release name to display revisions for.
	Release string
}

type valuesConfig struct {
	// Values is a list of values set on the CLI.
	Values []string
	// Files is a list of YAML files with values.
	Files []string
}

func (c *valuesConfig) setDefaults(env *localenv.LocalEnvironment) error {
	if !env.InGravity() {
		// If not running inside a Gravity cluster, do not auto-set registry.
		return nil
	}
	hasVar, err := helmutils.HasVar(defaults.ImageRegistryVar, c.Files, c.Values)
	if err != nil {
		return trace.Wrap(err)
	}
	if hasVar {
		// If image.registry variable was set explicitly, do not touch it.
		return nil
	}
	// Otherwise, set it to the local cluster registry address.
	c.Values = append(c.Values, fmt.Sprintf("%v=%v/", defaults.ImageRegistryVar,
		constants.DockerRegistry))
	return nil
}

func releaseInstall(env *localenv.LocalEnvironment, conf releaseInstallConfig) error {
	err := conf.setDefaults(env)
	if err != nil {
		return trace.Wrap(err)
	}
	locator, err := makeLocator(env, conf.Image)
	if err == nil { // not a tarball, but locator - should download
		env.PrintStep("Downloading application image %v", conf.Image)
		result, err := catalog.Download(catalog.DownloadRequest{
			Application: *locator,
		})
		if err != nil {
			return trace.Wrap(err)
		}
		conf.Image = result.Path
		defer result.Close() // Remove downloaded tarball after install.
	}
	imageEnv, err := localenv.NewImageEnvironment(conf.Image)
	if err != nil {
		return trace.Wrap(err)
	}
	err = appSyncEnv(env, imageEnv, appSyncConfig{
		Image:          conf.Image,
		registryConfig: conf.registryConfig,
	})
	if err != nil {
		return trace.Wrap(err)
	}
	env.PrintStep("Installing application %v:%v",
		imageEnv.Manifest.Metadata.Name,
		imageEnv.Manifest.Metadata.ResourceVersion)
	tmp, err := ioutil.TempDir("", "")
	if err != nil {
		return trace.Wrap(err)
	}
	defer os.RemoveAll(tmp)
	err = pack.Unpack(imageEnv.Packages, imageEnv.Manifest.Locator(), tmp, nil)
	if err != nil {
		return trace.Wrap(err)
	}
	helmClient, err := helm.NewClient(helm.ClientConfig{
		DNSAddress: env.DNS.Addr(),
	})
	if err != nil {
		return trace.Wrap(err)
	}
	defer helmClient.Close()
	release, err := helmClient.Install(helm.InstallParameters{
		Path:      filepath.Join(tmp, "resources"),
		Values:    conf.Files,
		Set:       conf.Values,
		Name:      conf.Name,
		Namespace: conf.Namespace,
	})
	if err != nil {
		return trace.Wrap(err)
	}
	env.PrintStep("Installed release %v", release.Name)
	return nil
}

func releaseList(env *localenv.LocalEnvironment) error {
	helmClient, err := helm.NewClient(helm.ClientConfig{
		DNSAddress: env.DNS.Addr(),
	})
	if err != nil {
		return trace.Wrap(err)
	}
	defer helmClient.Close()
	releases, err := helmClient.List(helm.ListParameters{})
	if err != nil {
		return trace.Wrap(err)
	}
	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 1, '\t', 0)
	fmt.Fprintf(w, "Release\tStatus\tChart\tRevision\tNamespace\tUpdated\n")
	fmt.Fprintf(w, "-------\t------\t-----\t--------\t---------\t-------\n")
	for _, r := range releases {
		fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\t%v\n",
			r.Name,
			r.Status,
			r.Chart,
			r.Revision,
			r.Namespace,
			r.Updated.Format(constants.HumanDateFormatSeconds))
	}
	w.Flush()
	return nil
}

func releaseUpgrade(env *localenv.LocalEnvironment, conf releaseUpgradeConfig) error {
	err := conf.setDefaults(env)
	if err != nil {
		return trace.Wrap(err)
	}
	locator, err := makeLocator(env, conf.Image)
	if err == nil { // not a tarball, but locator - should download
		env.PrintStep("Downloading application image %v", conf.Image)
		result, err := catalog.Download(catalog.DownloadRequest{
			Application: *locator,
		})
		if err != nil {
			return trace.Wrap(err)
		}
		conf.Image = result.Path
		defer result.Close() // Remove downloaded tarball after upgrade.
	}
	helmClient, err := helm.NewClient(helm.ClientConfig{
		DNSAddress: env.DNS.Addr(),
	})
	if err != nil {
		return trace.Wrap(err)
	}
	defer helmClient.Close()
	release, err := helmClient.Get(conf.Release)
	if err != nil {
		return trace.Wrap(err)
	}
	imageEnv, err := localenv.NewImageEnvironment(conf.Image)
	if err != nil {
		return trace.Wrap(err)
	}
	err = appSyncEnv(env, imageEnv, appSyncConfig{
		Image:          conf.Image,
		registryConfig: conf.registryConfig,
	})
	env.PrintStep("Upgrading release %v (%v) to version %v",
		release.Name, release.Chart,
		imageEnv.Manifest.Metadata.ResourceVersion)
	tmp, err := ioutil.TempDir("", "")
	if err != nil {
		return trace.Wrap(err)
	}
	defer os.RemoveAll(tmp)
	err = pack.Unpack(imageEnv.Packages, imageEnv.Manifest.Locator(), tmp, nil)
	if err != nil {
		return trace.Wrap(err)
	}
	release, err = helmClient.Upgrade(helm.UpgradeParameters{
		Release: release.Name,
		Path:    filepath.Join(tmp, "resources"),
		Values:  conf.Files,
		Set:     conf.Values,
	})
	if err != nil {
		return trace.Wrap(err)
	}
	env.PrintStep("Upgraded release %v to version %v", release.Name,
		imageEnv.Manifest.Metadata.ResourceVersion)
	return nil
}

func releaseRollback(env *localenv.LocalEnvironment, conf releaseRollbackConfig) error {
	helmClient, err := helm.NewClient(helm.ClientConfig{
		DNSAddress: env.DNS.Addr(),
	})
	if err != nil {
		return trace.Wrap(err)
	}
	defer helmClient.Close()
	release, err := helmClient.Rollback(helm.RollbackParameters{
		Release:  conf.Release,
		Revision: conf.Revision,
	})
	if err != nil {
		return trace.Wrap(err)
	}
	env.PrintStep("Rolled back release %v to %v", release.Name, release.Chart)
	return nil
}

func releaseUninstall(env *localenv.LocalEnvironment, conf releaseUninstallConfig) error {
	helmClient, err := helm.NewClient(helm.ClientConfig{
		DNSAddress: env.DNS.Addr(),
	})
	if err != nil {
		return trace.Wrap(err)
	}
	defer helmClient.Close()
	release, err := helmClient.Uninstall(conf.Release)
	if err != nil {
		return trace.Wrap(err)
	}
	env.PrintStep("Uninstalled release %v", release.Name)
	return nil
}

func releaseHistory(env *localenv.LocalEnvironment, conf releaseHistoryConfig) error {
	helmClient, err := helm.NewClient(helm.ClientConfig{
		DNSAddress: env.DNS.Addr(),
	})
	if err != nil {
		return trace.Wrap(err)
	}
	defer helmClient.Close()
	releases, err := helmClient.Revisions(conf.Release)
	if err != nil {
		return trace.Wrap(err)
	}
	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 1, '\t', 0)
	fmt.Fprintf(w, "Revision\tChart\tStatus\tUpdated\tDescription\n")
	fmt.Fprintf(w, "--------\t-----\t------\t-------\t-----------\n")
	for _, r := range releases {
		fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\n",
			r.Revision,
			r.Chart,
			r.Status,
			r.Updated.Format(constants.HumanDateFormatSeconds),
			r.Description)
	}
	w.Flush()
	return nil
}

func appSearch(env *localenv.LocalEnvironment, pattern string, remoteOnly, all bool) error {
	result, err := catalog.Search(catalog.SearchRequest{
		Pattern: pattern,
		Local:   !remoteOnly || all,
		Remote:  remoteOnly || all,
	})
	if err != nil {
		return trace.Wrap(err)
	}
	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 1, '\t', 0)
	fmt.Fprintf(w, "Name\tVersion\tDescription\tCreated\n")
	fmt.Fprintf(w, "----\t-------\t-----------\t-------\n")
	for repository, apps := range result.Apps {
		for _, app := range apps {
			if app.Manifest.Kind == schema.KindApplication {
				fmt.Fprintf(w, "%v/%v\t%v\t%v\t%v\n",
					repository,
					app.Package.Name,
					app.Package.Version,
					app.Manifest.Metadata.Description,
					app.PackageEnvelope.Created.Format(constants.HumanDateFormat))
			}
		}
	}
	w.Flush()
	return nil
}

func appRebuildIndex(env *localenv.LocalEnvironment) error {
	env.PrintStep("Rebuilding charts repository index, this might take a while...")
	clusterEnv, err := env.NewClusterEnvironment()
	if err != nil {
		return trace.Wrap(err)
	}
	charts, err := helm.NewRepository(helm.Config{
		Packages: clusterEnv.Packages,
		Backend:  clusterEnv.Backend,
	})
	if err != nil {
		return trace.Wrap(err)
	}
	err = charts.RebuildIndex()
	if err != nil {
		return trace.Wrap(err)
	}
	env.PrintStep("Index rebuild finished")
	return nil
}

func appIndex(env *localenv.LocalEnvironment) error {
	indexFile, err := helm.GenerateIndexFile(env.Apps)
	if err != nil {
		return trace.Wrap(err)
	}
	bytes, err := yaml.Marshal(indexFile)
	if err != nil {
		return trace.Wrap(err)
	}
	env.Println(string(bytes))
	return nil
}

// makeLocator attempts to create a locator from the provided app image reference.
//
// If the image reference has all parts of the locator (repo/name:ver), then
// a locator with all these parts is returned.
//
// If the image reference omits repository (name:ver), then repository part
// in the locator will be set to the local cluster name.
func makeLocator(env *localenv.LocalEnvironment, image string) (*loc.Locator, error) {
	if !strings.Contains(image, ":") {
		return nil, trace.BadParameter("not a locator: %q", image)
	}
	locator, err := loc.ParseLocator(image)
	if err == nil {
		return locator, nil
	}
	parts := strings.Split(image, ":")
	if len(parts) != 2 {
		return nil, trace.BadParameter("expected <name:ver> format: %q", image)
	}
	localCluster, err := env.LocalCluster()
	if err != nil {
		return nil, trace.Wrap(err)
	}
	return loc.NewLocator(localCluster.Domain, parts[0], parts[1])
}
