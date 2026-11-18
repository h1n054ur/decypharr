# Fork notes

This is a personal fork of [sirrobot01/decypharr](https://github.com/sirrobot01/decypharr). The branch
`main-patched` is upstream tag `v2.7.2` plus the fixes listed below, one commit
each. `main` and the other upstream branches are left as upstream has them, so
pull requests to upstream still start from a clean base.

If upstream merges or replaces a fix, it is dropped from here at the next
rebase.

## Fixes of my own

- Link validation: a failed validation no longer leaves a file unreadable
  until a restart. Upstream issue [#434](https://github.com/sirrobot01/decypharr/issues/434), offered upstream as
  [#450](https://github.com/sirrobot01/decypharr/pull/450). Upstream now
  treats 400, 403 and 410 as refetchable, so what is left here is the rest:
  a remembered failure expires after a minute, and an unknown code is
  retryable instead of permanent.
- Streaming: sessions reuse the resolved CDN target of a provider redirect
  link, so reconnects, seeks and resumes don't each call the provider API. A
  throttle that lasts past one wait drops the cached target and refreshes the
  link, instead of retrying the same link forever. Since v2.7.2 upstream asks
  TorBox for the CDN URL once (`requestdl` with `redirect=false`) and streams
  that, so for TorBox the cache only comes into play when that call fails and
  the redirect link is used instead. The throttle recovery applies either way.
- Uncached refusals: releases refused because no provider has them cached are
  kept for 24 hours and listed at `GET /api/refusals/uncached`
  (`queue_cleanup.record_uncached_refusals`, on by default). The arrs count
  such a grab as skipped and retry it from their pending queue forever, so a
  job next to the arrs uses the list to blocklist those pending items and let
  the arr pick another release.

- TorBox pack names: TorBox sometimes names a cached season pack after one of
  its files (for example `Show.S33E06...mkv` for the folder `Show.S33.../`).
  Reported as the qBittorrent name, that made Sonarr treat the whole season as
  one episode and remove the download after importing that one file. When
  every file sits under one root folder, the folder is reported as the name,
  as qBittorrent does for a multi-file torrent.
- qBittorrent hashes: a `hashes` form value with several hashes joined by
  `|` (the qBittorrent Web API format) is split into single hashes. Before,
  the joined string was looked up as one hash, so `torrents/delete` with more
  than one hash answered 200 and deleted nothing.
- Dead TorBox items: some items answer `requestdl` with HTTP 500 and
  `DATABASE_ERROR` for every file, also after a re-add. Before, the read path
  retried such a file without end (session resumes, chunk retries, a fresh
  downloader per waiting read, the circuit breaker reset after its cooldown),
  so a media probe over the mount hung until a restart. Now a dead-item code
  in the provider's answer (`DATABASE_ERROR`, `ITEM_NOT_FOUND`,
  `LINK_OFFLINE`) is retried with backoff, and after 3 such answers within
  2 minutes (about 6 seconds of waits) the file is marked dead for 6 hours:
  the read fails with EIO and later opens fail at once without a provider
  call. A good answer clears the count, and plain 5xx answers keep the old
  transient retry. The redirect-link fallback is retained and will be checked
  in the live stream verification with upstream's `redirect=false` call.
  Dead items are kept for 24 hours and listed at
  `GET /api/refusals/dead` (hash, name, category, file, provider and error
  code, newest first, `?since=<RFC 3339>` narrows it), so a job next to the
  arrs can drop the download before an import reaches it.
- Refresh grace: TorBox's torrent list can lag a fresh add, so the refresh
  right after an add found the pack missing and dropped it from the mount
  while the queue still showed it complete. Imports in that gap failed with
  file not found until the next refresh brought it back. Now a placement the
  remote list doesn't show is kept while it was added or completed within
  `refresh_missing_grace` (per debrid, default `30m`, `"0"` turns it off).
  Deletes Decypharr makes itself remove the entry at once as before, and an
  item the provider dropped goes on the first refresh after the grace.

## Open upstream pull requests

Each one is squashed into a single commit whose title ends in "(upstream #N)":
[#445](https://github.com/sirrobot01/decypharr/pull/445), [#444](https://github.com/sirrobot01/decypharr/pull/444), [#411](https://github.com/sirrobot01/decypharr/pull/411), [#418](https://github.com/sirrobot01/decypharr/pull/418), [#367](https://github.com/sirrobot01/decypharr/pull/367), [#432](https://github.com/sirrobot01/decypharr/pull/432), [#394](https://github.com/sirrobot01/decypharr/pull/394) and
[#340](https://github.com/sirrobot01/decypharr/pull/340).

Small changes needed to build or pass the tests are folded into the same
commit (a missing import in #340, test fixtures in #411 and #445). Upstream
[#439](https://github.com/sirrobot01/decypharr/pull/439) now refetches an expired cached link in the account, so only the
link service part of #411 is left.

## Dropped at the v2.7.2 rebase

- [#412](https://github.com/sirrobot01/decypharr/pull/412), [#440](https://github.com/sirrobot01/decypharr/pull/440), [#419](https://github.com/sirrobot01/decypharr/pull/419), [#422](https://github.com/sirrobot01/decypharr/pull/422), [#417](https://github.com/sirrobot01/decypharr/pull/417) and [#448](https://github.com/sirrobot01/decypharr/pull/448) were cherry-picked
  from `beta` and are part of v2.7.2.
- [#442](https://github.com/sirrobot01/decypharr/pull/442) is replaced by upstream [#441](https://github.com/sirrobot01/decypharr/pull/441), which cancels the post-download
  action of a deleted entry. The fork retains #442's late-update guard,
  cancellation wait of up to 15 seconds and partial download cleanup.
- [#424](https://github.com/sirrobot01/decypharr/pull/424) is replaced by upstream [#423](https://github.com/sirrobot01/decypharr/pull/423), which keeps a shared folder in the
  name index on delete.
  `8fdf791`'s one-time startup reconciliation was dropped at v2.7.2 because
  the deployed patched builds had already run it.
- [#406](https://github.com/sirrobot01/decypharr/pull/406) is replaced by upstream's own AllDebrid fix, which waits until every
  file of a ready magnet has a link.

## Left out for now

- #402 and #369 touch the same code as the #434 fix. #402 is a subset of what
  upstream now does.
  #369 makes 429 and 503 refetchable, which spends more provider API calls
  under load.
- #370 is covered by #444, which does the same cache check and also handles
  the add limit.
- #430 duplicates #432.
- #426 and #410 conflict with the changes above. #348 no longer built on v2.7 (not checked again on v2.7.2).
- Larger features and older pull requests written against earlier code.

## Image builds

`.forgejo/` holds a Forgejo Actions workflow that builds the image with kaniko
and pushes it to a registry set in the repository's Actions variables. GitHub
ignores it. The image tag is `TAG` in `.forgejo/workflows/image.yml`, and it
gets bumped for every new build, since a tag is never pushed twice. Tags read
`v<upstream version>-patched-<n>`, and `n` starts again at 1 on a new base.

## Keeping up with upstream

The branch is kept in a private repository and pushed here by hand after each
change. A new upstream release gets the commits rebased onto it:

```sh
git remote add upstream https://github.com/sirrobot01/decypharr.git   # once
git fetch upstream --tags
git switch -c main-patched-vX.Y main-patched
git rebase --onto vX.Y v2.7.2    # v2.7.2 is the base the branch has now
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
