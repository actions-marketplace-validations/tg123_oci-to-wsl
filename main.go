package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tg123/oci-to-wsl/config"
	"github.com/tg123/oci-to-wsl/registry"
	"github.com/tg123/oci-to-wsl/wsl"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		profilePath string
		imageName   string
		distroName  string
		installDir  string
		platform    string
		tenant      string
		saveTar     string
	)

	flag.StringVar(&profilePath, "profile", "", "Path to a YAML profile file (overrides other flags when set)")
	flag.StringVar(&imageName, "image", "", "OCI image reference, e.g. ubuntu:22.04 or myacr.azurecr.io/myimage:latest")
	flag.StringVar(&distroName, "name", "", "WSL distribution name to create")
	flag.StringVar(&installDir, "dir", "", "Directory to store the WSL virtual disk (default: ./<name>)")
	flag.StringVar(&platform, "platform", "", "Image platform to pull, e.g. linux/amd64 or linux/arm64 (default: host)")
	flag.StringVar(&tenant, "tenant", "", "Azure AD tenant id for ACR auth (required when signed-in as a guest in the ACR's tenant)")
	flag.StringVar(&saveTar, "save-tar", "", "Write the exported rootfs tar to this path and skip 'wsl --import' (useful on non-Windows hosts)")
	flag.Usage = usage
	flag.Parse()

	// Build a Profile from the YAML file or the CLI flags.
	var profile *config.Profile
	if profilePath != "" {
		var err error
		profile, err = config.LoadProfile(profilePath)
		if err != nil {
			return fmt.Errorf("loading profile: %w", err)
		}
	} else {
		if imageName == "" {
			flag.Usage()
			return fmt.Errorf("provide --profile, or --image (and --name unless --save-tar is set)")
		}
		if distroName == "" && saveTar == "" {
			flag.Usage()
			return fmt.Errorf("--name is required unless --save-tar is set")
		}
		profile = &config.Profile{
			Name:       distroName,
			Image:      imageName,
			InstallDir: installDir,
			Platform:   platform,
			Tenant:     tenant,
		}
	}

	// CLI flags override matching profile fields when explicitly set.
	if platform != "" {
		profile.Platform = platform
	}
	if tenant != "" {
		profile.Tenant = tenant
	}

	return loadProfile(profile, saveTar)
}

func loadProfile(profile *config.Profile, saveTar string) error {
	if profile.Image == "" {
		return fmt.Errorf("profile: 'image' is required")
	}
	if saveTar == "" && profile.Name == "" {
		return fmt.Errorf("profile: 'name' is required")
	}
	if saveTar == "" {
		if profile.InstallDir == "" {
			profile.InstallDir = filepath.Join(".", profile.Name)
		}
		if err := os.MkdirAll(profile.InstallDir, 0700); err != nil {
			return fmt.Errorf("creating install directory %q: %w", profile.InstallDir, err)
		}
	}

	// Decide tar destination + cleanup policy.
	var tarPath string
	var cleanup bool
	if saveTar != "" {
		tarPath = saveTar
		cleanup = false
	} else {
		tarPath = filepath.Join(os.TempDir(), profile.Name+"-rootfs.tar")
		cleanup = true
	}

	tarFile, err := os.Create(tarPath)
	if err != nil {
		return fmt.Errorf("creating tar file %q: %w", tarPath, err)
	}
	defer func() {
		tarFile.Close()
		if cleanup {
			os.Remove(tarPath)
		}
	}()

	if err := registry.PullToTar(profile.Image, tarFile, registry.PullOptions{
		Platform: profile.Platform,
		Tenant:   profile.Tenant,
	}); err != nil {
		return fmt.Errorf("pulling image: %w", err)
	}
	tarFile.Close()

	if saveTar != "" {
		fi, _ := os.Stat(tarPath)
		fmt.Printf("Wrote rootfs tar to %s", tarPath)
		if fi != nil {
			fmt.Printf(" (%d bytes)", fi.Size())
		}
		fmt.Println()
		return nil
	}

	// Import the rootfs into WSL.
	if err := wsl.Import(wsl.ImportOptions{
		Name:       profile.Name,
		InstallDir: profile.InstallDir,
		RootfsTar:  tarPath,
	}); err != nil {
		return err
	}

	fmt.Printf("WSL distribution %q created successfully.\n", profile.Name)

	// Run any post-creation initialisation commands.
	for _, cmd := range profile.InitCmds {
		if err := wsl.RunCommand(profile.Name, cmd); err != nil {
			return fmt.Errorf("init command %q failed: %w", cmd, err)
		}
	}

	if len(profile.InitCmds) > 0 {
		fmt.Printf("Initialisation of %q complete.\n", profile.Name)
	}
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `oci-to-wsl – load an OCI container image into a WSL distribution.

Usage:
  oci-to-wsl --profile <profile.yaml>
  oci-to-wsl --image <image> --name <distro> [--dir <path>]

Flags:
`)
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
Examples:
  # Use a YAML profile
  oci-to-wsl --profile ubuntu.yaml

  # Import directly from Docker Hub
  oci-to-wsl --image ubuntu:22.04 --name my-ubuntu --dir C:\WSL\ubuntu

  # Import from Azure Container Registry (browser login triggered automatically)
  oci-to-wsl --image myacr.azurecr.io/myimage:latest --name myimage

Profile YAML example:
  name: my-ubuntu
  image: ubuntu:22.04
  install_dir: C:\WSL\my-ubuntu
  init_cmds:
    - apt-get update -y
    - apt-get install -y curl git
`)
}
