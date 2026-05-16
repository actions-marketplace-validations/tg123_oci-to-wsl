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
	)

	flag.StringVar(&profilePath, "profile", "", "Path to a YAML profile file (overrides other flags when set)")
	flag.StringVar(&imageName, "image", "", "OCI image reference, e.g. ubuntu:22.04 or myacr.azurecr.io/myimage:latest")
	flag.StringVar(&distroName, "name", "", "WSL distribution name to create")
	flag.StringVar(&installDir, "dir", "", "Directory to store the WSL virtual disk (default: ./<name>)")
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
		if imageName == "" || distroName == "" {
			flag.Usage()
			return fmt.Errorf("provide --profile, or both --image and --name")
		}
		profile = &config.Profile{
			Name:       distroName,
			Image:      imageName,
			InstallDir: installDir,
		}
	}

	return loadProfile(profile)
}

func loadProfile(profile *config.Profile) error {
	if profile.Name == "" {
		return fmt.Errorf("profile: 'name' is required")
	}
	if profile.Image == "" {
		return fmt.Errorf("profile: 'image' is required")
	}
	if profile.InstallDir == "" {
		profile.InstallDir = filepath.Join(".", profile.Name)
	}

	// Create the install directory if it doesn't exist.
	if err := os.MkdirAll(profile.InstallDir, 0700); err != nil {
		return fmt.Errorf("creating install directory %q: %w", profile.InstallDir, err)
	}

	// Pull the OCI image and write it to a temporary rootfs tar.
	tarPath := filepath.Join(os.TempDir(), profile.Name+"-rootfs.tar")
	tarFile, err := os.Create(tarPath)
	if err != nil {
		return fmt.Errorf("creating temporary tar file: %w", err)
	}
	defer func() {
		tarFile.Close()
		os.Remove(tarPath)
	}()

	if err := registry.PullToTar(profile.Image, tarFile, registry.PullOptions{}); err != nil {
		return fmt.Errorf("pulling image: %w", err)
	}
	tarFile.Close()

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
