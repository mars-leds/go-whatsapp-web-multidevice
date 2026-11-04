# Fork changes

This fork of
[`aldinokemal/go-whatsapp-web-multidevice`](https://github.com/aldinokemal/go-whatsapp-web-multidevice)
adds two REST endpoints: [`media-retry`](#media-retry-endpoint) and
[`chat label`](#chat-label-endpoint). Both follow the same rules: new
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
   | 2. appstate.BuildLabelChat(jid, label_id, labeled)
   |      -> PatchInfo{Type: regular, Index: [label_jid, label_id, jid],
   |                   LabelAssociationAction{Labeled}}
   | 3. client.SendAppState(ctx, patch)   (IQ w:sync:app:state, type=set)
   | 4. phone applies it, fans it out to all devices, whatsmeow resyncs
   |    `regular` and emits events.AppState -> webhook `label.association`
   v
JSON {status, message, chat_jid, label_id, label_name, labeled}
```

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
  chat<->label association. Creating/renaming/deleting labels
  (`appstate.BuildLabelEdit`) is deliberately not exposed, so the
  `label_name` in the body cannot create a label; it is echoed back only.
  Get the ids from the `label.edit` webhook events (fired on connect
  when the `regular` app state is synced) or from the phone.
- **Regular (non-Business) accounts** have no labels; the server may
  accept the patch and the phone will simply ignore it.
- **No local read-back.** gowa does not persist label associations, so
  there is no `GET`; rely on the `label.association` webhook event that
  follows a successful patch as the confirmation.
- **JID form.** The association is indexed by the JID the caller passes
  (after LID -> phone-number resolution, like `/pin`). Chats that the
  phone stores by LID may show the label under the LID key in webhook
  events (`chat_lid`); labeling by `@lid` directly is untested.
- **App state keys.** `SendAppState` reuses the primary device's app
  state sync key (whatsmeow TODO). If the session has no synced keys yet
  (fresh pairing, before the first `regular` sync) it fails with
  `no app state keys found`; retry after the initial sync.

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
