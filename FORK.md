# Fork notes

This is a personal fork of [sirrobot01/decypharr](https://github.com/sirrobot01/decypharr). The branch
`main-patched` is upstream tag `v2.7` plus the fixes listed below, one commit
each. `main` and the other upstream branches are left as upstream has them, so
pull requests to upstream still start from a clean base.

If upstream merges or replaces a fix, it is dropped from here at the next
rebase.

## Fixes of my own

- Link validation: a transient HTTP 400 no longer leaves a file unreadable
  until a restart. Upstream issue [#434](https://github.com/sirrobot01/decypharr/issues/434), offered upstream as
  [#450](https://github.com/sirrobot01/decypharr/pull/450).
- Streaming: sessions reuse the resolved CDN target of a provider redirect
  link (TorBox `requestdl`), so reconnects, seeks and resumes don't each call
  the provider API. A throttle that lasts past one wait drops the cached target
  and refreshes the link, instead of retrying the same link forever.
- Uncached refusals: releases refused because no provider has them cached are
  kept for 24 hours and listed at `GET /api/refusals/uncached`
  (`queue_cleanup.record_uncached_refusals`, on by default). The arrs count
  such a grab as skipped and retry it from their pending queue forever, so a
  job next to the arrs uses the list to blocklist those pending items and let
  the arr pick another release.

## Upstream commits from `beta` (after v2.7)

[#412](https://github.com/sirrobot01/decypharr/pull/412), [#440](https://github.com/sirrobot01/decypharr/pull/440), [#419](https://github.com/sirrobot01/decypharr/pull/419), [#422](https://github.com/sirrobot01/decypharr/pull/422), [#417](https://github.com/sirrobot01/decypharr/pull/417) and [#448](https://github.com/sirrobot01/decypharr/pull/448), cherry-picked
with `-x`.

## Open upstream pull requests

Each one is squashed into a single commit whose title ends in "(upstream #N)":
[#445](https://github.com/sirrobot01/decypharr/pull/445), [#444](https://github.com/sirrobot01/decypharr/pull/444), [#411](https://github.com/sirrobot01/decypharr/pull/411), [#424](https://github.com/sirrobot01/decypharr/pull/424), [#418](https://github.com/sirrobot01/decypharr/pull/418), [#367](https://github.com/sirrobot01/decypharr/pull/367), [#432](https://github.com/sirrobot01/decypharr/pull/432), [#442](https://github.com/sirrobot01/decypharr/pull/442),
[#406](https://github.com/sirrobot01/decypharr/pull/406), [#394](https://github.com/sirrobot01/decypharr/pull/394) and [#340](https://github.com/sirrobot01/decypharr/pull/340).

Small changes needed to build or pass the tests on v2.7 are folded into the
same commit (a missing import in #340, test fixtures in #411 and #445).

## Left out for now

- #402 and #369 touch the same code as the #434 fix. #402 is a subset of it.
  #369 makes 429 and 503 refetchable, which spends more provider API calls
  under load.
- #370 is covered by #444, which does the same cache check and also handles
  the add limit.
- #430 duplicates #432.
- #426 and #410 conflict with the changes above. #348 no longer builds on v2.7.
- Larger features and older pull requests written against earlier code.

## Image builds

`.forgejo/` holds a Forgejo Actions workflow that builds the image with kaniko
and pushes it to a registry set in the repository's Actions variables. GitHub
ignores it. The image tag is `TAG` in `.forgejo/workflows/image.yml`, and it
gets bumped for every new build, since a tag is never pushed twice.

## Keeping up with upstream

The branch is kept in a private repository and pushed here by hand after each
change. A new upstream release gets the commits rebased onto it:

```sh
git remote add upstream https://github.com/sirrobot01/decypharr.git   # once
git fetch upstream --tags
git switch -c main-patched-vX.Y main-patched
git rebase --onto vX.Y v2.7    # v2.7 is the base the branch has now
# Drop commits upstream has merged (check the "(upstream #N)" titles against
# upstream's log), fix conflicts, run go vet ./... and go test ./...,
# bump TAG in .forgejo/workflows/image.yml, then:
git switch main-patched && git reset --hard main-patched-vX.Y
git push --force-with-lease <private remote> main-patched
git push --force-with-lease <github remote> main-patched
```

Fixes merged only to upstream `beta` are taken with `git cherry-pick -x <sha>`.
An open upstream pull request is taken with
`git fetch upstream pull/N/head:refs/pr/N`, then `git merge --squash refs/pr/N`,
committed with "(upstream #N)" at the end of the title.
