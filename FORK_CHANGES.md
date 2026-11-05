# Fork changes

This fork of
[`aldinokemal/go-whatsapp-web-multidevice`](https://github.com/aldinokemal/go-whatsapp-web-multidevice)
adds four features: the [`media-retry`](#media-retry-endpoint)
endpoint, the [`chat label`](#chat-label-endpoint) endpoint, the
[labels API](#labels-api) (`GET/POST /labels`, `PUT/DELETE
/labels/{label_id}`, typed label webhooks) and the
[`chat unread`](#chat-unread-endpoint) endpoint (+ `chat.read_state`
webhook). All follow the same rules: new
files for the feature, minimal edits to upstream files marked with
`// Fork-specific:` comments, documented in `docs/openapi.yaml`.

# `media-retry` endpoint

This fork of
[`aldinokemal/go-whatsapp-web-multidevice`](https://github.com/aldinokemal/go-whatsapp-web-multidevice)
adds a single feature: a REST endpoint that uses whatsmeow's
`SendMediaRetryReceipt` / `DecryptMediaRetryNotification` flow to recover
media whose CDN URLs have expired (typically for messages older than
~30 days).

## Why

The stock `GET /message/{id}/download` forwards the signed URL stored in
whatsmeow's message cache straight to `mmg.whatsapp.net`. Once
`oh=`/`oe=` expire, the CDN returns `410 Gone` and `download failed
with status code 410` bubbles up as a 500 INTERNAL_SERVER_ERROR. WhatsApp
supports a _media retry_ round-trip — the client asks the server to tell
the original sender to re-upload — but none of that is exposed by
upstream gowa. This fork plugs it in.

## What's added (files)

| File | Change | Purpose |
| --- | --- | --- |
| `src/infrastructure/whatsapp/mediaretry/waiter.go` | new | In-process `sync.Map` registry of waiters keyed by `message_id`. |
| `src/infrastructure/whatsapp/event_handler.go` | +6 lines | Routes `*events.MediaRetry` to the waiter registry. |
| `src/domains/message/media_retry.go` | new | `MediaRetryRequest` / `MediaRetryResponse` DTOs. |
| `src/domains/message/interfaces.go` | +1 method | Adds `MediaRetry(...)` to `IMessageManagement`. |
| `src/usecase/message_media_retry.go` | new | Service impl: send receipt, wait for event, decrypt, download via `DownloadMediaWithPath`, save to disk. |
| `src/ui/rest/message_media_retry.go` | new | Fiber (v3) handler with 404/502/504 classification. |
| `src/ui/rest/message.go` | +2 lines | Registers `POST /message/:message_id/media-retry`. |
| `docs/openapi.yaml` | +1 path | Documents the new endpoint. |
| `src/infrastructure/whatsapp/mediaretry/waiter_test.go`, `src/usecase/message_media_retry_test.go` | new | Unit tests for the waiter registry and media-type mapping. |

Everything else is untouched.

## How it works

```
┌─ HTTP ──────────────────────────────────────────────────────────┐
│  POST /message/{id}/media-retry  (phone, timeout_seconds)       │
└────────────┬─────────────────────────────────────────────────────┘
             │
             │ 1. Lookup stored message (chatstorage) → mediaKey etc.
             │ 2. mediaretry.Register(id) → chan *events.MediaRetry
             │ 3. client.SendMediaRetryReceipt(ctx, &info, mediaKey)
             │ 4. <-waiter.Ch   (or timeout / ctx.Done)
             │ 5. whatsmeow.DecryptMediaRetryNotification(evt, mediaKey)
             │ 6. client.DownloadMediaWithPath(newDirectPath, ...)
             │ 7. write bytes under statics/media/<chat>/<date>/
             ▼
         JSON with file_path, file_size, media_type
```

The waiter registry is a tiny `sync.Map` that the central event
dispatcher (`src/infrastructure/whatsapp/event_handler.go`) feeds via a
new `case *events.MediaRetry` branch. No other event-handling code is
touched.

## HTTP status map

| Code | When |
| --- | --- |
| 200 | Media downloaded and saved. |
| 400 | Missing `phone`, unsupported `media_type`, bad stored sender JID. |
| 404 | Message not found, no media, no stored `mediaKey`, wrong chat. |
| 502 | Sender refused the retry, decryption failed, refreshed path rejected. |
| 504 | Timed out waiting for `events.MediaRetry` (default 30 s, max 120 s). |

The endpoint is not bound by upstream's global 45 s request timeout: it
waits up to `timeout_seconds` for the notification, then gets a separate
60 s budget for the download.

## Example

```bash
curl -u "$GOWA_USER:$GOWA_PASS" \
  -X POST "https://gowa.example.com/message/3EB0123456789ABCDEF/media-retry" \
  -H "X-Device-Id: SOPORTE_FABIAN" \
  -H "Content-Type: application/json" \
  -d '{
        "phone": "5491112345678@s.whatsapp.net",
        "timeout_seconds": 45
      }'
```

Successful response:

```json
{
  "code": "SUCCESS",
  "message": "Media retry downloaded successfully to statics/media/5491112345678/2023-11-20/1714000000-retry-3EB0123...jpg",
  "results": {
    "message_id": "3EB0123456789ABCDEF",
    "status": "Media retry downloaded successfully to ...",
    "media_type": "image",
    "filename": "1714000000-retry-3EB0123....jpg",
    "file_path": "statics/media/5491112345678/2023-11-20/1714000000-retry-3EB0123....jpg",
    "file_size": 524288
  }
}
```

Timeout response (504):

```json
{
  "code": "MEDIA_RETRY_FAILED",
  "message": "no media retry notification received within 30s"
}
```

## Known limitations / edge cases

- **Sender offline / on old WhatsApp build** → times out (504). There
  is no way to force a retry from the receiver side.
- **Sender re-uploaded already, but notification arrived in a previous
  process lifetime** → the retry is scoped to the current process; no
  persistent queue.
- **Group chats**: the sender JID stored in chatstorage may be `@lid`.
  We parse it as-is and hand it to whatsmeow; if the LID→PN mapping is
  needed by the protocol, whatsmeow handles it internally.
- **`is_from_me` messages**: we set `info.Sender` to the device's own
  JID. In practice there is little reason to retry our own outbound
  media, but the code path does not explicitly block it.
- **Concurrent retries for the same `message_id`**: the later caller
  wins; the earlier one stops receiving the notification and ends with
  its own timeout (504).

# Chat label endpoint

Adds `POST /chat/{chat_jid}/label` with body
`{"label_id": "...", "label_name": "...", "labeled": true|false}`
(operationId `labelChat`), which applies or removes a WhatsApp (Business)
label on a chat.

## Why

Upstream *documented* this exact operation in `docs/openapi.yaml` from
v6.8.0 (PR #341, the pin/unpin feature, 2025-07-12) to v8.10.0, but
never implemented it: there was no handler, usecase or validation for it
in any release, and the phantom entry was dropped from the spec in the
docs cleanup commit `8b73125` (2026-07-03, "docs: update API and webhook
documentation"), the same commit that documented the `label.edit` /
`label.association` webhook events added by PR #659. So on every gowa
version the route answers 404. This fork implements it with the contract
upstream had published.

## What's added (files)

| File | Change | Purpose |
| --- | --- | --- |
| `src/domains/chat/chat_label.go` | new | `LabelChatRequest` / `LabelChatResponse` DTOs. `labeled` is a `*bool` so a missing field is rejected instead of silently unlabeling. |
| `src/domains/chat/interfaces.go` | +3 lines | Adds `LabelChat(...)` to `IChatUsecase`. |
| `src/validations/chat_label_validation.go` | new | `chat_jid`, `label_id`, `label_name` required; `labeled` not nil. |
| `src/usecase/chat_label.go` | new | Service impl: `appstate.BuildLabelChat` + `client.SendAppState`, mirroring `PinChat` / `ArchiveChat`. |
| `src/ui/rest/chat_label.go` | new | Fiber (v3) handler; route `chat_jid` is authoritative over the body. |
| `src/ui/rest/chat.go` | +2 lines | Registers `POST /chat/:chat_jid/label`. |
| `src/ui/rest/chat_test.go` | +5 lines | The existing usecase stub gains the new interface method. |
| `docs/openapi.yaml` | +1 path, +1 schema | Documents the endpoint (`LabelChatResponse`). |
| `src/validations/chat_label_validation_test.go`, `src/usecase/chat_label_test.go`, `src/ui/rest/chat_label_test.go` | new | Unit tests for validation, the whatsmeow patch shape and the handler. |

## How it works

```
POST /chat/{chat_jid}/label  {label_id, label_name, labeled}
   |
   | 1. validate body, resolve chat_jid (LID -> PN) + require login
   | 2. take the per-client label mutex (shared with /labels)
   | 3. resolve the chat's LID (client.Store.LIDs.GetLIDForPN)
   | 4. appstate.BuildLabelChat(index_jid, label_id, labeled) per index key
   |      labeled=true  -> [LID]      (the phone's form; PN if LID unknown)
   |      labeled=false -> [LID, PN]  (one patch, two mutations)
   |      -> PatchInfo{Type: regular, Index: [label_jid, label_id, jid],
   |                   LabelAssociationAction{Labeled}}
   | 5. client.SendAppState(ctx, patch)   (IQ w:sync:app:state, type=set)
   | 6. phone applies it, fans it out to all devices, whatsmeow resyncs
   |    `regular` and emits events.LabelAssociationChat -> label cache +
   |    webhook `label.association`
   v
JSON {status, message, chat_jid, chat_lid?, label_id, label_name, labeled}
```

Why two index forms: the phone writes `label_jid` with the chat's **LID**
(that is why the webhook carries `chat_lid`), while the first version of
this endpoint wrote the phone-number JID. They are different keys that
coexist, so removing by number never cleared what the phone had put under
the LID (and vice versa). Applying now writes the phone's form; removing
writes both so either origin is cleared.

It is the same round-trip `/chat/{chat_jid}/pin` (`BuildPin`) and
`/chat/{chat_jid}/archive` (`BuildArchive`) already use; the only
difference is the collection (`regular` instead of `regular_low`) and
the mutation index.

## HTTP status map

| Code | When |
| --- | --- |
| 200 | Patch accepted by the WhatsApp server. |
| 400 | Malformed JSON, missing `label_id` / `label_name` / `labeled`, bad `chat_jid`. |
| 401 | Basic auth. |
| 500 | Not logged in, `SendAppState` error (server rejected the patch, app state keys missing, LTHash mismatch, ...). |

## Example

```bash
curl -u "$GOWA_USER:$GOWA_PASS" \
  -X POST "https://gowa.example.com/chat/5491112345678%40s.whatsapp.net/label" \
  -H "X-Device-Id: SOPORTE_FABIAN" \
  -H "Content-Type: application/json" \
  -d '{"label_id": "7", "label_name": "Derivado", "labeled": true}'
```

Successful response:

```json
{
  "code": "SUCCESS",
  "message": "Chat labeled successfully with label 'Derivado'",
  "results": {
    "status": "success",
    "message": "Chat labeled successfully with label 'Derivado'",
    "chat_jid": "5491112345678@s.whatsapp.net",
    "label_id": "7",
    "label_name": "Derivado",
    "labeled": true
  }
}
```

## Known limitations / edge cases

- **The label must already exist.** The endpoint only writes the
  chat<->label association; `label_name` in the body cannot create a
  label and is echoed back only. Create labels with `POST /labels` (see
  [Labels API](#labels-api)) or from the phone, and get the ids from
  `GET /labels` or the `label.edit` webhook events.
- **Regular (non-Business) accounts** have no labels; the server may
  accept the patch and the phone will simply ignore it.
- **Read-back** is `GET /labels` (cached, see below); the
  `label.association` webhook that follows a successful patch is the
  confirmation that the phone applied it.
- **LID unknown.** If whatsmeow has never seen the chat's LID (no message
  exchanged since pairing) the association is written under the phone
  number, which is what older builds always did. Whether the phone shows
  that form for every chat is the part verified only on the dev account.
- **App state keys.** `SendAppState` reuses the primary device's app
  state sync key (whatsmeow TODO). If the session has no synced keys yet
  (fresh pairing, before the first `regular` sync) it fails with
  `no app state keys found`; retry after the initial sync.

# `chat unread` endpoint

Adds `POST /chat/{chat_jid}/unread` (mark a chat as unread, or read, on
the account's own devices) and the `chat.read_state` webhook event.

## Why

A bot that answers on a shared Business account reads the chat (read
receipts, so the customer sees the blue ticks) and replies; afterwards the
chat looks "handled" in the phone's chat list. The humans sharing the
account want the opposite: every chat the bot touched should show up in
the phone's **Unread** filter so they review it without hunting for
labels. WhatsApp has that as the "Mark as unread" chat-list entry, which
is an app state mutation (`markChatAsRead`, `regular_low` collection),
not a receipt. whatsmeow builds it (`appstate.BuildMarkChatAsRead`) and
emits `*events.MarkChatAsRead` when another device uses it, but upstream
gowa exposes neither.

## What's added (files)

| File | Change | Purpose |
| --- | --- | --- |
| `src/domains/chat/chat_unread.go` | new | `UnreadChatRequest` / `UnreadChatResponse`, `jid_form` constants. |
| `src/domains/chat/interfaces.go` | +3 lines | `UnreadChat` on `IChatUsecase`. |
| `src/validations/chat_unread_validation.go` | new | `chat_jid` required, `jid_form` in {pn, lid, both}. |
| `src/usecase/chat_unread.go` | new | Resolves the JID, looks up the chat's newest stored message, builds and sends the patch under the per-device app state mutex; full-syncs `regular_low` and retries once when the write is rejected as out of sync (or on `resync: true`). |
| `src/infrastructure/whatsapp/appstate_resync.go` | new | `IsAppStateOutOfSync` (409 / mismatching LTHash) and `FullSyncAppState` (reset + snapshot of one collection, no events). |
| `src/ui/rest/chat_unread.go` | new | Fiber handler (empty body allowed). |
| `src/ui/rest/chat.go` | +1 line | Route. |
| `src/infrastructure/whatsapp/event_read_state.go` | new | `chat.read_state` webhook from `*events.MarkChatAsRead` (skips `FromFullSync`). |
| `src/infrastructure/whatsapp/event_handler.go` | +3 lines | Routes the event. |
| `src/infrastructure/whatsapp/event_label.go` | rename | The async webhook helpers are now `forwardAppStateEvent*` (shared). |
| `docs/openapi.yaml`, `docs/webhook-payload.md` | updated | Path, `UnreadChatResponse`, event docs. |
| `*_test.go` next to each file | new | Validation, index-key selection, patch shape vs whatsmeow, last-message lookup (sqlite in memory), handler, payload + forwarder. |

## How it works

1. `POST /chat/{chat_jid}/unread` with an optional body
   `{"unread": true|false, "jid_form": "pn"|"lid"|"both", "resync": bool}`
   (empty body = unread, `pn`, no forced resync; `?resync=true` works too).
2. The usecase validates, resolves the JID (an `@lid` input is resolved to
   its number), takes the per-device app state write mutex (the one the
   labels API uses), and looks up the newest message stored for the chat
   and this device (`GetMessages` limit 1, by number first, by LID second).
3. It builds `appstate.BuildMarkChatAsRead(jid, read=!unread, ts, key)`
   per index key (`jid_form`) into one `regular_low` patch and sends it
   with `SendAppState`. Without a stored message the range is "now" with
   no key (whatsmeow's own default).
   If the server rejects the write as out of sync (see "Drifted
   `regular_low`" below) the usecase full-syncs the collection and retries
   once; with `resync: true` the full sync runs before the first attempt.
4. The phone applies it, echoes the mutation back, and whatsmeow
   dispatches `*events.MarkChatAsRead`, which `event_read_state.go` turns
   into a `chat.read_state` webhook (`{chat_id, chat_lid?, read,
   last_message_timestamp?, last_message_id?}`; LID resolved to number as
   in `label.association`). Full-sync replays are skipped.

Read receipts are untouched: `POST /message/{id}/read` still sends them
and they still surface as `message.ack`.

## HTTP status map

| Code | When |
| --- | --- |
| 200 | OK. |
| 400 | Malformed JSON, bad `chat_jid`, `jid_form` outside {pn, lid, both}. |
| 401 | Basic auth. |
| 500 | Not logged in, `SendAppState` rejected (patch error, no app state keys), or rejected as out of sync even after the automatic resync + retry. The message is whatsmeow's full error text (it is what tells a drifted collection from anything else). |

## Example

```bash
curl -u "$GOWA_USER:$GOWA_PASS" -X POST \
  "https://gowa.example.com/chat/6289685028129@s.whatsapp.net/unread" \
  -H "X-Device-Id: MARS_IA"
```

```json
{
  "code": "SUCCESS",
  "message": "Chat marked as unread successfully",
  "results": {
    "status": "success",
    "message": "Chat marked as unread successfully",
    "chat_jid": "6289685028129@s.whatsapp.net",
    "unread": true,
    "last_message_timestamp": "2026-11-04T15:03:40Z",
    "last_message_id": "3EB0C767D26A1D8E4F1A"
  }
}
```

`"resynced": true` appears in `results` when the collection was fully
resynced during the call.

## Drifted `regular_low` (old accounts)

On a long-lived account (years of patches, many client versions) the
local copy of the `regular_low` collection can drift from the server's.
The symptom is a 500 on this endpoint with:

```
server returned error updating app state (regular_low): <error code="409" text="conflict"/>
(also, applying patches in the response failed: failed to decode app state regular_low patches:
failed to verify patch v188: mismatching LTHash)
```

whatsmeow handles the 409 by applying the conflict patches the server
sends back and retrying, but those patches fail LTHash verification
against the drifted local state, and the regular incremental sync
(`handleAppStateNotification` -> `FetchAppState` without full sync) only
logs the same error: whatsmeow never full-syncs on its own. A fresh
account never hits this, which is why it only showed up in production.

The repair is a full sync of that one collection
(`client.FetchAppState(ctx, WAPatchRegularLow, fullSync=true, false)`:
reset the local version, download the snapshot). The usecase does it on
its own, under the same per-device app state mutex, when `SendAppState`
returns an error wrapping `whatsmeow.ErrAppStateUpdate` or
`appstate.ErrMismatchingLTHash`, then retries the write once; the resync
is logged with device, collection and previous local version, and the
response carries `resynced: true`. `resync: true` forces it before the
first attempt (manual repair). If the retry fails too, the 500 carries
whatsmeow's full error text, untouched.

Notes:

- A full sync of `regular_low` **emits no events** (gowa leaves
  `EmitAppStateEventsOnFullSync` at false, and `FullSyncAppState` does not
  raise it, unlike `FullSyncLabels`), so no webhook fires for the pins /
  archives / read marks of the snapshot.
- It **never touches labels**: those live in the `regular` collection.
- The same drift could one day hit `regular` and break the label writes
  with the same error. The manual repair already exists there:
  `GET /labels?refresh=true` (`FullSyncLabels`); the automatic retry is
  only wired into this endpoint for now.

## Known limitations / edge cases

- **Index form (verified on a Business account, Nov 2026).** The phone
  honours the phone-number form: `jid_form: pn` (the default) shows the
  marker in the chat list and in the "Unread" filter. The phone itself
  writes `markChatAsRead` under the chat's **LID** (its `read: true` when
  the chat is opened arrives with `chat_lid`), so `lid` / `both` are kept
  for the day the number form stops working.
- **Verified behaviour (same account):** a read receipt sent through
  `POST /message/{id}/read` does **not** clear the marker and emits no
  `chat.read_state`; a message sent through the API **does** clear it on
  the phone, silently (no app state event), so mark the chat unread
  *after* replying; opening the chat on the phone emits
  `chat.read_state` with `read: true` (so does switching away from an open
  chat). The marker also works when the chat's last message is our own
  outgoing one.
- **The marker is per account, not per device**: marking unread from the
  API clears on every linked device when any of them opens the chat.
- **The range is only as good as the chat storage**: a chat with no stored
  message gets a "now" range with no key, which the phone accepts but
  may place the marker differently.
- **`unread: false` is not a read receipt.** It clears the marker locally;
  the sender's ticks do not change. Use `POST /message/{id}/read` for that.
- **Own writes echo back** as `chat.read_state` (one per index key), like
  the label writes do; consumers should treat the event as idempotent.

# Labels API

Adds `GET /labels`, `POST /labels`, `PUT /labels/{label_id}` and
`DELETE /labels/{label_id}`, moves the `label.edit` / `label.association`
webhooks to whatsmeow's typed events, fixes their `timestamp`, and makes
`POST /chat/{chat_jid}/label` write the index form the phone uses (above).

## Why

A client that drives labels (apply/remove them as conversation state,
react to a label a human puts on a chat) needs to **list** them: ids,
names and which chats carry which label. gowa could not: whatsmeow does not
persist label names or associations at all (its store keeps only the
collection version, hash and mutation MACs), and upstream only forwards
label changes as they happen, so after a restart, a missed app state
patch or a reconnection that found the server's patch log pruned, a
consumer has no way to resync. It also could not create labels, so every
label had to be created by hand on the phone.

## What's added (files)

| File | Change | Purpose |
| --- | --- | --- |
| `src/infrastructure/whatsapp/labelstate/state.go` | new | Per-client in-memory cache of `label_edit` / `label_jid` records + the per-client mutex that serializes full syncs with app state writes. `NormalizeEventTime` (timestamp fix). |
| `src/infrastructure/whatsapp/label_sync.go` | new | `FullSyncLabels`: full resync of the `regular` collection with `EmitAppStateEventsOnFullSync` raised only for the call and a temporary event handler that collects the replayed records. |
| `src/infrastructure/whatsapp/lid_lookup.go` | new | `LIDForPN` (phone-number JID -> LID via `client.Store.LIDs`). |
| `src/infrastructure/whatsapp/event_label.go` | rewritten | Webhook forwarder from `*events.LabelEdit` / `*events.LabelAssociationChat` (was the raw `*events.AppState`); skips `FromFullSync`; feeds the cache; same payloads as upstream. |
| `src/infrastructure/whatsapp/event_handler.go` | +8 lines | Routes the two typed events; `handleAppState` keeps only its debug log; drops the cache on `LoggedOut`. |
| `src/domains/label/*.go` | new | DTOs + `ILabelUsecase`. |
| `src/validations/label_validation.go` | new | name required (<= 100 chars), color 0-19, update needs name or color. |
| `src/usecase/label.go` | new | List (sync + normalize), create (`max id + 1`), update/delete (full-record SET). |
| `src/usecase/chat_label.go` | rewritten | LID-aware index keys, shared mutex, optimistic cache update, `chat_lid` in the response. |
| `src/ui/rest/label.go` | new | Fiber (v3) handlers + routes. |
| `src/cmd/root.go`, `src/cmd/rest.go` | +3 lines | Wire the usecase and routes (device-scoped, like the rest). |
| `src/pkg/error/label_error.go` | new | `ErrLabelNotFound` (404). |
| `docs/openapi.yaml`, `docs/webhook-payload.md` | updated | Paths, `Label` / `ChatLabel` / `ListLabelsResponse` / `LabelMutationResponse` schemas, `chat_lid`, timestamp note. |
| `*_test.go` next to each file | new / updated | Cache semantics, collector, flag restore, payload parity with upstream, normalization/dedup, patch shapes, validation, handlers. |

## How it works

### Listing (`GET /labels?refresh=`)

```
GET /labels
   |
   | 1. require login; take the per-client label mutex
   | 2. if cache not synced yet, or refresh=true:
   |      client.EmitAppStateEventsOnFullSync = true   (restored on exit)
   |      client.AddEventHandler(collector)            (removed on exit)
   |      client.FetchAppState(ctx, "regular", fullSync=true, false)
   |        -> deletes the local version, downloads the snapshot,
   |           re-emits every LabelEdit / LabelAssociationChat with
   |           FromFullSync=true, synchronously, before returning
   |      cache.Replace(collected)
   | 3. labels  = every known record (deleted ones with deleted=true)
   |    chats   = associations of non-deleted labels, LID -> PN resolved,
   |             PN/LID forms of one chat merged (latest record wins),
   |             chat_lid kept, sorted
   v
JSON {labels: [...], chats: [...], refreshed}
```

The `regular` collection carries only `label_edit`, `label_jid` and
`label_message` (mute/pin/archive/contacts live in `regular_high`,
`regular_low` and `critical_*`), so the resync touches nothing else. The
main event handler ignores `FromFullSync` events, so the replay does not
re-fire a `label.edit` / `label.association` webhook per historical
record (upstream, which read the raw `events.AppState`, would have).

Between full syncs the cache is kept current by the incremental events
(including the resync whatsmeow runs after our own `SendAppState`), and
each write also applies its own record optimistically. The cache is per
`*whatsmeow.Client`, so a re-login starts empty, and it is dropped on
`LoggedOut`.

### Create / update / delete

`label_edit` is a SET that replaces the whole record. `appstate.BuildLabelEdit`
only fills name/color/deleted, so:

- **create**: full record `{name, color, deleted:false, is_active:true,
  type:CUSTOM, order_index: max+1}`, id = `max(known numeric ids) + 1`
  (deleted labels keep their ids), after a full sync if the cache was
  not synced yet;
- **update**: the last known record with only name/color replaced;
- **delete**: the last known record with `deleted: true`. Associations
  are left alone (WhatsApp does the same); the listing filters them.

### Timestamps

The phone stamps `label_edit` mutations with small values that whatsmeow
reads as milliseconds (`time.UnixMilli`), which is where the
`"timestamp": "1970-01-01T..."` of upstream's `label.edit` webhooks came
from. `labelstate.NormalizeEventTime` treats raw values below 1e12 as
seconds and zero as "now"; the fix applies to the webhook `timestamp` and
to the cache ordering.

### Concurrency

`SendAppState` reads the collection version without whatsmeow's sync lock;
a full sync resets that version to 0 before refetching, so a concurrent
write could be encoded against version 0 and rejected. Every label write
(`/labels*`, `/chat/{jid}/label`) and every full sync take the same
per-client mutex. Incremental syncs driven by server notifications are
not serialized with it (they never were) and do not need to be.

## HTTP status map

| Code | When |
| --- | --- |
| 200 | OK. |
| 400 | Malformed JSON, missing `name`, color outside 0-19, update with neither name nor color. |
| 401 | Basic auth. |
| 404 | `PUT`/`DELETE` on an unknown label id (after a sync if needed). |
| 500 | Not logged in, full sync failed (offline, server error), `SendAppState` rejected (patch error, no app state keys, LTHash mismatch, label limit). |

## Example

```bash
curl -u "$GOWA_USER:$GOWA_PASS" "https://gowa.example.com/labels?refresh=true" \
  -H "X-Device-Id: MARS_IA"
```

```json
{
  "code": "SUCCESS",
  "message": "Success get labels",
  "results": {
    "labels": [
      {"label_id": "4", "name": "ATENCION HUMANA", "color": 4, "deleted": false, "is_active": true, "type": "CUSTOM", "predefined_id": 0, "order_index": 0},
      {"label_id": "5", "name": "MARSIA", "color": 7, "deleted": false, "is_active": true, "type": "CUSTOM", "predefined_id": 0, "order_index": 1}
    ],
    "chats": [
      {"chat_id": "5491112345678@s.whatsapp.net", "chat_lid": "223754944819424@lid", "label_id": "4", "labeled": true}
    ],
    "refreshed": true
  }
}
```

```bash
curl -u "$GOWA_USER:$GOWA_PASS" -X POST "https://gowa.example.com/labels" \
  -H "X-Device-Id: MARS_IA" -H "Content-Type: application/json" \
  -d '{"name": "IRRITADO", "color": 2}'
# PUT  /labels/6   {"name": "CLIENTE IRRITADO"}      rename, keeps color
# PUT  /labels/6   {"color": 9}                      recolor, keeps name
# DELETE /labels/6
```

## Known limitations / edge cases

- **Id allocation is a guess that the server accepted on the dev account.**
  WhatsApp has no "next id" API; `max + 1` matches what the phone did
  (13, 14, 15 on one account, 4, 5 on another). Two clients creating
  labels at the same time on the same account could pick the same id.
- **`order_index` / `predefined_id`** are set the way the phone sets them
  for its own custom labels (next index, no predefined id). If a future
  phone version expects more fields, created labels may render oddly;
  rename/delete are safe because they resend the phone's own record.
- **The label limit (20 per Business account)** is enforced by WhatsApp;
  the API surfaces the rejection as a 500 with the server's error.
- **A full sync is a real download** (a few hundred ms to a few seconds
  on a busy account) and holds the label mutex meanwhile; label writes
  wait for it. Do not call `refresh=true` in a tight loop.
- **Deleted-label associations** are filtered from `chats` but still
  exist in the app state; recreating a label with the same id would
  revive them (ids are never reused by `max + 1`, so this only matters if
  a client picks ids by hand).
- **`labeled: false` rows** are returned in `chats` (they are real records
  in the state); consumers that want "chats with label X" filter on
  `labeled`.
- **Regular (non-Business) accounts** have no labels; the sync returns an
  empty list and writes are ignored by the phone.

## Keeping the fork in sync with upstream

**Do this regularly (about monthly).** WhatsApp periodically rejects old
whatsmeow builds with `Client outdated (405)`. When that happens, existing
sessions can't reconnect and new QR or pairing codes can't be generated.
Upstream bumps whatsmeow every few days, so merging upstream is the fix.
This happened in Oct 2026: the April whatsmeow was rejected, and merging
upstream v9.5.0 fixed it.

```bash
# one-time
git remote add upstream https://github.com/aldinokemal/go-whatsapp-web-multidevice.git

# later
git fetch upstream
git merge upstream/main   # or: git rebase upstream/main
```

All fork-specific code is isolated in new files; the only edits to
upstream files are small and marked with `// Fork-specific:` comments,
so conflicts should be rare.

## Build & run

Locally (requires Go 1.25+):

```bash
cd src
go build -o ../gowa .
./gowa rest
```

Docker:

```bash
docker build -f docker/golang.Dockerfile -t gowa-fork:dev .
docker run --rm -p 3001:3000 \
  -e APP_BASIC_AUTH=user:pass \
  -v gowa_data:/app/storages \
  gowa-fork:dev
```

The binary is a drop-in replacement for the upstream
`aldinokemal2104/go-whatsapp-web-multidevice` image — same env vars,
same `/app/storages` volume, same default port 3000.
