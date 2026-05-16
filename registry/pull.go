// Package registry handles pulling OCI images and exporting them as rootfs tarballs.
package registry

import (
	"fmt"
	"io"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
)

// PullOptions controls how an image is pulled.
type PullOptions struct {
	// Authenticator is used for registry authentication.
	// If nil, the default keychain (docker config, env vars) is used.
	Authenticator authn.Authenticator
}

// PullToTar pulls the OCI image identified by imageRef and writes the flattened
// rootfs tar to w.  The flattened tar is suitable for use with "wsl --import".
func PullToTar(imageRef string, w io.Writer, opts PullOptions) error {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return fmt.Errorf("parsing image reference %q: %w", imageRef, err)
	}

	pullOpts := buildCraneOptions(ref, opts)

	fmt.Printf("Pulling image %s ...\n", ref)
	img, err := crane.Pull(imageRef, pullOpts...)
	if err != nil {
		return fmt.Errorf("pulling image %q: %w", imageRef, err)
	}

	fmt.Println("Exporting rootfs tar ...")
	if err := crane.Export(img, w); err != nil {
		return fmt.Errorf("exporting rootfs tar: %w", err)
	}
	return nil
}

// buildCraneOptions constructs the crane.Option slice, wiring in the right
// authenticator (ACR browser flow, explicit creds, or the default keychain).
func buildCraneOptions(ref name.Reference, opts PullOptions) []crane.Option {
	var craneOpts []crane.Option

	if opts.Authenticator != nil {
		craneOpts = append(craneOpts, crane.WithAuth(opts.Authenticator))
		return craneOpts
	}

	// Auto-detect ACR registries and use browser-based auth.
	registry := ref.Context().RegistryStr()
	if isACR(registry) {
		fmt.Printf("Detected ACR registry %s – initiating browser login ...\n", registry)
		auth, err := NewACRAuthenticator(registry)
		if err != nil {
			// Fall through to default keychain; the error will surface during pull.
			fmt.Printf("Warning: ACR browser auth failed: %v – falling back to keychain\n", err)
		} else {
			craneOpts = append(craneOpts, crane.WithAuth(auth))
			return craneOpts
		}
	}

	// Default: use the Docker credential keychain (config.json / env vars).
	craneOpts = append(craneOpts, crane.WithAuthFromKeychain(authn.DefaultKeychain))
	return craneOpts
}

// isACR returns true when the registry host looks like an Azure Container Registry.
func isACR(registry string) bool {
	lower := strings.ToLower(registry)
	return strings.HasSuffix(lower, ".azurecr.io") ||
		strings.HasSuffix(lower, ".azurecr.cn") ||
		strings.HasSuffix(lower, ".azurecr.us")
}
