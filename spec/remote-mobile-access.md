# Remote mobile access without Tailscale

## Recommendation

Yes, this can work without Tailscale.

Use two separate surfaces:

1. **Telegram bot for routine remote communication.** The daemon makes outbound
   HTTPS requests using Telegram Bot API long polling. The bot sends alerts and
   supports a deliberately small set of actions: view task status, answer a
   pending multiple-choice question, approve/reject a checkpoint, and pause or
   cancel a task.
2. **Cloudflare Tunnel + Access for the full board when needed.** `cloudflared`
   runs on the Mac and creates an outbound tunnel to a private hostname.
   Cloudflare Access requires the owner's identity before proxying to Ultraflow
   on `127.0.0.1`. No router port-forward, public listener, static IP, or VPN app
   on the phone is required.

Telegram should be the everyday interface; the protected browser board is the
escape hatch for reading diffs, viewing screenshots, typing a nuanced answer,
or doing other work that does not fit safely into bot buttons.

The Mac still needs to be awake, online, and running both Ultraflow and the
relevant connector. No transport can work around a sleeping or offline host.

## What this looks like on the phone

For the first version, create a private Telegram bot with BotFather and open its
chat once from the phone (a bot cannot initiate a conversation with a user).
Ultraflow then behaves like this:

```text
Ultraflow needs your answer
Task: Research remote board access
Question: Which approach should I implement?

[Telegram only] [Remote web too]
[Open board]
```

Tapping an answer calls the same core answer operation as the local board. The
bot edits the message to show who answered and the resulting state. **Open
board** is present only if the protected remote web gateway has also been set
up; otherwise Telegram remains fully useful on its own.

A minimal owner workflow is:

