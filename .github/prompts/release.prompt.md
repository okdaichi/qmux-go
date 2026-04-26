# Release Prompt

Use this prompt when preparing a new release. It should summarise the
entire process so that the AI (or a human receiver of the prompt) has clear
instructions and context. A comprehensive release prompt typically covers:

- **version number** (e.g. `v1.2.3`)
- **changelog highlights** – key bug fixes, features, and breaking changes
- **testing status** – confirmation that unit/integration tests pass and any
  special test commands run
- **tagging and git workflow** – which branch to tag, whether to push annotated
  tags, and any release branches to create
- **artifact publication** – building binaries, Docker images, or packages and
  where they should be published
- **documentation updates** – updating `CHANGELOG.md`, `SPECIFICATION.md`, docs
  site, and any release notes or `ReleaseNote` files
- **post-release actions** – notifications, updating version numbers in
  dependent repos, and updating package registries
- **special instructions** – anything unusual for this release, such as
  manual steps or known issues to call out


detailed checklist example:

1. Run all tests (`mage test`, `go test ./...`, etc.) and ensure CI passes.
2. Bump version in `version.go` and other relevant files, commit changes.
3. Update `CHANGELOG.md` with entries for the new version. Add any release
   notes or link to GitHub issues.
4. Create or update `ReleaseNote` in docs if your project uses it.
5. Generate documentation or run `hugo` if needed to update the website.
6. Tag the commit with `git tag -a vX.Y.Z -m "Release vX.Y.Z"` and push tags.
7. Build and publish artifacts (Go binaries, Docker images, npm packages, etc.).
8. Create a GitHub release using the tag, including the changelog highlights.
9. Announce the release, update any dependent repositories or registries.


Example prompt for the AI:

```markdown
Prepare a release for version v0.10.7. The changes include non-root
Dockerfile, client version wiring, session stream race fixes, and updated
interop tooling. Run all tests, update the CHANGELOG and ReleaseNote, tag the
commit, build the binaries, and create a GitHub release with the tag.
Mention any post-release steps if applicable.
```
