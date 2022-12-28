package controllers

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
	"github.com/actions/actions-runner-controller/github"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// The api creds scret annotation is added by the runner controller or the runnerset controller according to runner.spec.githubAPICredentialsFrom.secretRef.name,
	// so that the runner pod controller can share the same GitHub API credentials and the instance of the GitHub API client with the upstream controllers.
	annotationKeyGitHubAPICredsSecret = annotationKeyPrefix + "github-api-creds-secret"
)

type runnerOwnerRef struct {
	// kind is either StatefulSet or Runner, and populated via the owner reference in the runner pod controller or via the reconcilation target's kind in
	// runnerset and runner controllers.
	kind     string
	ns, name string
}

type secretRef struct {
	ns, name string
}

// savedClient is the each cache entry that contains the client for the specific set of credentials,
// like a PAT or a pair of key and cert.
// the `hash` is a part of the savedClient not the key because we are going to keep only the client for the latest creds
// in case the operator updated the k8s secret containing the credentials.
type savedClient struct {
	hash string

	// refs is the map of all the objects that references this client, used for reference counting to gc
	// the client if unneeded.
	refs map[runnerOwnerRef]struct{}

	*github.Client
}

type resourceReader interface {
	Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error
}

type MultiGitHubClient struct {
	mu sync.Mutex

	client resourceReader

	githubClient *github.Client

	// The saved client is freed once all its dependents disappear, or the contents of the secret changed.
	// We track dependents via a golang map embedded within the savedClient struct. Each dependent is checked on their respective Kubernetes finalizer,
	// so that we won't miss any dependent's termination.
	// The change is the secret is determined using the hash of its contents.
	clients map[secretRef]savedClient
}

func NewMultiGitHubClient(client resourceReader, githubClient *github.Client) *MultiGitHubClient {
	return &MultiGitHubClient{
		client:       client,
		githubClient: githubClient,
		clients:      map[secretRef]savedClient{},
	}
}

// Init sets up and return the *github.Client for the object.
// In case the object (like RunnerDeployment) does not request a custom client, it returns the default client.
func (c *MultiGitHubClient) InitForRunnerPod(ctx context.Context, pod *corev1.Pod) (*github.Client, error) {
	// These 3 default values are used only when the user created the pod directly, not via Runner, RunnerReplicaSet, RunnerDeploment, or RunnerSet resources.
	ref := refFromRunnerPod(pod)
	secretName := pod.Annotations[annotationKeyGitHubAPICredsSecret]

	// kind can be any of Pod, Runner, RunnerReplicaSet, RunnerDeployment, or RunnerSet depending on which custom resource the user directly created.
	return c.initClientWithSecretName(ctx, pod.Namespace, secretName, ref)
}

// Init sets up and return the *github.Client for the object.
// In case the object (like RunnerDeployment) does not request a custom client, it returns the default client.
func (c *MultiGitHubClient) InitForRunner(ctx context.Context, r *v1alpha1.Runner) (*github.Client, error) {
	var secretName string
	if r.Spec.GitHubAPICredentialsFrom != nil {
		secretName = r.Spec.GitHubAPICredentialsFrom.SecretRef.Name
	}

	// These 3 default values are used only when the user created the runner resource directly, not via RunnerReplicaSet, RunnerDeploment, or RunnerSet resources.
	ref := refFromRunner(r)
	if ref.ns != r.Namespace {
		return nil, fmt.Errorf("referencing github api creds secret from owner in another namespace is not supported yet")
	}

	// kind can be any of Runner, RunnerReplicaSet, or RunnerDeployment depending on which custom resource the user directly created.
	return c.initClientWithSecretName(ctx, r.Namespace, secretName, ref)
}

// Init sets up and return the *github.Client for the object.
// In case the object (like RunnerDeployment) does not request a custom client, it returns the default client.
func (c *MultiGitHubClient) InitForRunnerSet(ctx context.Context, rs *v1alpha1.RunnerSet) (*github.Client, error) {
	ref := refFromRunnerSet(rs)

	var secretName string
	if rs.Spec.GitHubAPICredentialsFrom != nil {
		secretName = rs.Spec.GitHubAPICredentialsFrom.SecretRef.Name
	}

	return c.initClientWithSecretName(ctx, rs.Namespace, secretName, ref)
}

// Init sets up and return the *github.Client for the object.
// In case the object (like RunnerDeployment) does not request a custom client, it returns the default client.
func (c *MultiGitHubClient) InitForHRA(ctx context.Context, hra *v1alpha1.HorizontalRunnerAutoscaler) (*github.Client, error) {
	ref := refFromHorizontalRunnerAutoscaler(hra)

	var secretName string
	if hra.Spec.GitHubAPICredentialsFrom != nil {
		secretName = hra.Spec.GitHubAPICredentialsFrom.SecretRef.Name
	}

	return c.initClientWithSecretName(ctx, hra.Namespace, secretName, ref)
}

func (c *MultiGitHubClient) DeinitForRunnerPod(p *corev1.Pod) {
	secretName := p.Annotations[annotationKeyGitHubAPICredsSecret]
	c.derefClient(p.Namespace, secretName, refFromRunnerPod(p))
}

func (c *MultiGitHubClient) DeinitForRunner(r *v1alpha1.Runner) {
	var secretName string
	if r.Spec.GitHubAPICredentialsFrom != nil {
		secretName = r.Spec.GitHubAPICredentialsFrom.SecretRef.Name
	}

	c.derefClient(r.Namespace, secretName, refFromRunner(r))
}

