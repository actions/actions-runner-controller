package simulator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/go-github/v47/github"
)

type RunnerGroupScope int

const (
	Organization RunnerGroupScope = iota
	Enterprise
)

func (s RunnerGroupScope) String() string {
	switch s {
	case Organization:
		return "Organization"
	case Enterprise:
		return "Enterprise"
	default:
		panic(fmt.Sprintf("unimplemented RunnerGroupScope: %v", int(s)))
	}
}

type RunnerGroupKind int

const (
	Default RunnerGroupKind = iota
	Custom
)

func (s RunnerGroupKind) String() string {
	switch s {
	case Default:
		return "Default"
	case Custom:
		return "Custom"
	default:
		panic(fmt.Sprintf("unimplemented RunnerGroupKind: %v", int(s)))
	}
}

func NewRunnerGroupFromGitHub(g *github.RunnerGroup) RunnerGroup {
	var name string
	if !g.GetDefault() {
		name = g.GetName()
	}

	var scope RunnerGroupScope

	if g.GetInherited() {
		scope = Enterprise
	} else {
		scope = Organization
	}

	return newRunnerGroup(scope, name)
}

func NewRunnerGroupFromProperties(enterprise, organization, group string) RunnerGroup {
	var scope RunnerGroupScope

	if enterprise != "" {
		scope = Enterprise
	} else {
		scope = Organization
	}

	return newRunnerGroup(scope, group)
}

// newRunnerGroup creates a new RunnerGroup instance from the provided arguments.
// There's a convention that an empty name implies a default runner group.
func newRunnerGroup(scope RunnerGroupScope, name string) RunnerGroup {
	if name == "" {
		return RunnerGroup{
			Scope: scope,
			Kind:  Default,
			Name:  "",
		}
	}

	return RunnerGroup{
		Scope: scope,
		Kind:  Custom,
		Name:  name,
	}
}

type RunnerGroup struct {
	Scope RunnerGroupScope
	Kind  RunnerGroupKind
	Name  string
}

func (r RunnerGroup) String() string {
	return fmt.Sprintf("RunnerGroup{Scope:%s, Kind:%s, Name:%s}", r.Scope, r.Kind, r.Name)
}

// VisibleRunnerGroups is a set of enterprise and organization runner groups
// that are visible to a GitHub repository.
// GitHub Actions chooses one of such visible group on which the workflow job is scheduled.
// ARC chooses the same group as Actions as the scale target.
type VisibleRunnerGroups struct {
	// sortedGroups is a pointer to a mutable list of RunnerGroups that contains all the runner sortedGroups
	// that are visible to the repository, including organization sortedGroups defined at the organization level,
	// and enterprise sortedGroups that are inherited down to the organization.
	sortedGroups []RunnerGroup
}

func NewVisibleRunnerGroups() *VisibleRunnerGroups {
	return &VisibleRunnerGroups{}
}

func (g *VisibleRunnerGroups) String() string {
	var gs []string
	for _, g := range g.sortedGroups {
		gs = append(gs, g.String())
	}

	return strings.Join(gs, ", ")
}

func (g *VisibleRunnerGroups) IsEmpty() bool {
	return len(g.sortedGroups) == 0
}

func (r *VisibleRunnerGroups) Includes(ref RunnerGroup) bool {
	for _, r := range r.sortedGroups {
		if r.Scope == ref.Scope && r.Kind == ref.Kind && r.Name == ref.Name {
			return true
		}
	}
	return false
}

// Add adds a runner group into VisibleRunnerGroups
// at a certain position in the list so that
// Traverse can return runner groups in order of higher precedence to lower precedence.
func (g *VisibleRunnerGroups) Add(rg RunnerGroup) error {
	n := len(g.sortedGroups)
	i := sort.Search(n, func(i int) bool {
		data := g.sortedGroups[i]

		if rg.Kind > data.Kind {
			return false
		} else if rg.Kind < data.Kind {
			return true
		}

		if rg.Scope > data.Scope {
			return false
		} else if rg.Scope < data.Scope {
			return true
		}

		return false
	})

	g.insert(rg, i)

	return nil
}

func (g *VisibleRunnerGroups) insert(rg RunnerGroup, i int) {
	var result []RunnerGroup

	result = append(result, g.sortedGroups[:i]...)
	result = append(result, rg)
	result = append(result, g.sortedGroups[i:]...)

	g.sortedGroups = result
}

// Traverse traverses all the runner groups visible to a repository
// in order of higher precedence to lower precedence.
func (g *VisibleRunnerGroups) Traverse(f func(RunnerGroup) (bool, error)) error {
	for _, rg := range g.sortedGroups {
		ok, err := f(rg)
		if err != nil {
			return err
		}

		if ok {
			return nil
		}
	}

	return nil
}
