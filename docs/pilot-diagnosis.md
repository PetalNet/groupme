# Synapse pilot diagnosis

Investigated the running `c7e93c7912e9c3830dc38147818c80d68ef3fa88` pilot on October 17, 2026. This patch is review-only. No bridge, Synapse, registration, database, or user session was modified during this investigation.

## History ordering and initial backfill

GroupMe's [message API](https://dev.groupme.com/docs/v3#messages) returns newest-first for default and `before_id` requests, but oldest-first for `after_id`. `pkg/connector/backfill.go:49` normalizes each page to descending IDs. The pagination loop at line 115 advances `before_id` to the oldest ID or `after_id` to the newest ID. Line 145 sorts the combined result ascending before conversion. `Forward` follows the request; `HasMore` describes whether the native page was full, except when the forward anchor is reached. A full final page can require one more empty request. DM requests have a fixed native page size of 20, so a returned batch can exceed the preferred count.

That matches bridgev2 v0.31.0's `FetchMessagesResponse` contract in `bridgev2/networkinterface.go:568`. `Forward` responses ignore cursor and `HasMore`. Standard Synapse uses `sendLegacyBackfill` in `bridgev2/portalbackfill.go:589`, which sends the supplied order.

The defect is earlier: the group and DM `ChatInfo` responses omitted `CanBackfill`. Bridgev2 checks this flag in `bridgev2/portal.go:5525` before initial room history. A room-creating ChatResync returns without running the later existing-room backfill check (`portal.go:2639`). Meanwhile, `pkg/connector/client.go:129` queues chat creation and starts reaction/edit refresh. New rooms can therefore process reactions/edits with no history, until a later resync backfills them. Set `CanBackfill: true` in both ChatInfo responses. Do not reverse the already-correct FetchMessages output.

Read-only live evidence:

- 1,203 bridge message records across 65 rooms had zero timestamp reversals in insertion order.
- The corresponding Synapse events had zero timestamp reversals in either stream order or topological order.
- Of 654 missing reaction-target warnings, 50 targets are now present. All other 604 targets precede their portal's retained history window.
- One of seven missing edit targets is now present. Six edit targets and all nine reply targets remain absent.
- Reaction reconciliation requests up to 100 group messages (`pkg/connector/sync.go:137`), while this pilot retains 50 initial messages. The initial-backfill fix cannot make out-of-window targets exist. The 387 full-channel warnings indicate queue pressure, not reversed message order or proof of dropped events.

These observations establish the initial-backfill defect, but do not reproduce the client's reported reversed timeline. An affected room/client example is still needed to explain that display.

## Names

The connector already returns names. Group members use `name`, then `nickname`, then ID (`pkg/connector/chatinfo.go:91`). GetUserInfo supplies contact/self names, and message conversion fills missing ghost profiles from `msg.Name` (`pkg/connector/handlegroupme.go:83`). Bridgev2 applies the Name pointer independently of avatar success (`bridgev2/ghost.go:335` and `pushProfileChanges`).

A reproducible defect remains: a contact or self response containing an empty/missing name produced a non-nil pointer to an empty string. Bridgev2 interprets that as clearing the Matrix name. The same issue exists in contact-list and DM-member profiles. Use `ptr.NonZero` for these optional account names so incomplete responses preserve a known name and still provide available avatars.

At inspection time, all 398 Synapse ghost profiles and all 1,607 stored ghost membership events had displaynames. The bridge had 398 named user ghosts and one empty internal system ghost. The empty-profile regression proves a code defect; it does not prove it caused Eli's particular missing-name display. No claim of a verified client-side fix is made.

## Avatar 403s

`pkg/connector/chatinfo.go:41` calls `DownloadImage`, which passes an empty token to the downloader (`pkg/groupmeext/message.go:17`). No access-token header or token cookie is sent. Two affected `https://i.groupme.com` URLs returned 403 without credentials. One response reported XML `AccessDenied`; an existing successful avatar returned 200 through the same unauthenticated path. Five ghost avatar URLs currently lack a stored MXC.

