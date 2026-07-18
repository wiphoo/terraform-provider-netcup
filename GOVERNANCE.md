# Project Governance

This document describes how the **terraform-provider-netcup** project is
governed. It is intentionally lightweight: the project currently has a single
maintainer, and this document also defines how governance evolves as the project
grows.

## Roles

### Maintainer

Maintainers are responsible for the overall health and direction of the project.
A maintainer may:

- Set the roadmap and technical direction.
- Review, approve, and merge pull requests.
- Cut and publish releases.
- Manage repository settings, labels, and branch protection
  (see [`.github/settings.yml`](.github/settings.yml)).
- Triage issues and enforce the [Code of Conduct](CODE_OF_CONDUCT.md).

The current maintainers are the code owners listed in
[`.github/CODEOWNERS`](.github/CODEOWNERS).

### Contributor

Anyone who submits an issue or pull request is a contributor. Contributors do
not need any special permissions — see [CONTRIBUTING.md](CONTRIBUTING.md) to get
started. There is no CLA; contributions are accepted under the project's
[MPL-2.0 license](LICENSE).

## Decision-making

The project currently operates under a **BDFL (Benevolent Dictator For Life)**
model, reflecting its single-maintainer reality:

- The maintainer has final say on all technical and project decisions.
- Routine changes (bug fixes, docs, dependency bumps) are merged once CI is
  green and the change is reviewed.
- Significant or potentially contentious changes (new resources, breaking API
  changes, changes to the auth model) should start as an issue so direction can
  be discussed **before** code is written — as already noted in
  [CONTRIBUTING.md](CONTRIBUTING.md).
- Discussion happens in the open, on GitHub issues, pull requests, and
  [Discussions](https://github.com/wiphoo/terraform-provider-netcup/discussions).

## Adding maintainers

New maintainers are added by invitation from an existing maintainer, based on a
track record of high-quality, sustained contributions and good judgment in
reviews and discussions. When a second maintainer is added, this document and
`.github/settings.yml` will be updated to:

- Require at least one approving review before merge
  (`required_approving_review_count: 1`).
- Enable code-owner reviews (`require_code_owner_reviews: true`).

At that point the project transitions from BDFL to **lazy consensus**:
proposals are considered accepted if no maintainer objects within a reasonable
review window, with unresolved disagreements decided by a simple majority of
maintainers.

## Conflict resolution

Most disagreements are resolved through discussion on the relevant issue or pull
request. If consensus cannot be reached:

- While the project has a single maintainer, the maintainer makes the final
  call.
- Once there are multiple maintainers, a decision is reached by a simple
  majority vote of the maintainers; ties are broken by the longest-tenured
  maintainer.

Conduct-related concerns are handled separately under the
[Code of Conduct](CODE_OF_CONDUCT.md); report them to the contact listed there.

## Changing this document

Changes to governance are themselves proposed via pull request and are subject
to the decision-making process above.
