// Copyright 2022 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"fmt"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"kpt.dev/configsync/e2e/nomostest"
	"kpt.dev/configsync/e2e/nomostest/gitproviders"
	"kpt.dev/configsync/e2e/nomostest/ntopts"
	v1 "kpt.dev/configsync/pkg/api/configmanagement/v1"
	"kpt.dev/configsync/pkg/api/configsync"
	"kpt.dev/configsync/pkg/api/configsync/v1beta1"
	"kpt.dev/configsync/pkg/importer/analyzer/validation/nonhierarchical"
	"kpt.dev/configsync/pkg/testing/fake"
)

const (
	privateARHelmRegistry     = "oci://us-docker.pkg.dev/stolos-dev/configsync-ci-ar-helm"
	privateHelmChartVersion   = "1.13.3"
	privateHelmChart          = "coredns"
	privateNSHelmChart        = "ns-chart"
	privateNSHelmChartVersion = "0.1.0"
	publicHelmRepo            = "https://kubernetes.github.io/ingress-nginx"
	publicHelmChart           = "ingress-nginx"
	publicHelmChartVersion    = "4.0.5"
)

// TestPublicHelm can run on both Kind and GKE clusters.
// It tests Config Sync can pull from public Helm repo without any authentication.
func TestPublicHelm(t *testing.T) {
	publicHelmRepo := "https://kubernetes.github.io/ingress-nginx"
	nt := nomostest.New(t, ntopts.SkipMonoRepo, ntopts.Unstructured)
	origRepoURL := nt.GitProvider.SyncURL(nt.RootRepos[configsync.RootSyncName].RemoteRepoName)

	rs := fake.RootSyncObjectV1Beta1(configsync.RootSyncName)
	nt.T.Log("Update RootSync to sync from a public Helm Chart")
	nt.MustMergePatch(rs, fmt.Sprintf(`{"spec": {"sourceType": "%s", "helm": {"repo": "%s", "chart": "ingress-nginx", "auth": "none", "version": "4.0.5", "releaseName": "my-ingress-nginx", "namespace": "ingress-nginx"}, "git": null}}`,
		v1beta1.HelmSource, publicHelmRepo))
	nt.T.Cleanup(func() {
		// Change the rs back so that it works in the shared test environment.
		nt.MustMergePatch(rs, fmt.Sprintf(`{"spec": {"sourceType": "%s", "helm": null, "git": {"dir": "acme", "branch": "main", "repo": "%s", "auth": "ssh","gcpServiceAccountEmail": "", "secretRef": {"name": "git-creds"}}}}`,
			v1beta1.GitSource, origRepoURL))
	})
	nt.WaitForRepoSyncs(nomostest.WithRootSha1Func(helmChartVersion("ingress-nginx:4.0.5")),
		nomostest.WithSyncDirectoryMap(map[types.NamespacedName]string{nomostest.DefaultRootRepoNamespacedName: "ingress-nginx"}))
	if err := nt.Validate("my-ingress-nginx-controller", "ingress-nginx", &appsv1.Deployment{}); err != nil {
		nt.T.Error(err)
	}
}

