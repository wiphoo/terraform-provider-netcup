# Gates: PR #149 Codex loop — measured final state (head a8d7d40, round 12)

OWNS: internal/provider/server_snapshot_resource.go, internal/provider/server_snapshot_resource_test.go, .unlazy/pr149-codex/**

Scope: drive wiphoo/terraform-provider-netcup PR #149 to a measured final state — latest fixes committed, pushed, and verified locally, with Codex's verdict on the current head received and no open Codex feedback remaining.

- [x] G0: this ledger states outcomes that can fail
  CHECK: node /home/terng/.agents/skills/unlazy/scripts/gate-lint.mjs /home/terng/work/personal/wiphoo/terraform-provider-netcup/terraform-provider-netcup.git/.unlazy/pr149-codex/gates.md
  EXPECT: LINT OK
  EVIDENCE: exit=0; shell=/bin/sh; cwd=/home/terng/work/personal/wiphoo/terraform-provider-netcup/terraform-provider-netcup.git/.unlazy/pr149-codex; path=b9499fe32217/37 entries; EXPECT=matched; output-sha256=48630b7361dd44ee870917b12c3d19b9d7bdea738aaca16bb04d4cab83b772d2; output-bytes=8

- [x] G1: round-12 commit a8d7d40 is the remote PR branch head
  CHECK: git ls-remote origin refs/heads/issue-142/server-snapshot-resource
  EXPECT: a8d7d401dca1134c5b7dd0519bfe61b9919a1017
  EVIDENCE: exit=0; shell=/bin/sh; cwd=/home/terng/work/personal/wiphoo/terraform-provider-netcup/terraform-provider-netcup.git/.unlazy/pr149-codex; path=b9499fe32217/37 entries; EXPECT=matched; output-sha256=11693f7e735ae23a960cebc88871050591ac827e2cc0376483d0cb05044a6b7f; output-bytes=87

- [x] G2: full local check suite is green on the committed head
  CHECK: cd /home/terng/work/personal/wiphoo/terraform-provider-netcup/terraform-provider-netcup.git && git rev-parse --verify HEAD && go build ./... && go test ./... -count=1 && go vet ./... && test -z "$(gofmt -l .)" && golangci-lint run --new-from-rev=origin/main && echo CHECKS-PASSED
  EXPECT: CHECKS-PASSED
  EVIDENCE: exit=0; shell=/bin/sh; cwd=/home/terng/work/personal/wiphoo/terraform-provider-netcup/terraform-provider-netcup.git/.unlazy/pr149-codex; path=b9499fe32217/37 entries; EXPECT=matched; output-sha256=e2de87472ef554582ac93704618996d70a04955af233bd7a801641502c647be1; output-bytes=510

- [ ] G3: a Codex verdict exists for head a8d7d40 (Codex review on that commit, or a reviewer +1 reaction on the PR)
  CHECK: N=$(gh api repos/wiphoo/terraform-provider-netcup/pulls/149/reviews --jq '[.[] | select(.user.login == "chatgpt-codex-connector[bot]") | select(.commit_id | startswith("a8d7d401dca1134c5b7dd0519bfe61b9919a1017"))] | length'); R=$(gh api repos/wiphoo/terraform-provider-netcup/issues/149/reactions --jq '[.[] | select(.content == "+1")] | length'); test "$N" -ge 1 -o "$R" -ge 1 && echo CODEX-VERDICT-RECEIVED
  EXPECT: CODEX-VERDICT-RECEIVED
  EVIDENCE: pending

- [ ] G4: no unresolved review threads remain on the PR (clean final state)
  CHECK: T=$(gh api graphql -f query='query { repository(owner: "wiphoo", name: "terraform-provider-netcup") { pullRequest(number: 149) { reviewThreads(last: 100) { nodes { isResolved } } } } }' --jq '[.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved == false)] | length'); test "$T" -eq 0 && echo NO-OPEN-THREADS
  EXPECT: NO-OPEN-THREADS
  EVIDENCE: pending
