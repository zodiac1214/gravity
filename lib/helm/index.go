/*
Copyright 2019 Gravitational, Inc.

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

package helm

import (
	"fmt"

	"github.com/gravitational/gravity/lib/app"
	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/schema"

	"k8s.io/helm/pkg/proto/hapi/chart"
	"k8s.io/helm/pkg/repo"

	"github.com/gravitational/trace"
)

func GenerateIndexFile(apps app.Applications) (*repo.IndexFile, error) {
	items, err := apps.ListApps(app.ListAppsRequest{
		Repository: defaults.SystemAccountOrg,
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}
	indexFile := repo.NewIndexFile()
	for _, item := range items {
		switch item.Manifest.Kind {
		case schema.KindBundle, schema.KindApplication, schema.KindCluster:
		default: // Do not include system apps and runtimes
			continue
		}
		indexFile.Add(
			generateChartMetadata(item.Manifest),
			fmt.Sprintf("%v-%v.tar", item.Manifest.Metadata.Name, item.Manifest.Metadata.ResourceVersion),
			chartRepoURL,
			fmt.Sprintf("sha512:%v", item.PackageEnvelope.SHA512))
	}
	indexFile.SortEntries()
	return indexFile, nil
}

// generateChartMetadata generates chart metadata from the provided manifest.
func generateChartMetadata(manifest schema.Manifest) *chart.Metadata {
	return &chart.Metadata{
		Name:        manifest.Metadata.Name,
		Version:     manifest.Metadata.ResourceVersion,
		Description: manifest.Metadata.Description,
		Annotations: map[string]string{
			"gravitational.io/kind": manifest.ImageType(),
			"gravitational.io/logo": manifest.Logo,
		},
	}
}

const chartRepoURL = "http://charts.gravitational.io.s3.us-east-2.amazonaws.com/"
