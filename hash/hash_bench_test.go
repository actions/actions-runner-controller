package hash

import (
	"fmt"
	"testing"
)

type benchmarkNestedSpec struct {
	Image       string
	Args        []string
	Env         map[string]string
	Annotations map[string]string
}

type benchmarkTemplate struct {
	Name        string
	Namespace   string
	Labels      map[string]string
	Annotations map[string]string
	Replicas    int
	Specs       []benchmarkNestedSpec
}

func newBenchmarkTemplate(size int) benchmarkTemplate {
	labels := make(map[string]string, size)
	annotations := make(map[string]string, size)
	specs := make([]benchmarkNestedSpec, 0, size)

	for i := range size {
		k := fmt.Sprintf("k-%d", i)
		v := fmt.Sprintf("v-%d", i)
		labels[k] = v
		annotations[k] = v

		env := map[string]string{
			"FOO": v,
			"BAR": v + "-bar",
		}

		specs = append(specs, benchmarkNestedSpec{
			Image:       "ghcr.io/actions/runner:latest",
			Args:        []string{"--once", "--ephemeral", v},
			Env:         env,
			Annotations: map[string]string{"a": v, "b": v + "-b"},
		})
	}

	return benchmarkTemplate{
		Name:        "bench-template",
		Namespace:   "default",
		Labels:      labels,
		Annotations: annotations,
		Replicas:    size,
		Specs:       specs,
	}
}

func BenchmarkComputeTemplateHash(b *testing.B) {
	cases := []struct {
		name string
		size int
	}{
		{name: "small", size: 3},
		{name: "medium", size: 15},
		{name: "large", size: 60},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			tpl := newBenchmarkTemplate(tc.size)
			b.ReportAllocs()
			for b.Loop() {
				_ = ComputeTemplateHash(&tpl)
			}
		})
	}
}

func BenchmarkFNVHashStringObjects(b *testing.B) {
	cases := []struct {
		name string
		size int
	}{
		{name: "small", size: 3},
		{name: "medium", size: 15},
		{name: "large", size: 60},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			tpl := newBenchmarkTemplate(tc.size)
			b.ReportAllocs()
			for b.Loop() {
				_ = FNVHashStringObjects(tpl.Labels, tpl.Annotations, tpl.Specs)
			}
		})
	}
}

func BenchmarkFNVHashString(b *testing.B) {
	const value = "namespace@runner-group@https://github.com/org/repo"
	b.ReportAllocs()
	for b.Loop() {
		_ = FNVHashString(value)
	}
}