// TestHelmNamespaceRepo verifies RepoSync does not sync the helm chart with cluster-scoped resources. It also verifies that RepoSync can successfully
// sync the namespace scoped resources, and assign the RepoSync namespace to these resources.
// This test will work only with following pre-requisites:
// Google service account `e2e-test-ar-reader@stolos-dev.iam.gserviceaccount.com` is created with `roles/artifactregistry.reader` for accessing images in Artifact Registry.
// A JSON key file is generated for this service account and stored in Secret Manager
func TestHelmNamespaceRepo(t *testing.T) {
	repoSyncNN := nomostest.RepoSyncNN(testNs, "rs-test")
	nt := nomostest.New(t, ntopts.SkipMonoRepo, ntopts.RequireGKE(t),
		ntopts.NamespaceRepo(repoSyncNN.Namespace, repoSyncNN.Name))
	nt.T.Log("Update RepoSync to sync from a public Helm Chart")
	rs := nomostest.RepoSyncObjectV1Beta1FromNonRootRepo(nt, repoSyncNN)
	rs.Spec.SourceType = string(v1beta1.HelmSource)
	rs.Spec.Helm = &v1beta1.HelmRepoSync{HelmBase: v1beta1.HelmBase{
		Repo:    publicHelmRepo,
		Chart:   publicHelmChart,
		Auth:    configsync.AuthNone,
		Version: publicHelmChartVersion,
	}}
	nt.RootRepos[configsync.RootSyncName].Add(nomostest.StructuredNSPath(repoSyncNN.Namespace, repoSyncNN.Name), rs)
	nt.RootRepos[configsync.RootSyncName].CommitAndPush("Update RepoSync to sync from a public Helm Chart with cluster-scoped type")
	// Change the RepoSync to sync from the original git source to make test works in the shared test environment.
	nt.T.Cleanup(func() {
		rs.Spec.SourceType = string(v1beta1.GitSource)
		nt.RootRepos[configsync.RootSyncName].Add(nomostest.StructuredNSPath(repoSyncNN.Namespace, repoSyncNN.Name), rs)
		nt.RootRepos[configsync.RootSyncName].CommitAndPush("Update RepoSync to sync from the original git repository")
	})
	nt.WaitForRepoSyncSourceError(repoSyncNN.Namespace, repoSyncNN.Name, nonhierarchical.BadScopeErrCode, "must be Namespace-scoped type")
	nt.T.Log("Fetch password from Secret Manager")
	key, err := gitproviders.FetchCloudSecret("config-sync-ci-ar-key")
	if err != nil {
		nt.T.Fatal(err)
	}
	nt.T.Log("Create secret for authentication")
	_, err = nt.Kubectl("create", "secret", "generic", "foo", fmt.Sprintf("--namespace=%s", repoSyncNN.Namespace), "--from-literal=username=_json_key", fmt.Sprintf("--from-literal=password=%s", key))
	if err != nil {
		nt.T.Fatalf("failed to create secret, err: %v", err)
	}
	nt.T.Log("Update RepoSync to sync from a private Artifact Registry")
	rs.Spec.Helm = &v1beta1.HelmRepoSync{HelmBase: v1beta1.HelmBase{
		Repo:        privateARHelmRegistry,
		Chart:       privateNSHelmChart,
		Auth:        configsync.AuthToken,
		Version:     privateNSHelmChartVersion,
		ReleaseName: "test",
		SecretRef:   v1beta1.SecretReference{Name: "foo"},
	}}
	nt.RootRepos[configsync.RootSyncName].Add(nomostest.StructuredNSPath(repoSyncNN.Namespace, repoSyncNN.Name), rs)
	nt.RootRepos[configsync.RootSyncName].CommitAndPush("Update RepoSync to sync from a private Helm Chart without cluster scoped resources")
	nt.WaitForRepoSyncs(nomostest.WithRepoSha1Func(helmChartVersion(privateNSHelmChart+":"+privateNSHelmChartVersion)), nomostest.WithSyncDirectoryMap(map[types.NamespacedName]string{repoSyncNN: privateNSHelmChart}))
	if err := nt.Validate(rs.Spec.Helm.ReleaseName+"-"+privateNSHelmChart, testNs, &appsv1.Deployment{}); err != nil {
		nt.T.Error(err)
	}
}

// TestHelmARFleetWISameProject tests the `gcpserviceaccount` auth type with Fleet Workload Identity (in-project).
//
//	The test will run on a GKE cluster only with following pre-requisites
//
// 1. Workload Identity is enabled.
// 2. The Google service account `e2e-test-ar-reader@stolos-dev.iam.gserviceaccount.com` is created with `roles/artifactregistry.reader` for access image in Artifact Registry.
// 3. An IAM policy binding is created between the Google service account and the Kubernetes service accounts with the `roles/iam.workloadIdentityUser` role.
//
//	gcloud iam service-accounts add-iam-policy-binding --project=stolos-dev \
//	   --role roles/iam.workloadIdentityUser \
//	   --member "serviceAccount:stolos-dev.svc.id.goog[config-management-system/root-reconciler]" \
//	   e2e-test-ar-reader@stolos-dev.iam.gserviceaccount.com
//
// 4. The following environment variables are set: GCP_PROJECT, GCP_CLUSTER, GCP_REGION|GCP_ZONE.
func TestHelmARFleetWISameProject(t *testing.T) {
	testWorkloadIdentity(t, workloadIdentityTestSpec{
		fleetWITest:   true,
		crossProject:  false,
		sourceRepo:    privateARHelmRegistry,
		sourceVersion: privateHelmChartVersion,
		sourceChart:   privateHelmChart,
		sourceType:    v1beta1.HelmSource,
		gsaEmail:      gsaARReaderEmail,
		rootCommitFn:  helmChartVersion(privateHelmChart + ":" + privateHelmChartVersion),
	})
}

