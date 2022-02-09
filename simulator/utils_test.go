package simulator

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestVisibleRunnerGroups(t *testing.T) {
	v := NewRunnerGroups()

	orgDefault := NewRunnerGroupFromProperties("", "myorg1", "")
	orgCustom := NewRunnerGroupFromProperties("", "myorg1", "myorg1group1")
	enterpriseDefault := NewRunnerGroupFromProperties("myenterprise1", "", "")
	enterpriseCustom := NewRunnerGroupFromProperties("myenterprise1", "", "myenterprise1group1")

	if v.Includes(orgCustom) {
		t.Fatalf("orgCustom should not be included yet")
	}

	v.Add(orgCustom)

	if !v.Includes(orgCustom) {
		t.Fatalf("orgCustom should be included")
	}

	if v.Includes(enterpriseCustom) {
		t.Fatalf("enterpriseCustom should not be included")
	}

	v.Add(orgDefault)

	if !v.Includes(orgDefault) {
		t.Fatalf("orgDefault should be included")
	}

	if v.Includes(enterpriseDefault) {
		t.Fatalf("enterpriseDefault should not be included")
	}

	v.Add(enterpriseCustom)
	v.Add(enterpriseDefault)

	var allRunnerGroups []RunnerGroup

	err := v.Traverse(func(rg RunnerGroup) (bool, error) {
		allRunnerGroups = append(allRunnerGroups, rg)

		return false, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if d := cmp.Diff(orgDefault, allRunnerGroups[0]); d != "" {
		t.Errorf("unexpected diff: want (-) got (+): %s", d)
	}

	if d := cmp.Diff(enterpriseDefault, allRunnerGroups[1]); d != "" {
		t.Errorf("unexpected diff: want (-) got (+): %s", d)
	}

	if d := cmp.Diff(orgCustom, allRunnerGroups[2]); d != "" {
		t.Errorf("unexpected diff: want (-) got (+): %s", d)
	}

	if d := cmp.Diff(enterpriseCustom, allRunnerGroups[3]); d != "" {
		t.Errorf("unexpected diff: want (-) got (+): %s", d)
	}

	var first []RunnerGroup

	err = v.Traverse(func(rg RunnerGroup) (bool, error) {
		first = append(first, rg)

		return true, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if d := cmp.Diff(orgDefault, first[0]); d != "" {
		t.Errorf("unexpected diff: want (-) got (+): %s", d)
	}

	if len(first) != 1 {
		t.Errorf("unexpected number of traverse func calls: want 1, got %d", len(first))
	}
}
