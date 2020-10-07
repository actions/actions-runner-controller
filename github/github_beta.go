package github

// this contains BETA API clients, that are currently not (yet) in go-github
// once these functions have been added there, they can be removed from here
// code was reused from https://github.com/google/go-github

import (
	"context"
	"fmt"
	"net/url"
	"reflect"

	"github.com/google/go-github/v32/github"
	"github.com/google/go-querystring/query"
)

// CreateOrganizationRegistrationToken creates a token that can be used to add a self-hosted runner on an organization.
//
// GitHub API docs: https://developer.github.com/v3/actions/self-hosted-runners/#create-a-registration-token-for-an-organization
func CreateOrganizationRegistrationToken(ctx context.Context, client *Client, owner string) (*github.RegistrationToken, *github.Response, error) {
	u := fmt.Sprintf("orgs/%v/actions/runners/registration-token", owner)

	req, err := client.NewRequest("POST", u, nil)
	if err != nil {
		return nil, nil, err
	}

	registrationToken := new(github.RegistrationToken)
	resp, err := client.Do(ctx, req, registrationToken)
	if err != nil {
		return nil, resp, err
	}

	return registrationToken, resp, nil
}

// ListOrganizationRunners lists all the self-hosted runners for an organization.
//
// GitHub API docs: https://developer.github.com/v3/actions/self-hosted-runners/#list-self-hosted-runners-for-an-organization
func ListOrganizationRunners(ctx context.Context, client *Client, owner string, opts *github.ListOptions) (*github.Runners, *github.Response, error) {
	u := fmt.Sprintf("orgs/%v/actions/runners", owner)
	u, err := addOptions(u, opts)
	if err != nil {
		return nil, nil, err
	}

	req, err := client.NewRequest("GET", u, nil)
	if err != nil {
		return nil, nil, err
	}

	runners := &github.Runners{}
	resp, err := client.Do(ctx, req, &runners)
	if err != nil {
		return nil, resp, err
	}

	return runners, resp, nil
}

// RemoveOrganizationRunner forces the removal of a self-hosted runner in a repository using the runner id.
//
// GitHub API docs: https://developer.github.com/v3/actions/self_hosted_runners/#remove-a-self-hosted-runner
func RemoveOrganizationRunner(ctx context.Context, client *Client, owner string, runnerID int64) (*github.Response, error) {
	u := fmt.Sprintf("orgs/%v/actions/runners/%v", owner, runnerID)

	req, err := client.NewRequest("DELETE", u, nil)
	if err != nil {
		return nil, err
	}

	return client.Do(ctx, req, nil)
}

// addOptions adds the parameters in opt as URL query parameters to s. opt
// must be a struct whose fields may contain "url" tags.
func addOptions(s string, opts interface{}) (string, error) {
	v := reflect.ValueOf(opts)
	if v.Kind() == reflect.Ptr && v.IsNil() {
		return s, nil
	}

	u, err := url.Parse(s)
	if err != nil {
		return s, err
	}

	qs, err := query.Values(opts)
	if err != nil {
		return s, err
	}

	u.RawQuery = qs.Encode()
	return u.String(), nil
}
