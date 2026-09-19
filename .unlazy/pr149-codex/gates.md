# Gates: PR #149 Codex loop — measured final state (head d79f2e8, round 13)

OWNS: internal/provider/server_snapshot_resource.go, internal/provider/server_snapshot_resource_test.go, .unlazy/pr149-codex/**

Scope: drive wiphoo/terraform-provider-netcup PR #149 to a measured final state — latest fixes committed, pushed, and verified locally, with Codex's verdict on the current head received and no open Codex feedback remaining.

- [x] G0: this ledger states outcomes that can fail
  CHECK: node /home/terng/.agents/skills/unlazy/scripts/gate-lint.mjs /home/terng/work/personal/wiphoo/terraform-provider-netcup/terraform-provider-netcup.git/.unlazy/pr149-codex/gates.md
  EXPECT: LINT OK
  EVIDENCE: exit=0; shell=/bin/sh; cwd=/home/terng/work/personal/wiphoo/terraform-provider-netcup/terraform-provider-netcup.git/.unlazy/pr149-codex; path=b9499fe32217/37 entries; EXPECT=matched; output-sha256=48630b7361dd44ee870917b12c3d19b9d7bdea738aaca16bb04d4cab83b772d2; output-bytes=8

- [x] G1: round-13 commit d79f2e8 is the remote PR branch head
  CHECK: git ls-remote origin refs/heads/issue-142/server-snapshot-resource
  EXPECT: d79f2e89c42eaa6ee124ac5ea2209ab463a1080b
  EVIDENCE: exit=0; shell=/bin/sh; cwd=/home/terng/work/personal/wiphoo/terraform-provider-netcup/terraform-provider-netcup.git/.unlazy/pr149-codex; path=b9499fe32217/37 entries; EXPECT=matched; output-sha256=6a601d8e8ce96455bbec187138e0f3386f157e29a9cde81714857e2f80346cf3; output-bytes=87

- [x] G2: full local check suite is green on the committed head
  CHECK: cd /home/terng/work/personal/wiphoo/terraform-provider-netcup/terraform-provider-netcup.git && git rev-parse --verify HEAD && go build ./... && go test ./... -count=1 && go vet ./... && test -z "$(gofmt -l .)" && golangci-lint run --new-from-rev=origin/main && echo CHECKS-PASSED
  EXPECT: CHECKS-PASSED
  EVIDENCE: exit=0; shell=/bin/sh; cwd=/home/terng/work/personal/wiphoo/terraform-provider-netcup/terraform-provider-netcup.git/.unlazy/pr149-codex; path=b9499fe32217/37 entries; EXPECT=matched; output-sha256=66b05da30234ee1846cc75fe039933d2772d8b35034952c5946b2e04600ef02c; output-bytes=510

- [ ] G3: a Codex verdict exists for head d79f2e8 (Codex review on that commit, or a reviewer +1 reaction on the PR)
  CHECK: N=$(gh api repos/wiphoo/terraform-provider-netcup/pulls/149/reviews --jq '[.[] | select(.user.login == "chatgpt-codex-connector[bot]") | select(.commit_id | startswith("d79f2e89c42eaa6ee124ac5ea2209ab463a1080b"))] | length'); R=$(gh api repos/wiphoo/terraform-provider-netcup/issues/149/reactions --jq '[.[] | select(.content == "+1")] | length'); test "$N" -ge 1 -o "$R" -ge 1 && echo CODEX-VERDICT-RECEIVED
  EXPECT: CODEX-VERDICT-RECEIVED
  EVIDENCE: pending

- [ ] G4: no unresolved review threads remain on the PR (clean final state)
  CHECK: T=$(gh api graphql -f query='query { repository(owner: "wiphoo", name: "terraform-provider-netcup") { pullRequest(number: 149) { reviewThreads(last: 100) { nodes { isResolved } } } } }' --jq '[.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved == false)] | length'); test "$T" -eq 0 && echo NO-OPEN-THREADS
  EXPECT: NO-OPEN-THREADS
  EVIDENCE: pending