// TestHelmARFleetWIDifferentProject tests the `gcpserviceaccount` auth type with Fleet Workload Identity (cross-project).
//
//	The test will run on a GKE cluster only with following pre-requisites
//
// 1. Workload Identity is enabled.
// 2. The Google service account `e2e-test-ar-reader@stolos-dev.iam.gserviceaccount.com` is created with `roles/artifactregistry.reader` for access image in Artifact Registry.
// 3. An IAM policy binding is created between the Google service account and the Kubernetes service accounts with the `roles/iam.workloadIdentityUser` role.
//
//	gcloud iam service-accounts add-iam-policy-binding --project=stolos-dev \
//	   --role roles/iam.workloadIdentityUser \
//	   --member="serviceAccount:cs-dev-hub.svc.id.goog[config-management-system/root-reconciler]" \
//	   e2e-test-ar-reader@stolos-dev.iam.gserviceaccount.com
//
// 4. The cross-project fleet host project 'cs-dev-hub' is created.
// 5. The following environment variables are set: GCP_PROJECT, GCP_CLUSTER, GCP_REGION|GCP_ZONE.
func TestHelmARFleetWIDifferentProject(t *testing.T) {
	testWorkloadIdentity(t, workloadIdentityTestSpec{
		fleetWITest:   true,
		crossProject:  true,
		sourceRepo:    privateARHelmRegistry,
		sourceVersion: privateHelmChartVersion,
		sourceChart:   privateHelmChart,
		sourceType:    v1beta1.HelmSource,
		gsaEmail:      gsaARReaderEmail,
		rootCommitFn:  helmChartVersion(privateHelmChart + ":" + privateHelmChartVersion),
	})
}

// TestHelmARGKEWorkloadIdentity tests the `gcpserviceaccount` auth type with GKE Workload Identity.
//
//	The test will run on a GKE cluster only with following pre-requisites
//
// 1. Workload Identity is enabled.
// 2. The Google service account `e2e-test-ar-reader@stolos-dev.iam.gserviceaccount.com` is created with `roles/artifactregistry.reader` for access image in Artifact Registry.
// 3. An IAM policy binding is created between the Google service account and the Kubernetes service accounts with the `roles/iam.workloadIdentityUser` role.
//
//	gcloud iam service-accounts add-iam-policy-binding --project=stolos-dev \
//	   --role roles/iam.workloadIdentityUser \
//	   --member "serviceAccount:stolos-dev.svc.id.goog[config-management-system/root-reconciler]" \
//	   e2e-test-ar-reader@stolos-dev.iam.gserviceaccount.com
//
// 4. The following environment variables are set: GCP_PROJECT, GCP_CLUSTER, GCP_REGION|GCP_ZONE.
func TestHelmARGKEWorkloadIdentity(t *testing.T) {
	testWorkloadIdentity(t, workloadIdentityTestSpec{
		fleetWITest:   false,
		crossProject:  false,
		sourceRepo:    privateARHelmRegistry,
		sourceVersion: privateHelmChartVersion,
		sourceChart:   privateHelmChart,
		sourceType:    v1beta1.HelmSource,
		gsaEmail:      gsaARReaderEmail,
		rootCommitFn:  helmChartVersion(privateHelmChart + ":" + privateHelmChartVersion),
	})
}

// TestHelmGCENode tests the `gcenode` auth type for the Helm repository.
// The test will run on a GKE cluster only with following pre-requisites:
// 1. Workload Identity is NOT enabled
// 2. The Compute Engine default service account `PROJECT_ID-compute@developer.gserviceaccount.com` needs to have the following role:
//   - `roles/artifactregistry.reader` for access image in Artifact Registry.
func TestHelmGCENode(t *testing.T) {
	nt := nomostest.New(t, ntopts.SkipMonoRepo, ntopts.Unstructured,
		ntopts.RequireGKE(t), ntopts.GCENodeTest)

	origRepoURL := nt.GitProvider.SyncURL(nt.RootRepos[configsync.RootSyncName].RemoteRepoName)

	rs := fake.RootSyncObjectV1Beta1(configsync.RootSyncName)
	nt.T.Log("Update RootSync to sync from a private Artifact Registry")
	nt.MustMergePatch(rs, fmt.Sprintf(`{"spec": {"sourceType": "%s", "helm": {"repo": "%s", "chart": "%s", "auth": "gcenode", "version": "%s", "releaseName": "my-coredns", "namespace": "coredns"}, "git": null}}`,
		v1beta1.HelmSource, privateARHelmRegistry, privateHelmChart, privateHelmChartVersion))
	nt.T.Cleanup(func() {
		// Change the rs back so that it works in the shared test environment.
		nt.MustMergePatch(rs, fmt.Sprintf(`{"spec": {"sourceType": "%s", "helm": null, "git": {"dir": "acme", "branch": "main", "repo": "%s", "auth": "ssh","gcpServiceAccountEmail": "", "secretRef": {"name": "git-creds"}}}}`,
			v1beta1.GitSource, origRepoURL))
	})

	nt.WaitForRepoSyncs(nomostest.WithRootSha1Func(helmChartVersion(privateHelmChart+":"+privateHelmChartVersion)),
		nomostest.WithSyncDirectoryMap(map[types.NamespacedName]string{nomostest.DefaultRootRepoNamespacedName: privateHelmChart}))
	if err := nt.Validate("my-coredns-coredns", "coredns", &appsv1.Deployment{},
		containerImagePullPolicy("IfNotPresent")); err != nil {
		nt.T.Error(err)
	}
}

