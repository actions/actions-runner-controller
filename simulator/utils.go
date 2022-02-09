package simulator

import (
	"fmt"
	"sort"

	"github.com/google/go-github/v39/github"
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

type RunnerGroups struct {
	refs []RunnerGroup
}

func (r *RunnerGroups) Includes(ref RunnerGroup) bool {
	for _, r := range r.refs {
		if r.Scope == ref.Scope && r.Kind == ref.Kind && r.Name == ref.Name {
			return true
		}
	}
	return false
}

func (r *RunnerGroups) Add(ref RunnerGroup) {
	r.refs = append(r.refs, ref)
}

func (r *RunnerGroups) IsEmpty() bool {
	return r.Len() == 0
}

func (r *RunnerGroups) Len() int {
	return len(r.refs)
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

	return New(scope, name)
}

func NewRunnerGroupFromProperties(enterprise, organization, group string) RunnerGroup {
	var scope RunnerGroupScope

	if enterprise != "" {
		scope = Enterprise
	} else {
		scope = Organization
	}

	return New(scope, group)
}

func NewRunnerGroups() *VisibleRunnerGroups {
	return &VisibleRunnerGroups{
		groups: &RunnerGroups{},
	}
}

func New(scope RunnerGroupScope, name string) RunnerGroup {
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

// VisibleRunnerGroups is a set of enterprise and organization runner groups
// that are visible to a GitHub repository.
// GitHub Actions chooses one of such visible group on which the workflow job is scheduled.
// ARC chooses the same group as Actions as the scale target.
type VisibleRunnerGroups struct {
	// groups is a pointer to a mutable list of RunnerGroups that contains all the runner groups
	// that are visible to the repository, including organization groups defined at the organization level,
	// and enterprise groups that are inherited down to the organization.
	groups *RunnerGroups
}

func (g VisibleRunnerGroups) IsEmpty() bool {
	return g.groups.IsEmpty()
}

func (g VisibleRunnerGroups) Includes(rg RunnerGroup) bool {
	return g.groups.Includes(rg)
}

func (g VisibleRunnerGroups) Add(rg RunnerGroup) error {
	if g.groups == nil {
		g.groups = &RunnerGroups{}
	}

	n := len(g.groups.refs)
	i := sort.Search(n, func(i int) bool {
		data := g.groups.refs[i]

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

	refs := g.groups.refs

	result := []RunnerGroup{}
	result = append(result, refs[:i]...)
	result = append(result, rg)
	result = append(result, refs[i:]...)

	g.groups.refs = result

	return nil
}

// Traverse traverses all the runner groups visible to a repository
// in order of higher precedence to lower precedence.
func (g VisibleRunnerGroups) Traverse(f func(RunnerGroup) (bool, error)) error {
	for _, rg := range g.groups.refs {
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
