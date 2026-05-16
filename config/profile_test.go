package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tg123/oci-to-wsl/config"
)

func TestLoadProfile_FullFields(t *testing.T) {
	yaml := `
name: test-distro
image: ubuntu:22.04
install_dir: C:\WSL\test
init_cmds:
  - apt-get update -y
  - apt-get install -y curl
`
	p := writeAndLoad(t, yaml)

	if p.Name != "test-distro" {
		t.Errorf("Name: got %q, want %q", p.Name, "test-distro")
	}
	if p.Image != "ubuntu:22.04" {
		t.Errorf("Image: got %q, want %q", p.Image, "ubuntu:22.04")
	}
	if p.InstallDir != `C:\WSL\test` {
		t.Errorf("InstallDir: got %q, want %q", p.InstallDir, `C:\WSL\test`)
	}
	if len(p.InitCmds) != 2 {
		t.Fatalf("InitCmds length: got %d, want 2", len(p.InitCmds))
	}
	if p.InitCmds[0] != "apt-get update -y" {
		t.Errorf("InitCmds[0]: got %q, want %q", p.InitCmds[0], "apt-get update -y")
	}
}

func TestLoadProfile_MinimalFields(t *testing.T) {
	yaml := "name: minimal\nimage: alpine:latest\n"
	p := writeAndLoad(t, yaml)

	if p.Name != "minimal" {
		t.Errorf("Name: got %q, want %q", p.Name, "minimal")
	}
	if p.Image != "alpine:latest" {
		t.Errorf("Image: got %q, want %q", p.Image, "alpine:latest")
	}
	if p.InstallDir != "" {
		t.Errorf("InstallDir: expected empty, got %q", p.InstallDir)
	}
	if len(p.InitCmds) != 0 {
		t.Errorf("InitCmds: expected empty slice, got %v", p.InitCmds)
	}
}

func TestLoadProfile_FileNotFound(t *testing.T) {
	_, err := config.LoadProfile(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadProfile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte(":\tinvalid: yaml: ["), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadProfile(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

// writeAndLoad writes yaml content to a temp file and calls LoadProfile.
func writeAndLoad(t *testing.T, yamlContent string) *config.Profile {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := config.LoadProfile(path)
	if err != nil {
		t.Fatalf("LoadProfile: unexpected error: %v", err)
	}
	return p
}