// TestHelmARTokenAuth verifies Config Sync can pull Helm chart from private Artifact Registry with Token auth type.
// This test will work only with following pre-requisites:
// Google service account `e2e-test-ar-reader@stolos-dev.iam.gserviceaccount.com` is created with `roles/artifactregistry.reader` for accessing images in Artifact Registry.
// A JSON key file is generated for this service account and stored in Secret Manager
func TestHelmARTokenAuth(t *testing.T) {
	nt := nomostest.New(t,
		ntopts.SkipMonoRepo,
		ntopts.Unstructured,
		ntopts.RequireGKE(t),
	)
	origRepoURL := nt.GitProvider.SyncURL(nt.RootRepos[configsync.RootSyncName].RemoteRepoName)

	rs := fake.RootSyncObjectV1Beta1(configsync.RootSyncName)
	nt.T.Log("Fetch password from Secret Manager")
	key, err := gitproviders.FetchCloudSecret("config-sync-ci-ar-key")
	if err != nil {
		nt.T.Fatal(err)
	}
	nt.T.Log("Create secret for authentication")
	_, err = nt.Kubectl("create", "secret", "generic", "foo", fmt.Sprintf("--namespace=%s", v1.NSConfigManagementSystem), "--from-literal=username=_json_key", fmt.Sprintf("--from-literal=password=%s", key))
	if err != nil {
		nt.T.Fatalf("failed to create secret, err: %v", err)
	}
	nt.T.Cleanup(func() {
		nt.MustKubectl("delete", "secret", "foo", "-n", v1.NSConfigManagementSystem, "--ignore-not-found")
	})
	nt.T.Log("Update RootSync to sync from a private Artifact Registry")
	nt.MustMergePatch(rs, fmt.Sprintf(`{"spec": {"sourceType": "%s", "git": null, "helm": {"repo": "%s", "chart": "%s", "auth": "token", "version": "%s", "releaseName": "my-coredns", "namespace": "coredns",
                                                                                                "values": {"image.pullPolicy": "Always", "resources.requests.memory": "250Mi", "resources.requests.cpu": "150m", "resources.limits.memory": "300Mi", "resources.limits.cpu": 1},
                                                                                                "secretRef": {"name" : "foo"}}}}`,
		v1beta1.HelmSource, privateARHelmRegistry, privateHelmChart, privateHelmChartVersion))
	nt.T.Cleanup(func() {
		// Change the rs back so that it works in the shared test environment.
		nt.MustMergePatch(rs, fmt.Sprintf(`{"spec": {"sourceType": "%s", "helm": null, "git": {"dir": "acme", "branch": "main", "repo": "%s", "auth": "ssh","gcpServiceAccountEmail": "", "secretRef": {"name": "git-creds"}}}}`,
			v1beta1.GitSource, origRepoURL))
	})
	nt.WaitForRepoSyncs(nomostest.WithRootSha1Func(helmChartVersion(privateHelmChart+":"+privateHelmChartVersion)),
		nomostest.WithSyncDirectoryMap(map[types.NamespacedName]string{nomostest.DefaultRootRepoNamespacedName: privateHelmChart}))
	if err := nt.Validate("my-coredns-coredns", "coredns", &appsv1.Deployment{},
		containerImagePullPolicy("Always"), nomostest.HasCorrectResourceRequestsLimits("coredns", resource.MustParse("150m"), resource.MustParse("1"), resource.MustParse("250Mi"), resource.MustParse("300Mi"))); err != nil {
		nt.T.Error(err)
	}
}

func helmChartVersion(chartVersion string) nomostest.Sha1Func {
	return func(*nomostest.NT, types.NamespacedName) (string, error) {
		return chartVersion, nil
	}
}
