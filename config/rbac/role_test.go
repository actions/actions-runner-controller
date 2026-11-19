/*
Copyright 2026 The actions-runner-controller authors.

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

package rbac_test

import (
	"os"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

func TestManagerRolePermissions(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("role.yaml")
	require.NoError(t, err)

	var role rbacv1.ClusterRole
	require.NoError(t, yaml.UnmarshalStrict(data, &role))

	for _, tc := range []struct {
		apiGroup string
		resource string
	}{
		{apiGroup: "", resource: "secrets"},
		{apiGroup: "", resource: "serviceaccounts"},
		{apiGroup: rbacv1.GroupName, resource: "roles"},
		{apiGroup: rbacv1.GroupName, resource: "rolebindings"},
	} {
		t.Run(tc.resource, func(t *testing.T) {
			t.Parallel()

			var verbs []string
			for _, rule := range role.Rules {
				if slices.Contains(rule.APIGroups, tc.apiGroup) &&
					slices.Contains(rule.Resources, tc.resource) &&
					len(rule.ResourceNames) == 0 {
					verbs = append(verbs, rule.Verbs...)
				}
			}

			assert.Subset(t, verbs, []string{"create", "delete", "get", "list", "patch", "update", "watch"},
				"manager role must allow both initial creation and reconciliation of %s", tc.resource)
		})
	}
}

func TestAggregateRoles(t *testing.T) {
	t.Parallel()

	const (
		viewLabel  = "rbac.authorization.k8s.io/aggregate-to-view"
		editLabel  = "rbac.authorization.k8s.io/aggregate-to-edit"
		adminLabel = "rbac.authorization.k8s.io/aggregate-to-admin"
	)

	for _, tc := range []struct {
		file   string
		labels []string
		absent []string
	}{
		{file: "aggregate_view_role.yaml", labels: []string{viewLabel, editLabel, adminLabel}},
		{file: "aggregate_edit_role.yaml", labels: []string{editLabel, adminLabel}, absent: []string{viewLabel}},
		{file: "aggregate_read_sensitive_role.yaml", labels: []string{editLabel, adminLabel}, absent: []string{viewLabel}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(tc.file)
			require.NoError(t, err)

			var role rbacv1.ClusterRole
			require.NoError(t, yaml.UnmarshalStrict(data, &role))

			for _, label := range tc.labels {
				assert.Equal(t, "true", role.Labels[label])
			}
			for _, label := range tc.absent {
				assert.NotContains(t, role.Labels, label)
			}
			for _, rule := range role.Rules {
				assert.NotContains(t, rule.Resources, "secrets", "aggregate roles must not grant secrets")
			}
		})
	}

	t.Run("view excludes runners", func(t *testing.T) {
		t.Parallel()

		data, err := os.ReadFile("aggregate_view_role.yaml")
		require.NoError(t, err)

		var role rbacv1.ClusterRole
		require.NoError(t, yaml.UnmarshalStrict(data, &role))

		for _, rule := range role.Rules {
			assert.NotContains(t, rule.Resources, "runners", "status.registration.token must not be readable through view")
			assert.NotContains(t, rule.Resources, "runners/status", "status.registration.token must not be readable through view")
			for _, verb := range rule.Verbs {
				assert.Contains(t, []string{"get", "list", "watch"}, verb)
			}
		}
	})
}

func TestAggregateReadSensitiveRole(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("aggregate_read_sensitive_role.yaml")
	require.NoError(t, err)

	var role rbacv1.ClusterRole
	require.NoError(t, yaml.UnmarshalStrict(data, &role))

	require.Len(t, role.Rules, 1)
	assert.Equal(t, []string{"runners", "runners/status"}, role.Rules[0].Resources)
	assert.Equal(t, []string{"get", "list", "watch"}, role.Rules[0].Verbs)
}

func TestAggregateEditCanReadWhatItWrites(t *testing.T) {
	t.Parallel()

	load := func(file string) rbacv1.ClusterRole {
		data, err := os.ReadFile(file)
		require.NoError(t, err)

		var role rbacv1.ClusterRole
		require.NoError(t, yaml.UnmarshalStrict(data, &role))

		return role
	}

	can := func(role rbacv1.ClusterRole, group, resource, verb string) bool {
		for _, rule := range role.Rules {
			if slices.Contains(rule.APIGroups, group) &&
				slices.Contains(rule.Resources, resource) &&
				slices.Contains(rule.Verbs, verb) {
				return true
			}
		}

		return false
	}

	view := load("aggregate_view_role.yaml")
	edit := load("aggregate_edit_role.yaml")
	sensitive := load("aggregate_read_sensitive_role.yaml")

	readVerbs := []string{"get", "list", "watch"}

	for _, rule := range edit.Rules {
		for _, group := range rule.APIGroups {
			for _, resource := range rule.Resources {
				if !slices.ContainsFunc(rule.Verbs, func(v string) bool { return !slices.Contains(readVerbs, v) }) {
					continue
				}

				for _, verb := range readVerbs {
					assert.True(t, can(view, group, resource, verb) || can(sensitive, group, resource, verb),
						"edit/admin must be able to %s %s.%s since they can write it", verb, resource, group)
				}
			}
		}
	}

	for _, rule := range edit.Rules {
		for _, resource := range rule.Resources {
			for _, verb := range rule.Verbs {
				assert.NotContains(t, readVerbs, verb, "%s: the edit role is write-only, reads belong in the view or read-sensitive role", resource)
			}
		}
	}
}
