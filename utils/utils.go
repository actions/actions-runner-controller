package utils

type RunnerGroups struct {
	DefaultOrganization bool
	DefaultEnterprise   bool
	Organization        []string
	Enterprise          []string
}

func (g RunnerGroups) IsEmpty() bool {
	return !g.DefaultOrganization && !g.DefaultEnterprise && len(g.Organization) == 0 && len(g.Enterprise) == 0
}

func ContainsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