1. Create the bot with [BotFather](https://core.telegram.org/bots/features#botfather),
   store its token in Keychain, and send it `/start` from the intended private
   chat.
2. In the local-only Ultraflow settings screen, enter the token and explicitly
   approve the numeric user/chat ID pair. Each Ultraflow owner can connect their
   own bot; saving replaces the running bot without a daemon restart.
3. Run one long-polling connector as part of the Ultraflow launchd service.
   Telegram permits only one `getUpdates` consumer for a bot token; do not also
   configure a webhook.
4. Test from cellular: receive a synthetic question, answer it once, tap the old
   button again, and verify an unapproved account can do nothing.

This requires no Telegram webhook and no inbound connection to the Mac. Telegram
will, however, receive the text included in notifications, so the notifier must
send concise, redacted summaries rather than code, diffs, paths, or logs.

## Why Telegram first

Telegram is the smallest and safest way to “communicate with the board” from
any network. Long polling is outbound-only, so it requires no inbound firewall
rule, domain, tunnel, or webhook. It also works naturally as a pager when a
mobile browser is closed.

### Safe first version

- Notify on `human_request`, `failed`, and `review`.
- Include task title, project, question/error, and a short redacted summary.
- Turn existing `ask_human.options[]` choices into inline buttons.
- Add `/status` and `/tasks` as read-only summaries.
- Permit `pause` and `cancel` only after a confirmation button.
- Accept messages and callbacks only from one configured numeric Telegram user
  ID and private chat ID. Do not identify the owner by username.
- Bind every button to task ID + request ID + action + expiry, store the real
  action server-side, and put only an opaque random token in `callback_data`.
- Re-read current task/request state before applying an action. Make callbacks
  single-use and idempotent so old messages cannot affect a newer checkpoint.
- Edit the original message after success so its final state is obvious.

The bot token is stored daemon-side in the local settings database and is never
returned by the settings API, logged, placed in task prompts/callback payloads,
or exposed to frontend JavaScript after submission. The database must therefore
remain private. A future Keychain-backed secret store would harden this further. Pairing
should happen locally: the settings screen displays the bot-observed numeric
IDs and the user confirms them on the Mac. Merely sending `/start` must not
authorize a chat.

### Keep out of Telegram initially

- arbitrary new prompts and free-form commands;
- terminal input, file upload, project selection, settings, merge, or retry;
- source files, full diffs, terminal logs, secrets, repository paths, and
  worktree paths;
- group chats and webhook ingress.

Free-form replies can come later, but only as explicit replies to a still-open
bot question, with request correlation, expiry, replay protection, length
limits, and clear handling of simultaneous questions. They must use the same
core lifecycle method as the web UI, not a privileged HTTP request back into
the daemon.

## Full board without a VPN app

Use a named Cloudflare Tunnel behind Cloudflare Access:

```text
iPhone browser
  -> HTTPS board.example.com
  -> Cloudflare Access (owner identity + MFA/passkey, short session)
  -> Cloudflare Tunnel
  -> http://127.0.0.1:7787
```

The tunnel must have a catch-all `http_status:404` rule and only the intended
hostname should route to Ultraflow. Access policy should allow one identity,
require MFA or a passkey, and use a short session. Do not enable an Access
bypass policy for the application.

This is materially safer than opening a router port, but it is still public
edge ingress to a very powerful local application. Ultraflow currently has no
application authentication, and its API can start permission-bypassed agents,
mutate repositories, upload files, and expose an interactive terminal. An
Access or tunnel misconfiguration would therefore have severe consequences.

### Required hardening before calling full-board access supported

- Verify the Access JWT at the Ultraflow boundary, including issuer, audience,
  signature, expiry, and the expected owner identity. Do not trust only a
  forwarded email header.
- Deny requests that arrive without the verified identity even if someone can
  reach the origin by another route.
- Add CSRF protection for mutations and an explicit allowed-origin list for
  HTTP and terminal WebSocket requests.
- Add secure headers, request/body limits, rate limits, and audit logging.
- Default the remote identity to a restricted route set. Do not remotely expose
  `/mcp`, terminal input, folder picking, uploads, settings, or arbitrary task
  creation. Consider keeping merge/revise/retry local too.
- Test Access login, SSE reconnect, WebSocket upgrade, checkpoint answering,
  logout, session expiry, tunnel restart, and Mac sleep/wake over cellular.

The current board cannot simply be placed behind the tunnel and expected to be
fully functional: its terminal WebSocket explicitly accepts only `localhost`,
`127.0.0.1`, and `[::1]` browser origins. Adding the public hostname to that list
would make the terminal reachable remotely, which is precisely the capability
the restricted gateway should withhold. The remote UI therefore needs either no
terminal at all or a deliberately read-only, authenticated log stream.

Operationally, a named tunnel requires a domain managed in a Cloudflare account.
Create the tunnel, route one hostname to `http://127.0.0.1:7787`, put an Access
application and owner-only policy in front of it, then run `cloudflared` under
launchd. Do not use a Quick Tunnel for persistent access: its random public URL
is intended for testing and does not provide the proposed application boundary.

Cloudflare should not be the application's only security boundary. A small
capability-scoped remote gateway is the correct seam: it can expose task reads
and specific lifecycle commands while the existing all-powerful local API
stays loopback-only. The full local board can progressively use that gateway
for its remote mode.

## Alternatives without Tailscale

| Option | Phone setup | Full board | Security/operations | Verdict |
|---|---|---:|---|---|
| Telegram long polling | Telegram only | No | Outbound-only; strict allowlist and narrow capabilities | **Build first** |
| Cloudflare Tunnel + Access | Browser only | Yes | Identity-aware public edge; needs app hardening | **Best no-VPN full-board route** |
| SSH reverse/local tunnel | SSH client | Yes | Strong keys, but awkward reconnects and browser routing on iOS | Power-user fallback |
| Self-hosted reverse proxy | Browser only | Yes | Must operate TLS, auth, DNS, firewall, patches, and monitoring | More work, no product advantage |
| Router port-forward / raw public URL | Browser only | Yes | Current app has no adequate security boundary | **Never** |

An ordinary public tunnel URL from ngrok, Cloudflare Quick Tunnel, or a similar
service is not sufficient. Neither is an obscure URL, HTTP Basic Auth alone, or
a bearer token in a query string.

## Implementation sequence

### Phase 1 — useful remote control with no inbound access

Add an internal notifier/command adapter fed from the same domain events as the
board. Implement Telegram long polling, local owner pairing, an allowlist,
deduplication, retry with backoff, and the notification types above. A Telegram
outage must never block task progress.

Success criterion: from cellular, the owner receives a pending question and can
select an existing option; an unauthorized chat and a stale/replayed button
cannot change state.

### Phase 2 — restricted remote web gateway

Create authenticated, capability-scoped endpoints for task summaries, pending
questions, and safe lifecycle actions. Add Cloudflare Access JWT verification,
CSRF/origin protection, audit logs, and negative authorization tests. Do not
route privileged local endpoints through the tunnel.

Success criterion: the restricted mobile surface works over cellular, while
terminal, MCP, settings, upload, project selection, and arbitrary prompt routes
are unreachable through the remote hostname.

### Phase 3 — optional richer mobile board

Make the existing board responsive and teach it a remote mode backed only by
the restricted gateway. Expand remote capabilities individually after threat
review and tests. Preserve a visibly distinct local-only mode for privileged
operations.

## Decision

The no-Tailscale plan is: **Telegram long polling first; Cloudflare Tunnel +
Access only in front of a hardened, restricted remote gateway for browser
access.** This provides useful communication from anywhere without installing a
VPN on the phone and avoids publishing Ultraflow's current privileged local API.

## Primary references

- Telegram Bot API: [`getUpdates`, webhooks, updates, and callback queries](https://core.telegram.org/bots/api)
- Telegram bot capabilities and the requirement that a user contacts a bot
  first: [Bots: An introduction for developers](https://core.telegram.org/bots)
- Cloudflare: [Create and remotely manage a tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/get-started/create-remote-tunnel/)
- Cloudflare: [Protect an application with Access](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/self-hosted-public-app/)
- Cloudflare: [Validate Access tokens at the origin](https://developers.cloudflare.com/cloudflare-one/identity/authorization-cookie/validating-json/)
- Cloudflare: [Tunnel configuration and catch-all ingress rules](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/configure-tunnels/local-management/configuration-file/)
