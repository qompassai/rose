# Integration Tests

This directory contains integration tests to exercise Rose end-to-end to verify behavior

## Neovim transport

`TestNeovimTransport` is an opt-in, model-free test of the real Neovim/curl adapter
against Rose's HTTP and hybrid mTLS transport. It is skipped unless explicit
checkout and executable paths are supplied:

```sh
ROSE_NVIM_ROOT=/absolute/path/to/rose.nvim NVIM=/absolute/path/to/nvim \
  go test -count=1 -v ./integration -run '^TestNeovimTransport$'
```

See [Neovim setup](../docs/neovim.md) for the required TLS capabilities. This test
does not download models or contact external services.

## Compiled server transport

`TestRoseBinaryTransport` starts a separately built Rose executable with temporary
storage and certificates. It checks local HTTP, hybrid mTLS, version metadata,
disabled debug routes and graceful shutdown:

```sh
go build -o /absolute/path/to/rose-test .
ROSE_BINARY=/absolute/path/to/rose-test \
  go test -count=1 -v ./integration -run '^TestRoseBinaryTransport$'
```

It is skipped unless `ROSE_BINARY` is supplied. It does not run model inference
or exercise accelerator images.

## Model inference

By default, the model inference tests are disabled. To run them, pass the
integration tag: `go test -tags=integration ./...`. These tests may download models;
run them only when that network and resource use is intended.


The integration tests have 2 modes of operating.

1. By default, they will start the server on a random port, run the tests, and then shutdown the server.
2. If `ROSE_TEST_EXISTING` is set to a non-empty string, the tests will run against an existing running server, which can be remote
