package simulator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVisibleRunnerGroupsInsert(t *testing.T) {
	g := NewVisibleRunnerGroups()

	orgDefault := NewRunnerGroupFromProperties("", "myorg1", "")
	orgCustom := NewRunnerGroupFromProperties("", "myorg1", "myorg1group1")
	enterpriseDefault := NewRunnerGroupFromProperties("myenterprise1", "", "")

	g.insert(orgCustom, 0)
	g.insert(orgDefault, 0)
	g.insert(enterpriseDefault, 1)

	var got []RunnerGroup

	err := g.Traverse(func(rg RunnerGroup) (bool, error) {
		got = append(got, rg)
		return false, nil
	})

	require.NoError(t, err)
	require.Equal(t, []RunnerGroup{orgDefault, enterpriseDefault, orgCustom}, got, "Unexpected result")
}

func TestVisibleRunnerGroups(t *testing.T) {
	v := NewVisibleRunnerGroups()

	requireGroups := func(t *testing.T, included, notIncluded []RunnerGroup) {
		t.Helper()

		for _, rg := range included {
			if !v.Includes(rg) {
				t.Errorf("%v must be included", rg)
			}
		}

		for _, rg := range notIncluded {
			if v.Includes(rg) {
				t.Errorf("%v must not be included", rg)
			}
		}

		var got []RunnerGroup

		err := v.Traverse(func(rg RunnerGroup) (bool, error) {
			got = append(got, rg)

			return false, nil
		})

		require.NoError(t, err)
		require.Equal(t, included, got)
	}

	orgDefault := NewRunnerGroupFromProperties("", "myorg1", "")
	orgCustom := NewRunnerGroupFromProperties("", "myorg1", "myorg1group1")
	enterpriseDefault := NewRunnerGroupFromProperties("myenterprise1", "", "")
	enterpriseCustom := NewRunnerGroupFromProperties("myenterprise1", "", "myenterprise1group1")

	requireGroups(t, nil, []RunnerGroup{orgDefault, enterpriseDefault, orgCustom, enterpriseCustom})

	v.Add(orgCustom)

	requireGroups(t, []RunnerGroup{orgCustom}, []RunnerGroup{orgDefault, enterpriseDefault, enterpriseCustom})

	v.Add(orgDefault)

	requireGroups(t, []RunnerGroup{orgDefault, orgCustom}, []RunnerGroup{enterpriseDefault, enterpriseCustom})

	v.Add(enterpriseCustom)

	requireGroups(t, []RunnerGroup{orgDefault, orgCustom, enterpriseCustom}, []RunnerGroup{enterpriseDefault})

	v.Add(enterpriseDefault)

	requireGroups(t, []RunnerGroup{orgDefault, enterpriseDefault, orgCustom, enterpriseCustom}, nil)

	var first []RunnerGroup

	err := v.Traverse(func(rg RunnerGroup) (bool, error) {
		first = append(first, rg)

		return true, nil
	})

	require.NoError(t, err)
	require.Equal(t, []RunnerGroup{orgDefault}, first)
}
