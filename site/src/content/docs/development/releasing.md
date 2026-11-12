---
title: Releasing
description: Publish a new version with one click.
---

Run **Actions → Release → Run workflow** on `main`, or:

```bash
gh workflow run release.yml                  # automatic bump
gh workflow run release.yml -f bump=minor    # force patch, minor, or major
gh workflow run release.yml -f dry_run=true  # build only, publish nothing
```

## Version bump

The next version comes from the
[Conventional Commits](https://www.conventionalcommits.org) since the last
`vX.Y.Z` tag:

| Commits | Bump |
| --- | --- |
| `type!:` subject, or a `BREAKING CHANGE:` footer | major |
| Any `feat:` | minor |
| Anything else | patch |

Choosing `patch`, `minor`, or `major` overrides it. The run stops early if it
isn't on `main`, there are no new commits, or the tag already exists.

## What happens

1. The CLI (after tests), desktop app, and browser extension are built in
   parallel, all stamped with the new version.
2. Only if every build succeeds, the tag is created on the built commit and
   the GitHub release is published with generated notes and all files.

A failed build publishes nothing, so there's nothing to clean up. A **dry
run** attaches the builds to the workflow run as artifacts instead.

## Signing

The desktop app is signed only on platforms whose credentials are configured;
otherwise unsigned builds are published. The CLI binaries are never signed.

| Platform | Secrets | Variables |
| --- | --- | --- |
| macOS (sign) | `MACOS_CERTIFICATE_P12` (base64 Developer ID Application `.p12`), `MACOS_CERTIFICATE_PASSWORD`, `MACOS_SIGN_IDENTITY` (e.g. `Developer ID Application: Name (TEAMID)`) | |
| macOS (notarize) | `APPLE_ID`, `APPLE_TEAM_ID`, `APPLE_APP_PASSWORD` (app-specific password) | |
| Windows ([Artifact Signing](https://learn.microsoft.com/azure/artifact-signing/)) | `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET` | `AZURE_SIGNING_ENDPOINT`, `AZURE_SIGNING_ACCOUNT`, `AZURE_CERTIFICATE_PROFILE` |

Notarization runs only after signing succeeds.