This is not evidence of an erroneously attached token. The provider-side reason for these objects being inaccessible is not established. Leave avatar downloading unchanged rather than send credentials to a media host speculatively.

## Tests

On the pinned production files, with the new tests retained:

- `TestChatInfoEnablesInitialBackfill` fails for groups and DMs: `new portal cannot backfill before queued reactions and edits`.
- `TestGetUserInfoPreservesKnownNameWhenContactNameMissing` fails: `known name would be overwritten with ""`.
- `TestIncompleteProfilesDoNotClearNames` fails for self, contact-list, and DM profiles.

All pass with the patch. `TestFetchMessagesChronologicalResponse` protects existing ordering, native cursor choice, response cursor, HasMore, and Forward for groups and DMs. It also passes on the pinned implementation, as expected. Existing tests cover continuation through edit-only pages and invalid pagination.

Validation commands:

```sh
go test ./... -count=1
go test -tags goolm ./... -count=1
```

## Existing rooms and safe recovery

No re-backfill or deletion is justified by the current ordering evidence. The patch affects initial history in newly created portals and future profile updates. It does not reorder existing Matrix events. A reconnect/resync can catch up from the latest known message; it cannot fill arbitrary older gaps once a newer anchor exists. Standard Synapse does not support inserting older events into an existing timeline.

If a specific room is later proven to have an unrecoverable historical gap or ordering problem, plan a separate, authorized maintenance action: back up the GroupMe database and config, preserve the old room/history, and recreate only that portal with an agreed bounded initial-history count after deploying the reviewed fix. Confirm the new room before retiring the old one. Do not wipe the bridge database, log everyone out, or run delete-all-portals. Bridgev2's `delete-portal` command is destructive and admin-only; neither pilot user currently has admin permissions. No recovery action was performed here.

## Follow-up: reported empty group topic

The pinned connector already maps `group.Description` to a non-nil `ChatInfo.Topic` (`chatinfo.go:118` at c7e93c7, line 119 on this branch), including an empty description so intentional clearing can propagate. `HandleGroupTopic` (`handlegroupme.go:430`) queues a full GetChatInfo resync.

Read-only checks found 29 group portals, four with nonempty bridge topics. All four exactly match the current `m.room.topic` state in Synapse. No topic contents were printed. This rules out a universally missing mapping, but does not explain the reported empty room. Follow-up needs the affected group/room and a comparison between its current native description, bridge metadata, and Matrix state, plus metadata-refresh errors. Without that evidence, changing the mapping would duplicate existing behavior or risk treating an intentional empty description as stale data. No production change or live resync was performed for this report.

## Follow-up: room avatars visible, ghost avatars missing in the client

Room and ghost avatars both use `avatarFor` -> `groupmeext.DownloadImage` -> `downloadMediaURL` with no token (`chatinfo.go:34`, `message.go:17`). There is no separate user-image authentication path to correct.

Additional read-only checks found:

- 257 of 398 user ghosts have uploaded avatars, matching 257 nonempty Synapse global profile avatars. Another 136 ghosts have no stored native avatar URL; five have a URL but no successful upload.
- Among 48 DM portals, 33 partners have uploaded avatars, 14 have no stored native avatar URL, and one has a failed upload.
- Of 751 current joined ghost memberships in Synapse, 501 belong to profiles with avatars. All 501 membership avatar URLs exactly match their global profiles; none is empty.
- All five failing native URLs returned 403 for the original URL and each `.avatar`, `.large`, and `.preview` suffix, without credentials. No URL contents or images were printed.

The 403 failures are real but cannot explain a client-wide absence of ghost avatars. No authentication or URL-rewrite fix is supported by these checks. Follow-up needs an affected DM/ghost and client: compare its native profile URL, current Matrix membership/profile avatar, and the client's media fetch/cache behavior. The missing stored native URLs also need comparison against current provider profile data before assuming those people have no avatar. No credential-based retry, live profile refresh, deployment, or speculative avatar fallback was performed.
