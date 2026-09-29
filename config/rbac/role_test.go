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
