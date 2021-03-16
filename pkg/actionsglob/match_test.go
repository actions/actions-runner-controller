package actionsglob

import (
	"testing"
)

func TestMatch(t *testing.T) {
	type testcase struct {
		Pattern, Target string
		Want            bool
	}

	run := func(t *testing.T, tc testcase) {
		t.Helper()

		got := Match(tc.Pattern, tc.Target)

		if got != tc.Want {
			t.Errorf("%s against %s: want %v, got %v", tc.Pattern, tc.Target, tc.Want, got)
		}
	}

	t.Run("foo == foo", func(t *testing.T) {
		run(t, testcase{
			Pattern: "foo",
			Target:  "foo",
			Want:    true,
		})
	})

	t.Run("!foo == foo", func(t *testing.T) {
		run(t, testcase{
			Pattern: "!foo",
			Target:  "foo",
			Want:    false,
		})
	})

	t.Run("foo == foo1", func(t *testing.T) {
		run(t, testcase{
			Pattern: "foo",
			Target:  "foo1",
			Want:    false,
		})
	})

	t.Run("!foo == foo1", func(t *testing.T) {
		run(t, testcase{
			Pattern: "!foo",
			Target:  "foo1",
			Want:    true,
		})
	})

	t.Run("*foo == foo", func(t *testing.T) {
		run(t, testcase{
			Pattern: "*foo",
			Target:  "foo",
			Want:    true,
		})
	})

	t.Run("!*foo == foo", func(t *testing.T) {
		run(t, testcase{
			Pattern: "!*foo",
			Target:  "foo",
			Want:    false,
		})
	})

	t.Run("*foo == 1foo", func(t *testing.T) {
		run(t, testcase{
			Pattern: "*foo",
			Target:  "1foo",
			Want:    true,
		})
	})

	t.Run("!*foo == 1foo", func(t *testing.T) {
		run(t, testcase{
			Pattern: "!*foo",
			Target:  "1foo",
			Want:    false,
		})
	})

	t.Run("*foo == foo1", func(t *testing.T) {
		run(t, testcase{
			Pattern: "*foo",
			Target:  "foo1",
			Want:    false,
		})
	})

	t.Run("!*foo == foo1", func(t *testing.T) {
		run(t, testcase{
			Pattern: "!*foo",
			Target:  "foo1",
			Want:    true,
		})
	})

	t.Run("*foo* == foo1", func(t *testing.T) {
		run(t, testcase{
			Pattern: "*foo*",
			Target:  "foo1",
			Want:    true,
		})
	})

	t.Run("!*foo* == foo1", func(t *testing.T) {
		run(t, testcase{
			Pattern: "!*foo*",
			Target:  "foo1",
			Want:    false,
		})
	})

	t.Run("*foo == foobar", func(t *testing.T) {
		run(t, testcase{
			Pattern: "*foo",
			Target:  "foobar",
			Want:    false,
		})
	})

	t.Run("!*foo == foobar", func(t *testing.T) {
		run(t, testcase{
			Pattern: "!*foo",
			Target:  "foobar",
			Want:    true,
		})
	})

	t.Run("*foo* == foobar", func(t *testing.T) {
		run(t, testcase{
			Pattern: "*foo*",
			Target:  "foobar",
			Want:    true,
		})
	})

	t.Run("!*foo* == foobar", func(t *testing.T) {
		run(t, testcase{
			Pattern: "!*foo*",
			Target:  "foobar",
			Want:    false,
		})
	})

	t.Run("foo* == foo", func(t *testing.T) {
		run(t, testcase{
			Pattern: "foo*",
			Target:  "foo",
			Want:    true,
		})
	})

	t.Run("!foo* == foo", func(t *testing.T) {
		run(t, testcase{
			Pattern: "!foo*",
			Target:  "foo",
			Want:    false,
		})
	})

	t.Run("foo* == foobar", func(t *testing.T) {
		run(t, testcase{
			Pattern: "foo*",
			Target:  "foobar",
			Want:    true,
		})
	})

	t.Run("!foo* == foobar", func(t *testing.T) {
		run(t, testcase{
			Pattern: "!foo*",
			Target:  "foobar",
			Want:    false,
		})
	})

	t.Run("foo (* == foo ( 1 / 2 )", func(t *testing.T) {
		run(t, testcase{
			Pattern: "foo (*",
			Target:  "foo ( 1 / 2 )",
			Want:    true,
		})
	})

	t.Run("!foo (* == foo ( 1 / 2 )", func(t *testing.T) {
		run(t, testcase{
			Pattern: "!foo (*",
			Target:  "foo ( 1 / 2 )",
			Want:    false,
		})
	})

	t.Run("actions-*-metrics == actions-workflow-metrics", func(t *testing.T) {
		run(t, testcase{
			Pattern: "actions-*-metrics",
			Target:  "actions-workflow-metrics",
			Want:    true,
		})
	})

	t.Run("!actions-*-metrics == actions-workflow-metrics", func(t *testing.T) {
		run(t, testcase{
			Pattern: "!actions-*-metrics",
			Target:  "actions-workflow-metrics",
			Want:    false,
		})
	})
}