func (c *MultiGitHubClient) DeinitForRunnerSet(rs *v1alpha1.RunnerSet) {
	var secretName string
	if rs.Spec.GitHubAPICredentialsFrom != nil {
		secretName = rs.Spec.GitHubAPICredentialsFrom.SecretRef.Name
	}

	c.derefClient(rs.Namespace, secretName, refFromRunnerSet(rs))
}

func (c *MultiGitHubClient) DeinitForHRA(hra *v1alpha1.HorizontalRunnerAutoscaler) {
	var secretName string
	if hra.Spec.GitHubAPICredentialsFrom != nil {
		secretName = hra.Spec.GitHubAPICredentialsFrom.SecretRef.Name
	}

	c.derefClient(hra.Namespace, secretName, refFromHorizontalRunnerAutoscaler(hra))
}

func (c *MultiGitHubClient) initClientForSecret(secret *corev1.Secret, dependent *runnerOwnerRef) (*savedClient, error) {
	secRef := secretRef{
		ns:   secret.Namespace,
		name: secret.Name,
	}

	cliRef := c.clients[secRef]

	var ks []string

	for k := range secret.Data {
		ks = append(ks, k)
	}

	sort.SliceStable(ks, func(i, j int) bool { return ks[i] < ks[j] })

	hash := sha1.New()
	for _, k := range ks {
		hash.Write(secret.Data[k])
	}
	hashStr := hex.EncodeToString(hash.Sum(nil))

	if cliRef.hash != hashStr {
		delete(c.clients, secRef)

		conf, err := secretDataToGitHubClientConfig(secret.Data)
		if err != nil {
			return nil, err
		}

		// Fallback to the controller-wide setting if EnterpriseURL is not set and the original client is an enterprise client.
		if conf.EnterpriseURL == "" && c.githubClient.IsEnterprise {
			conf.EnterpriseURL = c.githubClient.GithubBaseURL
		}

		cli, err := conf.NewClient()
		if err != nil {
			return nil, err
		}

		cliRef = savedClient{
			hash:   hashStr,
			refs:   map[runnerOwnerRef]struct{}{},
			Client: cli,
		}

		c.clients[secRef] = cliRef
	}

	if dependent != nil {
		c.clients[secRef].refs[*dependent] = struct{}{}
	}

	return &cliRef, nil
}

func (c *MultiGitHubClient) initClientWithSecretName(ctx context.Context, ns, secretName string, runRef *runnerOwnerRef) (*github.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if secretName == "" {
		return c.githubClient, nil
	}

	secRef := secretRef{
		ns:   ns,
		name: secretName,
	}

	if _, ok := c.clients[secRef]; !ok {
		c.clients[secRef] = savedClient{}
	}

	var sec corev1.Secret
	if err := c.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: secretName}, &sec); err != nil {
		return nil, err
	}

	savedClient, err := c.initClientForSecret(&sec, runRef)
	if err != nil {
		return nil, err
	}

	return savedClient.Client, nil
}

func (c *MultiGitHubClient) derefClient(ns, secretName string, dependent *runnerOwnerRef) {
	c.mu.Lock()
	defer c.mu.Unlock()

	secRef := secretRef{
		ns:   ns,
		name: secretName,
	}

	if dependent != nil {
		delete(c.clients[secRef].refs, *dependent)
	}

	cliRef := c.clients[secRef]

	if dependent == nil || len(cliRef.refs) == 0 {
		delete(c.clients, secRef)
	}
}

func secretDataToGitHubClientConfig(data map[string][]byte) (*github.Config, error) {
	var (
		conf github.Config

		err error
	)

	conf.URL = string(data["github_url"])

	conf.UploadURL = string(data["github_upload_url"])

	conf.EnterpriseURL = string(data["github_enterprise_url"])

	conf.RunnerGitHubURL = string(data["github_runner_url"])

	conf.Token = string(data["github_token"])

	appID := string(data["github_app_id"])

	conf.AppID, err = strconv.ParseInt(appID, 10, 64)
	if err != nil {
		return nil, err
	}

	instID := string(data["github_app_installation_id"])

	conf.AppInstallationID, err = strconv.ParseInt(instID, 10, 64)
	if err != nil {
		return nil, err
	}

	conf.AppPrivateKey = string(data["github_app_private_key"])

	return &conf, nil
}

func refFromRunner(r *v1alpha1.Runner) *runnerOwnerRef {
	return &runnerOwnerRef{
		kind: r.Kind,
		ns:   r.Namespace,
		name: r.Name,
	}
}

func refFromRunnerPod(po *corev1.Pod) *runnerOwnerRef {
	return &runnerOwnerRef{
		kind: po.Kind,
		ns:   po.Namespace,
		name: po.Name,
	}
}
func refFromRunnerSet(rs *v1alpha1.RunnerSet) *runnerOwnerRef {
	return &runnerOwnerRef{
		kind: rs.Kind,
		ns:   rs.Namespace,
		name: rs.Name,
	}
}

func refFromHorizontalRunnerAutoscaler(hra *v1alpha1.HorizontalRunnerAutoscaler) *runnerOwnerRef {
	return &runnerOwnerRef{
		kind: hra.Kind,
		ns:   hra.Namespace,
		name: hra.Name,
	}
}
