# Fork changes: `media-retry` endpoint

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
