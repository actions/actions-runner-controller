package testing

import (
	"context"
	"errors"
	"testing"
)

func TestGitRepoSyncValidation(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		repo GitRepo
		want error
	}{
		{
			name: "missing name",
			repo: GitRepo{Dir: "/tmp/repo", Branch: "main"},
			want: errors.New("missing git repo name"),
		},
		{
			name: "missing dir",
			repo: GitRepo{Name: "owner/repo", Branch: "main"},
			want: errors.New("missing git dir"),
		},
		{
			name: "missing branch",
			repo: GitRepo{Name: "owner/repo", Dir: "/tmp/repo"},
			want: errors.New("missing git branch"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.repo.Sync(ctx)
			if err == nil {
				t.Fatalf("expected error %q, got nil", tt.want)
			}
			if err.Error() != tt.want.Error() {
				t.Errorf("expected error %q, got %q", tt.want, err)
			}
		})
	}
}
