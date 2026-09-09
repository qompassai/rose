# Rose and rose.nvim

Rose is the default backend for [rose.nvim](https://github.com/qompassai/rose.nvim).
The integration keeps the compatible `/api/chat` JSON protocol; it does not require
a separate plugin API or a background credential read during editor setup.

## Same-machine setup

Build Rose using [the development instructions](development.md), then start it:

```sh
rose serve
```

The default is `http://127.0.0.1:11434`. Choose a model already installed in Rose
and configure the editor:

```lua
require("rose").setup({
  rose = {
    model = "your-installed-model",
  },
})
```

An empty `setup({})` selects Rose with the plugin's default model settings.
Setup does not start Rose, install a model or send a request. Use the plugin's
health check to inspect configuration; an actual chat requires a running backend
and a compatible installed model.

Use a literal loopback address for HTTP, not a DNS hostname. Remote plaintext is
rejected even when the editor's `allow_remote` is true. Loopback HTTP is not
encrypted or authenticated; other processes on the machine remain in the trust
boundary.

## Remote setup

Provision a private client-authentication CA, a server certificate with a matching
DNS/IP subject alternative name and `serverAuth` usage, and a separate client
certificate with `clientAuth` usage. Restrict access to all private keys and keep
the CA signing key off the server and editor machines.

On the server, use PEM files and an explicit HTTPS endpoint:

```sh
ROSE_HOST=https://0.0.0.0:11434 \
ROSE_TLS_CERT=/etc/rose/server.pem \
ROSE_TLS_KEY=/etc/rose/server.key \
ROSE_TLS_CLIENT_CA=/etc/rose/client-ca.pem \
  rose serve
```

The server key must be a regular, non-symlink file, with no group/other permissions
on Unix (for example mode `0600`). Each PEM file is limited to 1 MiB. A server
certificate may include its intermediate chain after the leaf certificate.

On the editor machine:

```lua
require("rose").setup({
  providers = { provider = "rose" },
  rose = {
    base_url = "https://rose.example.net:11434",
    model = "your-installed-model",
    allow_remote = true,
    transport = "curl",
    tls = {
      ca_file = "/home/me/.config/rose/server-ca.pem",
      cert_file = "/home/me/.config/rose/client.pem",
      key_file = "/home/me/.config/rose/client.key",
    },
  },
})
```

Replace the example paths and hostname. `ca_file` is optional when the system
trust store already trusts the server; the client certificate and key are required.
These are file paths, not inline secrets. The HTTP adapter reads them only through
curl at request time. Editor TLS paths must be absolute POSIX paths without
colons or control characters, at most 4096 bytes each. Windows drive paths are
not supported by this adapter.

The curl TLS backend must support `X25519MLKEM768`; merely recognizing HTTPS is not
enough. Rose requires TLS 1.3 and this hybrid group and does not retry with classical
TLS when negotiation fails. The editor uses curl without curlrc, proxies, redirects
or insecure certificate bypasses. Native `vim.net` transport is rejected for
Rose, including local HTTP.

## Rose CLI as a remote client

In a separate client shell, use client identity files, not the server's key:

```sh
ROSE_HOST=https://rose.example.net:11434 \
ROSE_TLS_CA=/home/me/.config/rose/server-ca.pem \
ROSE_TLS_CERT=/home/me/.config/rose/client.pem \
ROSE_TLS_KEY=/home/me/.config/rose/client.key \
  rose list
```

`ROSE_TLS_CA` replaces the Go client's system server roots when supplied.
`ROSE_TLS_CLIENT_CA` is the server's independent trust store for clients.
The environment client disables proxies and redirects. Code that passes its own
HTTP client to `api.NewClient` owns that client's security and timeout policy.

## Ollama compatibility

Select Ollama explicitly to keep using it:

```lua
require("rose").setup({
  providers = { provider = "ollama" },
  ollama = {
    base_url = "http://127.0.0.1:11434",
    model = "your-installed-model",
  },
})
```

For migration compatibility, an old configuration containing only an `ollama`
section still selects Ollama. Supplying a `rose` section or explicitly selecting a
provider makes the intended choice unambiguous; explicit provider selection wins.
Rose's mandatory hybrid TLS policy does not silently relabel a remote Ollama
connection as secure Rose.

## Containers

Container images now default to `127.0.0.1:11434` inside the container. Publishing
a host port does not make that loopback listener reachable from outside the
container. For remote access, explicitly set the HTTPS environment above and
mount the server certificate, protected key and client CA read-only. Do not change
the listener to wildcard plaintext to work around the restriction.

The Go build stage is pinned to Go 1.27.1 and checked against `go.mod`. Accelerator
images and platform builds still require separate build/runtime validation.

## Reproducible transport check

With both checkouts and the required Neovim/curl versions installed, run from Rose:

```sh
ROSE_NVIM_ROOT=/absolute/path/to/rose.nvim NVIM=/absolute/path/to/nvim \
  go test -count=1 -v ./integration -run '^TestNeovimTransport$'
```

The fixture generates temporary test certificates, sends synthetic messages via
the real Neovim adapter, and checks the negotiated TLS version, hybrid group and
verified client chain. It also tests local/default routing, legacy Ollama routing,
an untrusted server CA and a classical-only TLS server. It downloads no model and
does not verify inference quality or accelerator support.

To check the compiled server's endpoint metadata, TLS negotiation, disabled debug
routes and graceful shutdown without downloading a model:

```sh
go build -o /absolute/path/to/rose-test .
ROSE_BINARY=/absolute/path/to/rose-test \
  go test -count=1 -v ./integration -run '^TestRoseBinaryTransport$'
```

This second fixture starts the real binary on temporary loopback ports with
isolated storage. These transport checks do not replace the broader repository
test suite or platform-specific release validation.
