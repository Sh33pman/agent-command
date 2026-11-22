# Remote sessions

Remote sessions let one Agent Command use sessions that run on another. You mark a session on one Mac as Remote, and another Mac opens it in its sidebar, types in it, answers its questions and gets its notifications, as if it ran there. The session keeps running on the Mac that started it.

This is separate from **Agent Remote Control** (Settings → General, `acmd agent-remote-control`), which starts Claude and Antigravity sessions with `--remote-control` so you can use them from claude.ai, the Claude app or Antigravity. Remote sessions are between two Agent Commands, and don't involve Anthropic or Google.

The app steps below are for the macOS app. On Linux, the daemon and `acmd` do everything the CLI steps show, but the Linux app doesn't have the Remote screens yet.

- [Concepts](#concepts)
- [Quick start on a local network](#quick-start-on-a-local-network)
- [Over the internet with Cloudflare Tunnel](#over-the-internet-with-cloudflare-tunnel)
- [Using your own certs](#using-your-own-certs)
- [Ports](#ports)
- [What clients can do, and security](#what-clients-can-do-and-security)
- [CLI reference](#cli-reference)
- [Troubleshooting](#troubleshooting)
- [Configuration reference](#configuration-reference)

## Concepts

- **Host:** the Mac where the sessions run.
- **Client:** the Mac that uses them. One Mac can be both: a host for some machines, and a client of others.
- **Hosting:** the host's listener for clients. It's off by default. When it's on, it listens on `localhost:7433` unless you choose otherwise.
- **Remote session:** a session on the host that clients may use. Each session has its own Remote flag, and it's off unless you turn it on. Clients never see the host's other sessions.
- **Identity and fingerprints:** each Mac has a cert and key. acmd makes them the first time they're needed, or you use your own. A Mac is known by its cert's **fingerprint**: `SHA256:` followed by 43 characters, the SHA-256 of the cert in base64. `acmd remote identity` shows it, and so does Settings → Remote → Identity. There's no certificate authority: each side pins the other's fingerprint.
- **Authorized clients:** on the host, the clients allowed in, by fingerprint. acmd keeps them in its database; ones you list in config.toml (`[[remote.authorized_clients]]`) count too.
- **Pending requests:** a client the host doesn't know is refused, and its request waits on the host for you to approve or deny. Nothing is ever approved by itself. A request is kept for 24 hours after the client last tried.
- **Known hosts:** on the client, the hosts it connects to, each with a name, an address (`host:port`) and the fingerprint it expects. acmd keeps them in its database; ones you list in config.toml (`[[remote.hosts]]`) count too. A session you open from a host is **added** here: it's a row in the sidebar with a "↗ host" badge, and it still runs on the host.

The apps and the CLI never touch the network. They talk to their own Mac's background service (the daemon), and the two daemons talk to each other:

```
   Client Mac                                         Host Mac
  ┌──────────────────────┐                          ┌──────────────────────┐
  │ app, acmd            │                          │ app, acmd            │
  │      │ unix socket   │                          │      │ unix socket   │
  │      ▼               │   mutual TLS 1.3         │      ▼               │
  │ acmd daemon ─────────┼──────────────────────────┼─▶ Hosting (:7433)    │
  │   client identity    │  each side pins the      │   host identity      │
  │   known hosts        │  other's fingerprint     │   authorized clients │
  └──────────────────────┘                          │   Remote sessions    │
                                                    └──────────────────────┘
```

Both sides show a cert (mutual TLS). The client checks that the host's cert has the fingerprint it pinned, and the host checks that the client's fingerprint is authorized. Then the two daemons check they speak the same protocol version, and every call and terminal stream runs inside that one encrypted connection.

## Quick start on a local network

Two Macs on the same network: the **host** runs the sessions, the **client** uses them. Each step has the app's way and the command line's way. They do the same thing.

### 1. On the host: turn Hosting on

Clients on other machines can only reach Hosting when it listens on all interfaces.

- **App:** Settings → Remote → Hosting. Set **Listen on** to **All interfaces**, then turn **Hosting** on. The **Status** line shows the address and port it's listening on.
- **CLI:**

  ```sh
  acmd config set remote.host.listen all
  acmd remote hosting on
  acmd remote status
  ```

Note the port (7433 unless it was taken: see [Ports](#ports)) and the host's name or IP address on the network, such as `studio-mac.local`. Then get the host's fingerprint, to give to the client:

- **App:** Settings → Remote → Identity → **This Mac's fingerprint**, with Copy.
- **CLI:** `acmd remote identity`.

### 2. On the client: add the host

- **App:** Settings → Remote → Known hosts → **Add Host…**. Give it a name (such as `studio`), its address (`studio-mac.local:7433`), and paste its fingerprint, or choose the host's cert file.
- **CLI:**

  ```sh
  acmd remote add studio studio-mac.local:7433 --fingerprint SHA256:…
  ```

  The output shows this Mac's own fingerprint, for the host to check in the next step.

The client then tries to connect. The host doesn't know it yet, so the host keeps its request, and the client shows **Waiting for approval**.

### 3. On the host: approve the request

- **App:** Settings → Remote → Pending requests. A new request also puts a badge on the Remote section and sends a notification with **Approve**. Check that the fingerprint matches the one the client showed, then **Approve…** (you can give it a name).
- **CLI:**

  ```sh
  acmd remote requests                       # shows each request's id and fingerprint
  acmd remote requests approve <id> --name laptop
  ```

If you already have the client's fingerprint or cert file, you can authorize it before it ever connects: **Add Client…** under Authorized clients, or `acmd remote authorize <fingerprint|cert-file> --name laptop`.

### 4. Mark a session Remote, and open it on the client

On the host:

- **App:** **Turn Remote On** in a session's menu in the sidebar, or **Turn Remote on** in the command palette. A new session from ⇧⌘N or ⇧⌘T has a **Remote** checkbox in its folder panel. A Remote session shows "shared", and how many clients are attached.
- **CLI:** `acmd remote on <session>` for one that's running, or `acmd new --remote` for a new one. `acmd remote sessions` lists them.

On the client:

- **App:** nothing to do: once a host is known and connected, its sessions show in the sidebar, after this Mac's, **arranged as the host has them**: its groups (named "API · studio"), its sets, and its Default (named after the host). **Remote hosts…** in the command palette lists a host's sessions to open one, and starts new ones there (n). The browser's keys: ↑ ↓ move, ← → go between hosts and sessions, return shows a session, r retries a host now, n starts a new session on it, and c copies your fingerprint.
- **CLI:**

  ```sh
  acmd remote browse studio          # its sessions, numbered
  acmd remote attach studio 1        # attach this terminal; Ctrl+] detaches
  ```

A host's sessions show with "↗ studio". While the host can't be reached, their rows are greyed and reconnect by themselves. A change you make to one (renaming, archiving, moving it between the host's groups) is made on the host, so every client of the host sees the same tree. A host's sessions stay in its own groups and sets: they can't be moved into this Mac's.

Once a known host exists, the sidebar has a filter next to "Sessions": All, Local (the sessions that run on this Mac, Remote ones included), Remote (every host's sessions), or one host's sessions.

To start a new session on the host from the client: ⇧⌘N, then choose the host on its **Where** line, which lists This Mac and every known host (tab changes it), and a folder from the host's own folders. The palette's **Session on studio…**, and **New Session on studio…** in the Remote Hosts browser, open ⇧⌘N with the host already chosen. Or `acmd new --on studio --dir ~/code/app`.

## Over the internet with Cloudflare Tunnel

To reach a host that isn't on your network, don't open a port on your router. A [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) carries the connection instead: `cloudflared` on the host makes an outgoing connection to Cloudflare, and `cloudflared` on the client reaches it through a hostname on your domain. Hosting stays on **Localhost**, so nothing listens on the network.

You need a domain on Cloudflare, and `cloudflared` on both Macs (`brew install cloudflared`).

acmd's own mutual TLS still runs end to end, inside the tunnel. Cloudflare passes the bytes along, but only sees them encrypted, and can't pretend to be either Mac: the client still checks the host's fingerprint, and the host still checks the client's.

### On the host

Keep Hosting on Localhost (the default), and check the port it's listening on with `acmd remote status`.

1. Log in, and create a named tunnel. `create` prints the tunnel's UUID and where it saved the tunnel's credentials file:

   ```sh
   cloudflared tunnel login
   cloudflared tunnel create acmd
   ```

2. Write a config file for it, such as `~/.cloudflared/acmd.yml`, with the UUID and credentials file from the last step and the port from `acmd remote status`:

   ```yaml
   tunnel: <tunnel UUID>
   credentials-file: /Users/you/.cloudflared/<tunnel UUID>.json

   ingress:
     - hostname: acmd.example.com
       service: tcp://localhost:7433
     - service: http_status:404
   ```

   The last rule is the catch-all every config needs. `cloudflared tunnel --config ~/.cloudflared/acmd.yml ingress validate` checks the file.

3. Point the hostname at the tunnel, and run it:

   ```sh
   cloudflared tunnel route dns acmd acmd.example.com
   cloudflared tunnel --config ~/.cloudflared/acmd.yml run acmd
   ```

   The tunnel must keep running for clients to connect. Cloudflare's docs explain [running it as a service](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/as-a-service/).

**Use a port of your own for the tunnel.** With the default settings, when port 7433 is taken, Hosting falls back to another port, and the ingress rule would then point at the wrong one. Choose a port and turn the fallback off (in the app, saving a port other than 7433 does both), so Hosting stops with an error instead of moving:

```sh
acmd config set remote.host.port 7433
acmd config set remote.host.port_fallback off
```

Either way, `acmd remote status` shows the port Hosting really listens on, and that's the one the ingress rule needs.

**Add a Cloudflare Access policy.** Protect `acmd.example.com` with a [Cloudflare Access](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/non-http/cloudflared-authentication/arbitrary-tcp/) self-hosted application and a policy that lets only you in. Then only people who sign in can reach Hosting at all, before acmd's own checks. `cloudflared access tcp` on the client opens a browser to sign in.

### On the client

1. Run `cloudflared access tcp`, which listens on a local port and carries what it gets through the tunnel. Any free port works. Leave it running:

   ```sh
   cloudflared access tcp --hostname acmd.example.com --url localhost:17433
   ```

2. Add the host at that local address, with the host's fingerprint:

   ```sh
   acmd remote add studio localhost:17433 --fingerprint SHA256:…
   ```

   Or in the app: Settings → Remote → Known hosts → **Add Host…**, with the address `localhost:17433`.

3. Approve the request on the host, as in [step 3](#3-on-the-host-approve-the-request) of the quick start. From then on, browse and open the host's sessions as on a local network.

The host shows the client's requests and connections as coming from `127.0.0.1`, since they arrive through `cloudflared`. Check a request by its fingerprint, not its address.

## Using your own certs

acmd's own identity is fine for most uses. To use certs of your own, for example to manage them with your other keys, make a cert and key in PEM files. acmd accepts Ed25519, ECDSA P-256 and RSA keys of 2048 bits or more, in PKCS #8, PKCS #1 or SEC 1 form. The key must not have a passphrase, since the daemon starts unattended, and others must not be able to read it.

Ed25519, with OpenSSL 3 (`brew install openssl`; the `openssl` that comes with macOS is LibreSSL, which can't make Ed25519 keys):

```sh
openssl req -x509 -newkey ed25519 -nodes -days 3650 \
  -subj "/CN=studio-mac" -keyout host-key.pem -out host.pem
chmod 600 host-key.pem
```

ECDSA P-256, which the `openssl` that comes with macOS can make too:

```sh
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
  -pkeyopt ec_param_enc:named_curve -nodes -days 3650 \
  -subj "/CN=laptop" -keyout client-key.pem -out client.pem
chmod 600 client-key.pem
```

The CN is the name the other machine sees first, such as in a pending request. Keep `-pkeyopt ec_param_enc:named_curve`: without it, LibreSSL writes the curve out in full, and acmd refuses the cert.

The host role and the client role are set separately, so a Mac can show one cert as a host and another as a client:

- **App:** Settings → Remote → Identity. **Choose your own…** next to Hosting cert or Client cert asks for the cert file, then its key. **Use acmd's identity** goes back.
- **CLI:**

  ```sh
  acmd remote cert host ~/certs/host.pem ~/certs/host-key.pem
  acmd remote cert client ~/certs/client.pem ~/certs/client-key.pem
  acmd remote cert client --default     # acmd's own identity again
  ```

- **config.toml:** `cert` and `key` under `[remote.host]` or `[remote.client]`. Set both or neither.

Both ways check that the cert and key belong together, and that the key isn't readable by others, before anything is saved. `acmd doctor` checks them again.

To get the fingerprint to share, run `acmd remote identity` (it shows each role's fingerprint when they differ), or copy it in Settings → Remote → Identity. You can also give the other side the cert file itself: `acmd remote add --cert` and `acmd remote authorize <cert-file>` read the fingerprint from it. To work it out from the cert with OpenSSL:

```sh
openssl x509 -in host.pem -outform der | openssl dgst -sha256 -binary | base64 | tr -d '='
```

and put `SHA256:` in front.

A new cert has a new fingerprint. After you change one, clients must pin the host's new fingerprint (`acmd remote edit <host> --fingerprint …`), and hosts must authorize the client's new one.

## Ports

- Hosting's port is **7433** unless you choose another (`remote.host.port`, or **Port** in Settings → Remote → Hosting).
- **Use another port if this one is taken** (`port_fallback`, on by default): when the port is in use, Hosting listens on another free one instead of failing. It tries the port it fell back to last time first, so clients usually find it in the same place. `acmd remote status` and the app's status line show the port in use, as "listening on 127.0.0.1:59427 (port 7433 was taken)". Clients pin the address, so a moved port means editing the host's address on each client (`acmd remote edit <host> --address …`).
- **A port of your own fails when it's taken:** with the fallback off, Hosting doesn't start, and its status names the port: "port 7433 is in use, so Hosting can't start: free it, choose another port, or turn on port_fallback". `acmd remote hosting on` exits with code 8. In the app, saving a port other than 7433 turns the fallback off; the checkbox turns it back on. After you free the port, turn Hosting off and on again.
- The port the fallback picked is remembered in acmd's database (`~/.local/state/agent-command/state.db`).

## What clients can do, and security

**What a client gets.** In the app and with `acmd remote`, an authorized client sees only the host's Remote sessions. Every other session is hidden there, and acting on one fails as if it didn't exist. **With `acmd --host`, it gets the whole host** (Direct Connect): every session, group and scheduled job, as the host has them, and it can do what you can there, except admin ops (purging, `init`, Hosting and pairing, settings that run code) and what belongs to the host's own machine (speech, waking, hooks). Approve only your own machines, and revoke a pairing you no longer trust (`acmd remote clients`, then `acmd remote revoke`).

**What each client may do** is its scope, shown by `acmd remote clients` and changed with `acmd remote scope <client> read|act|admin` or in its menu under Settings → Remote: `read` lists and reads everything, follows changes and reads screens; `act` also starts, attaches to and types into sessions, answers the Inbox, renames, archives and moves (what a newly approved client gets); `admin` also changes settings and Hosting, approves clients, purges, and sets what runs. Clients paired before scopes existed, and ones `config.toml` lists without a `scope`, are `admin`, so they keep what they could do. A call a client's scope doesn't allow fails with `permission_denied`, naming the command that raises it. But a client has full control of the Remote sessions it sees: it can type in them (including answering permission prompts), read their screens and Inbox messages, rename them, finish and end them. It can also start new sessions on the host, including terminals, in any folder your account can reach, and list and make folders to do it. Each new one is Remote.

So authorizing a client is like giving it your account on the host. Only authorize machines you control, and check the fingerprint in a request matches the one the client shows before you approve it.

**Why localhost is the default.** Hosting is off until you turn it on, and then it listens on `127.0.0.1` only, so nothing on the network can reach it until you choose **All interfaces** or set up a tunnel. Even when it's reachable, a client must show a cert whose fingerprint you authorized; anyone else only leaves a request.

**Revoking a client.** Settings → Remote → Authorized clients → **Revoke…**, or `acmd remote revoke <name|fingerprint>`. Its connections are dropped at once, and it can't get back in. If it tries again, it leaves a new request, which you can deny. **Disconnect** (`acmd remote disconnect`) only drops its connections: it stays authorized and can reconnect.

**Regenerating the identity.** If a Mac's key may have leaked, make a new identity: Settings → Remote → Identity → **Regenerate…**, or `acmd remote identity --regenerate`. Every host and client that trusts this Mac must then trust its new fingerprint. A cert of your own isn't changed by this: replace it with `acmd remote cert`.

**Logs.** The daemon log, `~/.local/state/agent-command/daemon.log`, has a line, starting `remote:`, for each Hosting start and stop, each client that connects or disconnects (with its name, fingerprint and address), each unknown client's request, and each refused handshake. On a client, lines start `remote host <name>:`.

**Turning a session local** (`acmd remote off`, or **Turn Remote Off**) drops the clients attached to it at once. Turning Hosting off drops every connection.

## Writing a client

What a client can call is in the op catalog, `internal/catalog/testdata/catalog.schema.json` in the source: each op as JSON Schema, with its scope.

- **Errors.** Every failed call answers with a `code` and a message, on the local socket and over Remote alike: `not_found`, `invalid`, `bad_request`, `conflict`, `permission_denied`, `forbidden`, `not_running`, `not_supported`, `host_unreachable`, `unknown_op`, `proto_unsupported`, `exists`, `not_folder`, or `internal` for anything else. The catalog's `errors` lists them with what each means. Decide from the code; the message is for people, and can change.
- **Retries.** The ops that create something (`new`, `remote.new`, `job.save`, `job.run`, and the `group.create` mutation) are marked `retryable` in the catalog. Send one with a `request_id` (any unique string, such as a UUID) beside `op`, and a retry with the same id within 10 minutes gets the first result back instead of starting a second session, even if the daemon restarted in between. A call that failed isn't kept: retrying it tries again.

## CLI reference

Every command is under `acmd remote`, except `acmd new --remote` and `acmd new --on`. `acmd remote <command> --help` has the details.

On the host:

| Command | What it does |
|---|---|
| `hosting on\|off [--json]` | Turns Hosting on or off. Off drops every connection. |
| `status [--json]` | Hosting: on or off, its address and port, its fingerprint, and its clients. |
| `on <session>...` / `off <session>...` | Makes sessions Remote, or local again (dropping their clients). |
| `sessions [--json]` | The Remote sessions, whether Hosting is on, and how many clients are attached to each. |
| `requests [--json]` | Clients waiting for approval. |
| `requests approve <id> [--name N]` / `requests deny <id>` | Authorizes a request's client, or drops the request. |
| `authorize <cert-file\|fingerprint> [--name N]` | Lets a client in, before or after it asks. |
| `clients [--json]` | The authorized clients, and who's connected. |
| `rename <name\|fingerprint> <new-name>` | Renames an authorized client. It stays connected. |
| `revoke <name\|fingerprint>` | Stops a client connecting, and drops it now. |
| `disconnect <name\|fingerprint\|connection>` | Drops a client's connections. It stays authorized. |
| `acmd new --remote` / `--no-remote` | Starts a session as Remote or local. Without either, `new_sessions_remote` decides. |

On the client:

| Command | What it does |
|---|---|
| `add <name> <host:port> (--fingerprint SHA256:…\|--cert file)` | Adds a known host, and shows this Mac's fingerprint for the host to authorize. |
| `edit <name> [--name N] [--address host:port] [--fingerprint …\|--cert file]` | Changes a known host. Its sessions here are named by its new name. |
| `remove <name> [-y]` | Forgets a host: its sessions go from here. They keep running on the host. |
| `list [--json]` | The known hosts, each one's state, and how many of its sessions are here. |
| `retry <name>` | Connects now, waits up to 15 seconds, and exits with the code for how it went. |
| `browse <host> [--json]` | The host's sessions, numbered, the most recently active first. |
| `attach <host> <#\|id\|name>` (or `open`) | Attaches this terminal to one of them. Ctrl+] detaches. |
| `acmd new --on <host> [--dir D] [--harness H] [--name N] [--prompt P] [--group G]` | Starts a session on the host (in its home, without `--dir`): it shows here in the host's tree. |

On either:

| Command | What it does |
|---|---|
| `identity [--json] [--regenerate [-y]]` | This Mac's fingerprints and certs, for each role. `--regenerate` makes a new identity. |
| `cert host\|client (<cert-file> <key-file> \| --default)` | Uses your own cert and key for a role, or acmd's identity again. |

A session on a host can be named by its number in `browse`, its id on the host, its name, or a unique start of its name or id. `acmd list` shows where each session runs: `↗ studio` for one added from a host.

**Exit codes**, when a Remote host can't be used (also in `acmd --help`, with the others, and in the README):

| Code | Meaning |
|---|---|
| 3 | Unreachable: no answer from its address, or the connection dropped. |
| 4 | Waiting for approval: the host's user hasn't approved this machine yet. |
| 5 | Unauthorized: the host refused this machine. |
| 6 | Fingerprint mismatch: the host isn't showing the fingerprint you pinned. |
| 7 | Version mismatch: the two acmds speak different protocol versions. |
| 8 | Port taken: Hosting is on, but can't listen on its port (`acmd remote hosting on`). |
| 1 | Any other failure. |

## Troubleshooting

`acmd remote list` on a client, and `acmd remote status` on a host, show where things stand. `acmd doctor` checks the identities, warns about Remote sessions while Hosting is off, and about hosts that need someone to act.

**Fingerprint mismatch** (exit 6). The host showed a different fingerprint from the one you pinned, and the client won't connect, or retry, until you act. If you know why it changed (the host regenerated its identity, or switched to its own cert), check the new fingerprint with the host's `acmd remote identity`, then pin it:

```sh
acmd remote edit studio --fingerprint SHA256:…
```

If you don't know why, don't connect: something else may be answering at that address.

**Waiting for approval** (exit 4). The host has this Mac's request, but no one has approved it. On the host, approve it in Settings → Remote → Pending requests or with `acmd remote requests approve <id>`. A client the host revoked, or whose request was denied, waits for approval again when it next tries. **Unauthorized** (exit 5) means the host turned this Mac away without keeping a request, which happens when too many unknown clients asked at once; authorize it on the host with `acmd remote authorize <fingerprint>`.

**Version mismatch** (exit 7). The two Macs run acmd versions that speak different protocols. Update acmd on both (`brew upgrade --cask agent-command`). A host only says this once it has authorized the client: until then, a client of another version just waits for approval.

**Port taken** (exit 8). Something else is listening on Hosting's port. Free it, choose another port, or turn on **Use another port if this one is taken** (`acmd config set remote.host.port_fallback on`), then turn Hosting off and on. See [Ports](#ports).

**A session is Remote, but Hosting is off.** No client can reach it. The app's status bar warns about it, with **Turn On Hosting**, and so does `acmd doctor`. Turn Hosting on, or make the session local with `acmd remote off <session>`.

**The host is unreachable** (exit 3). The client keeps trying, waiting longer each time up to 30 seconds; `acmd remote retry <host>` tries now. Check that:

- the host is awake, and its daemon is running (`acmd remote status` there),
- Hosting is on, and listening on **All interfaces** for a client on another machine,
- the address and port match the host's status, which shows a fallback port if 7433 was taken,
- the host's firewall lets the connection in.

**Through a tunnel.** If the client says unreachable, check that `cloudflared access tcp` is still running on the client, and at the address the host was added with (`localhost:17433`). On the host, check that the tunnel is running and that its ingress rule points at the port `acmd remote status` shows. With an Access policy, `cloudflared access tcp` needs you to sign in in a browser; its output says if it's waiting. A fingerprint mismatch through a tunnel means the address leads somewhere other than the host you pinned.

## Configuration reference

The settings are in `~/.config/agent-command/config.toml`. The daemon is the only thing that writes it, and keeps your comments: the app, `acmd config set` and a client connected over Remote all change it through the daemon. Editing it by hand works too, and applies straight away. A hand edit with a mistake leaves the last good settings in effect: the app and `acmd config show` name the line, and changes wait until it's fixed.

Authorized clients and known hosts that you add in the app or with `acmd` are kept in acmd's database (`state.db`), not in config.toml. Ones you list in config.toml still count, read-only: the app marks them "Set in config.toml", and you remove them by editing the file. Upgrading moves the entries config.toml had into the database once, after copying the file to `config.toml.before-state-db`.

A client connected over Remote can read and change a host's settings (the app's **Settings for** picker, on the Claude, Antigravity and Notifications pages), but not its device settings: the editor and speech belong to whoever sits at the host.

```toml
[remote]
new_sessions_remote = false

[remote.host]
enabled = false
listen = "localhost"
port = 7433
port_fallback = true
cert = ""
key = ""

[remote.client]
cert = ""
key = ""

[[remote.authorized_clients]]
name = "laptop"
fingerprint = "SHA256:…"

[[remote.hosts]]
name = "studio"
address = "studio-mac.local:7433"
fingerprint = "SHA256:…"   # or cert = "/path/to/host.pem"
```

| Key | Default | What it is |
|---|---|---|
| `remote.new_sessions_remote` | `false` | Whether a new session is Remote, for ⌘N and `acmd new` (without `--remote` or `--no-remote`), and where ⇧⌘N's and ⇧⌘T's Remote checkbox starts. Set with **Start new sessions as Remote** or `acmd config set`. |
| `remote.host.enabled` | `false` | Hosting on or off. `acmd remote hosting on\|off`. |
| `remote.host.listen` | `"localhost"` | Where Hosting listens: `"localhost"` (127.0.0.1) or `"all"` (every interface). |
| `remote.host.port` | `7433` | Hosting's port, from 1 to 65535. |
| `remote.host.port_fallback` | `true` | When the port is taken, listen on another free one instead of failing. |
| `remote.host.cert`, `remote.host.key` | `""` | Your own cert and key for Hosting. Empty is acmd's identity. Set both or neither, with `acmd remote cert host`. |
| `remote.client.cert`, `remote.client.key` | `""` | Your own cert and key for connecting to hosts. Empty is acmd's identity. `acmd remote cert client`. |
| `[[remote.authorized_clients]]` | none | On a host, clients allowed in that you list yourself: `name`, and `fingerprint` (required). Read-only to acmd; the ones you approve, `authorize`, `rename` and `revoke` are kept in its database. |
| `[[remote.hosts]]` | none | On a client, known hosts you list yourself: `name` (unique), `address` (`host:port`), and `fingerprint` or `cert` (a path to the host's cert, to pin its fingerprint). Read-only to acmd; the ones you `add`, `edit` and `remove` are kept in its database. |

`acmd config show` lists every setting, and `acmd config set` changes one or more at once (`acmd config set remote.host.listen all remote.host.port 7500`).

Other files:

- `~/.config/agent-command/identity/`: acmd's own identity, `cert.pem` and `key.pem` (the key `0600`, the folder `0700`). acmd makes it the first time it's needed: when Hosting starts, a known host is added, or `acmd remote identity` runs.
- `~/.local/state/agent-command/state.db`: the authorized clients and known hosts added through acmd, Hosting's pending requests, and the port the fallback picked.
