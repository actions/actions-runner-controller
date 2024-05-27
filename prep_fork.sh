#!/usr/bin/env bash

set -e

for f in arc-publish.yaml arc-{publish,validate}-chart.yaml \
         arc-{release-runners,validate-runners,update-runners-scheduled}.yaml gha-{publish,validate}-chart.yaml \
         global-run-{first-interaction,stale}.yaml; do
    echo "Processing $f"
    git rm .github/workflows/$f
done

git commit -m "Remove workflows unused in a forked repo"

# cherry-pick: Remove legacy-canary-build job from global-publish-canary.yaml as unused in a forked repo
git cherry-pick 842b16d71498a5f2a1fc17c0917372435464d49c
