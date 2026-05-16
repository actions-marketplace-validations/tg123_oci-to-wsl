# oci-to-wsl

Load an OCI container-registry image directly into a **Windows Subsystem for Linux** distribution – a single, self-contained binary with no runtime dependencies.

## Features

| Feature | Detail |
|---|---|
| Single binary | Pure Go; produces one `.exe` with no extra DLLs or runtimes required |
| OCI image pull | Uses [go-containerregistry](https://github.com/google/go-containerregistry) (`crane`) to pull and flatten any OCI image into a rootfs tar |
| WSL import | Calls `wsl.exe --import` automatically |
| YAML profiles | Describe the image, install path, and post-install commands in a reusable file |
| ACR support | **Azure Container Registry** (*.azurecr.io / *.azurecr.cn / *.azurecr.us) is detected automatically; a browser-based Azure AD device-code login is triggered without any pre-configuration |

## Quick start

```powershell
# From Docker Hub
oci-to-wsl.exe --image ubuntu:22.04 --name my-ubuntu

# From Azure Container Registry (browser login opens automatically)
oci-to-wsl.exe --image myacr.azurecr.io/myimage:latest --name myimage

# Using a YAML profile
oci-to-wsl.exe --profile ubuntu.yaml
```

## YAML profile

```yaml
# ubuntu.yaml
name: my-ubuntu
image: ubuntu:22.04
install_dir: C:\WSL\my-ubuntu   # optional – defaults to .\<name>
init_cmds:                       # optional – run inside the new distro after import
  - apt-get update -y
  - apt-get install -y curl git
```

See [`example-profile.yaml`](example-profile.yaml) for a complete example.

## Building from source

```powershell
# Requires Go 1.21+
GOOS=windows GOARCH=amd64 go build -o oci-to-wsl.exe .
```

## How ACR authentication works

When the image reference points to an Azure Container Registry endpoint, `oci-to-wsl`:

1. Requests a **device code** from Azure AD (`login.microsoftonline.com`).
2. Prints the verification URL and code, and attempts to open the browser automatically.
3. Polls until you complete the sign-in flow.
4. Exchanges the AAD access token for an **ACR refresh token** via the registry's `/oauth2/exchange` endpoint.
5. Obtains a scoped **ACR access token** and uses it for the image pull.

No credentials are stored on disk.

## CLI flags

| Flag | Description |
|---|---|
| `--profile <path>` | Path to a YAML profile file |
| `--image <ref>` | OCI image reference (required without `--profile`) |
| `--name <distro>` | WSL distribution name (required without `--profile`) |
| `--dir <path>` | Directory to store the WSL virtual disk (default: `.\<name>`) |
