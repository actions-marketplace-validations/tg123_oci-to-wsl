// Package registry handles pulling OCI images and exporting them as rootfs tarballs.
package registry

import (
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

// PullOptions controls how an image is pulled.
type PullOptions struct {
	// Authenticator is used for registry authentication.
	// If nil, the default keychain (docker config, env vars) is used unless
	// the registry is detected as Azure Container Registry, in which case
	// the Azure SDK credential chain (az CLI + interactive browser) runs
	// automatically.
	Authenticator authn.Authenticator

	// Platform selects a specific OS/arch from a multi-arch manifest list.
	// Format is "os/arch" (e.g. "linux/amd64", "linux/arm64"). When empty
	// the host's runtime arch is used (with OS forced to linux).
	Platform string
}

// PullToTar pulls the OCI image identified by imageRef and writes the flattened
// rootfs tar to w.  The flattened tar is suitable for use with "wsl --import".
func PullToTar(imageRef string, w io.Writer, opts PullOptions) error {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return fmt.Errorf("parsing image reference %q: %w", imageRef, err)
	}

	platform, err := resolvePlatform(opts.Platform)
	if err != nil {
		return err
	}

	pullOpts := buildCraneOptions(ref, platform, opts)

	fmt.Printf("Pulling image %s (%s/%s) ...\n", ref, platform.OS, platform.Architecture)
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
// authenticator (ACR browser flow, explicit creds, or the default keychain)
// and the requested platform.
func buildCraneOptions(ref name.Reference, platform *v1.Platform, opts PullOptions) []crane.Option {
	craneOpts := []crane.Option{crane.WithPlatform(platform)}

	if opts.Authenticator != nil {
		craneOpts = append(craneOpts, crane.WithAuth(opts.Authenticator))
		return craneOpts
	}

	// Auto-detect ACR registries and use browser-based auth.
	registry := ref.Context().RegistryStr()
	if isACR(registry) {
		fmt.Printf("Detected ACR registry %s – authenticating via Azure SDK ...\n", registry)
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

// resolvePlatform converts a "os/arch" string into a *v1.Platform, falling
// back to the host runtime arch when the input is empty.
func resolvePlatform(spec string) (*v1.Platform, error) {
	if spec == "" {
		return &v1.Platform{OS: "linux", Architecture: hostArch()}, nil
	}
	parts := strings.SplitN(spec, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid platform %q (expected os/arch, e.g. linux/amd64)", spec)
	}
	return &v1.Platform{OS: parts[0], Architecture: normalizeArch(parts[1])}, nil
}

func hostArch() string {
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return runtime.GOARCH
	default:
		return "amd64"
	}
}

func normalizeArch(a string) string {
	switch strings.ToLower(a) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return a
	}
}

// isACR returns true when the registry host looks like an Azure Container Registry.
func isACR(registry string) bool {
	lower := strings.ToLower(registry)
	return strings.HasSuffix(lower, ".azurecr.io") ||
		strings.HasSuffix(lower, ".azurecr.cn") ||
		strings.HasSuffix(lower, ".azurecr.us")
}
