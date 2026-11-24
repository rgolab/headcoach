# Contributing

<!-- How to report a bug, suggest an idea or send a change to headcoach. -->

## Issues first

- a bug goes in an issue, with the bug report form
- an idea or a question goes in Discussions
- a pull request needs a maintainer's go-ahead on an issue first
- a pull request without one is closed automatically, nothing personal
- using an agent to write a change is fine, you stay accountable for it

## Changes

- every change reaches `main` through a pull request, the maintainer's too
- a pull request is merged once its CI is green
- `make check` passes
- one commit per feature or fix, with its tests and docs
- a fix, test or doc of a change not yet pushed is folded into its commit
- commit subjects read `area: Subject`, the area a package or `docs`, `build`, `ci`
- a test, or a way to check, for each change
- no new dependency without a discussion first
- a user-facing change adds a line under `Unreleased` in `CHANGELOG.md`
- a user-facing change updates the `README.md` section that describes it

## Releases

- a release is its own pull request, opened when the maintainer decides to ship, after any number of merged changes
- it comes from a branch `release-vX.Y.Z`
- it moves the `Unreleased` lines of `CHANGELOG.md` under a new `## [X.Y.Z] - date` heading
- it adds the compare link at the bottom of `CHANGELOG.md`, and points `[Unreleased]` at the new tag
- once it is merged, tag its merge commit and push the tag: `git fetch origin && git tag -a vX.Y.Z -m vX.Y.Z origin/main && git push origin vX.Y.Z`
- the release workflow builds the binaries and publishes the release with that section as notes
- `scripts/changelog.sh vX.Y.Z` prints the notes before you tag

## License

- headcoach is licensed under Apache-2.0
- a contribution is licensed under Apache-2.0 too
