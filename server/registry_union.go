package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/qompassai/rose/manifest"
	"github.com/qompassai/rose/types/model"
)

// Union registry resolution — the narrowed public-host union.
//
// Rose's default registry is the operator's Harbor (harbor.qompass.ai),
// but the two upstream public hosts are treated as the same identity so
// a store written by stock Ollama keeps resolving. The union is
// deliberately narrowed (dependency-confusion mitigations):
//
//   - Harbor is tried first for every union name.
//   - Fallthrough to a public host happens only on a clean 404 from
//     Harbor. Network errors, timeouts, and auth failures surface as
//     errors; they never fall through, so an outage or a rejected
//     credential cannot silently substitute a public model for a
//     private one.
//   - Pushes never use the union: they go to Harbor only.
//   - Private namespaces (see privateNamespaces and isPrivateNamespace)
//     resolve against Harbor only, even on 404.
//   - The first successful resolution pins name -> (host, manifest
//     digest). Later pulls fetch the manifest by that digest from the
//     pinned host, so a tag that moved upstream does not silently
//     change what an existing name resolves to.

// unionPrimaryHost is the first — and for pushes and private
// namespaces, the only — host consulted for union names.
const unionPrimaryHost = "harbor.qompass.ai"

// unionHosts lists the hosts that share one identity for resolution,
// in precedence order.
var unionHosts = []string{unionPrimaryHost, "registry.ollama.ai", "ollama.com"}

// privateNamespaces is the declared set of namespaces that must only
// ever resolve on Harbor. Declare a namespace here when it is the
// operator's own publishing namespace and must never be shadowed by a
// same-named public namespace. Namespaces not listed here can still be
// Harbor-only in effect: see isPrivateNamespace.
var privateNamespaces = map[string]bool{
	"qompassai": true,
}

func isUnionHost(host string) bool {
	for _, h := range unionHosts {
		if strings.EqualFold(host, h) {
			return true
		}
	}
	return false
}

// isPrivateNamespace reports whether namespace must resolve against
// Harbor only. A namespace is private when it is in the declared
// privateNamespaces set, or when resolution pins show it has only ever
// resolved on Harbor: once a namespace has a Harbor track record and
// no public one, falling through could hand the name to a public
// shadow, so it stays Harbor-only.
func isPrivateNamespace(namespace string) (bool, error) {
	if privateNamespaces[strings.ToLower(namespace)] {
		return true, nil
	}

	hosts, err := manifest.PinnedHostsForNamespace(namespace)
	if err != nil {
		return false, err
	}
	if len(hosts) == 0 {
		return false, nil
	}
	for host := range hosts {
		if !strings.EqualFold(host, unionPrimaryHost) {
			return false, nil
		}
	}
	return true, nil
}

// unionCandidates returns the names to try for a pull of n, in
// precedence order: Harbor first, then the public hosts. Private
// namespaces get Harbor only. The caller must have checked that n's
// host is a union host.
func unionCandidates(n model.Name) ([]model.Name, error) {
	private, err := isPrivateNamespace(n.Namespace)
	if err != nil {
		return nil, err
	}

	hosts := unionHosts
	if private {
		hosts = unionHosts[:1]
	}

	candidates := make([]model.Name, 0, len(hosts))
	for _, host := range hosts {
		candidate := n
		candidate.Host = host
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

// pushTargetName returns the name a push of n is sent to. Union names
// push to Harbor only — the union never applies to pushes — while a
// name with an explicit non-union host keeps that host, since the user
// named exactly one registry.
func pushTargetName(n model.Name) model.Name {
	if isUnionHost(n.Host) {
		n.Host = unionPrimaryHost
	}
	return n
}

// resolvePullManifest fetches the manifest for a pull of n and reports
// the name whose host actually served it, so blob downloads go to the
// host that has the model. The returned manifest must still be written
// locally under the requested name n, not the resolved name.
func resolvePullManifest(ctx context.Context, n model.Name, regOpts *registryOptions) (*manifest.Manifest, []byte, model.Name, error) {
	if !isUnionHost(n.Host) {
		mf, data, err := pullModelManifest(ctx, n, regOpts)
		return mf, data, n, err
	}

	pin, pinned, err := manifest.ReadResolutionPin(n)
	if err != nil {
		return nil, nil, n, fmt.Errorf("read resolution pin for %s: %w", n.DisplayShortest(), err)
	}
	if pinned {
		return resolvePinnedManifest(ctx, n, pin, regOpts)
	}

	candidates, err := unionCandidates(n)
	if err != nil {
		return nil, nil, n, err
	}

	for _, candidate := range candidates {
		mf, data, err := pullModelManifest(ctx, candidate, regOpts)
		if err == nil {
			digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
			slog.Info("resolved model manifest",
				"name", n.DisplayShortest(),
				"host", candidate.Host,
				"digest", digest)
			if err := manifest.WriteResolutionPin(n, manifest.ResolutionPin{Host: candidate.Host, Digest: digest}); err != nil {
				// The pull itself is valid; a pin that cannot be
				// persisted must not fail it, but the loss of
				// pinning protection is worth a loud warning.
				slog.Warn("could not record resolution pin; future pulls of this name are unpinned",
					"name", n.DisplayShortest(), "error", err)
			}
			return mf, data, candidate, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			// Only a clean 404 falls through. Anything else —
			// network failure, timeout, auth rejection — surfaces.
			return nil, nil, n, err
		}
		slog.Info("manifest not found on host",
			"name", n.DisplayShortest(),
			"host", candidate.Host)
	}

	return nil, nil, n, os.ErrNotExist
}

// resolvePinnedManifest serves a pull from the recorded pin: the
// manifest is fetched by digest from the pinned host and the bytes are
// verified against the pinned digest. Any failure is terminal — a
// pinned name never falls back to tag resolution, so a moved tag
// cannot change what the name resolves to. Removing the pin (see
// manifest.DeleteResolutionPin) is the explicit re-resolution action.
func resolvePinnedManifest(ctx context.Context, n model.Name, pin manifest.ResolutionPin, regOpts *registryOptions) (*manifest.Manifest, []byte, model.Name, error) {
	pinned := n
	pinned.Host = pin.Host

	mf, data, err := pullModelManifestByDigest(ctx, pinned, pin.Digest, regOpts)
	if err != nil {
		return nil, nil, n, fmt.Errorf("pinned manifest for %s is unavailable on %s: %w; remove its entry from resolution-pins.json to re-resolve", n.DisplayShortest(), pin.Host, err)
	}
	if got := fmt.Sprintf("sha256:%x", sha256.Sum256(data)); got != pin.Digest {
		return nil, nil, n, fmt.Errorf("manifest served by %s for pinned %s has digest %s, want pinned %s; remove its entry from resolution-pins.json to re-resolve", pin.Host, n.DisplayShortest(), got, pin.Digest)
	}

	slog.Info("resolved model manifest from pin",
		"name", n.DisplayShortest(),
		"host", pin.Host,
		"digest", pin.Digest)
	return mf, data, pinned, nil
}
