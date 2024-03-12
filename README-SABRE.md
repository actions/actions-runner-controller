# Local development and update procedure

Versions are kept in separate branches: release/<ARC_version>-custom.

## Prerequisites

1. Clone `sabre-internal/gh-core.github-sabre-arc` repository.
2. Link `gh-core.github-sabre-arc/sabreoss-arc-devenv` folder as `./internal`.
3. Enter the folder and then run `devenev shell`. 
4. Run all commands in the shell.

## Update procedure

1. Upgrade the fork branch with the original GH ARC repo:
    * Upgrade should be made to the branch `master`, which is clean from our changes
2. Create new custom target branch `release/<ARC_version>-custom` from the updated `master` branch or specific commit (if default branch is ahead selected base version)
3. Create your feature branch e.g: `feature/update-to-<ARC_version>` from previous `release/<ARC_previous-version>-custom`
   and rebase it on `release/<ARC_version>-custom`
    * If there are any conflicts, resolve them testing the code execution
4. Update the `Makefile.sabre.mk` file, changing `VERSION` and `DOCKER_IMAGE_VERSION` to <ARC_version>-sabre-1
    * All other changes to the fork should introduce update in the `Makefile.sabre.mk` file as well
      (e.g. next change in the controller image should be `DOCKER_IMAGE_VERSION=<current_version>-sabre-2`)
5. Create pull request to `release/<ARC_version>-custom` and merge it.
6. Release charts and controller/listener image from branch `release/<ARC_version>-custom`:
    * Run `make -f Makefile.sabre.mk upload-all` or other targets to release only charts or image.
