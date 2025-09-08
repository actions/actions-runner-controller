# Release Note Template

This is the template of actions-runner-controller's release notes.

Whenever a new release is made, I start by manually copy-pasting this template onto the GitHub UI for creating the release.

I then walk-through all the changes, take sometime to think abount best one-sentence explanations to tell the users about changes, write it all,
and click the publish button.

If you think you can improve future release notes in any way, please do submit a pull request to change the template below.

Note that even though it looks like a Go template, I don't use any templating to generate the changelog.
It's just that I'm used to reading and intepreting Go template by myself, not a computer program :)

**Title**:

```
v{{ .Version }}: {{ .TitlesOfImportantChanges }}
```

**Body**:

```
**CAUTION:** If you're using the Helm chart, beware to review changes to CRDs and do manually upgrade CRDs! Helm installs CRDs only on installing a chart. It doesn't automatically upgrade CRDs. Otherwise you end up with troubles like #427, #467, and #468. Please refer to the [UPGRADING](charts/actions-runner-controller/docs/UPGRADING.md) docs for the latest process.

This release includes the following changes from contributors. Thank you!

- @{{ .GitHubUser }} fixed {{ .Feature }} to not break when ... (#{{ .PullRequestNumber }})
- @{{ .GitHubUser }} enhanced {{ .Feature }} to ... (#{{ .PullRequestNumber }})
- @{{ .GitHubUser }} added {{ .Feature }} for ... (#{{ .PullRequestNumber }})
- @{{ .GitHubUser }} fixed {{ .Topic }} in the documentation so that ... (#{{ .PullRequestNumber }})
- @{{ .GitHubUser }} added {{ .Topic }} to the documentation (#{{ .PullRequestNumber }})
- @{{ .GitHubUser }} improved the documentation about {{ .Topic }} to also cover ... (#{{ .PullRequestNumber }})
```
