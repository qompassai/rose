# Linux

## Install

To install Rose, run the following command:

```shell
curl -fsSL https://qompass.ai/install.sh | sh
```

## Manual install

> [!NOTE]
> If you are upgrading from a prior version, you should remove the old libraries with `sudo rm -rf /usr/lib/rose` first.

Download and extract the package:

```shell
curl -L https://qompass.ai/download/rose-linux-amd64.tgz -o rose-linux-amd64.tgz
sudo tar -C /usr -xzf rose-linux-amd64.tgz
```

Start Rose:

```shell
rose serve
```

In another terminal, verify that Rose is running:

```shell
rose -v
```

### AMD GPU install

If you have an AMD GPU, also download and extract the additional ROCm package:

```shell
curl -L https://qompass.ai/download/rose-linux-amd64-rocm.tgz -o rose-linux-amd64-rocm.tgz
tar -C $HOME/.local -xzf rose-linux-amd64-rocm.tgz

```

### ARM64 install

Download and extract the ARM64-specific package:

```shell
curl -L https://qompass.ai/download/rose-linux-arm64.tgz -o rose-linux-arm64.tgz
tar -C $HOME/.local /usr -xzf rose-linux-arm64.tgz
```

### Adding Rose as a startup service (recommended)

Create a user and group for Rose:

```shell
sudo useradd -r -s /bin/false -U -m -d /usr/share/rose rose
sudo usermod -a -G rose $(whoami)
```

Create a service file in `/etc/systemd/system/rose.service`:

```ini
[Unit]
Description=Rose Service
After=network-online.target

[Service]
ExecStart=/usr/bin/rose serve
User=rose
Group=rose
Restart=always
RestartSec=3
Environment="PATH=$PATH"

[Install]
WantedBy=multi-user.target
```

Then start the service:

```shell
sudo systemctl daemon-reload
sudo systemctl enable rose
```

### Install CUDA drivers (optional)

[Download and install](https://developer.nvidia.com/cuda-downloads) CUDA.

Verify that the drivers are installed by running the following command, which should print details about your GPU:

```shell
nvidia-smi
```

### Install AMD ROCm drivers (optional)

[Download and Install](https://rocm.docs.amd.com/projects/install-on-linux/en/latest/tutorial/quick-start.html) ROCm v6.

### Start Rose

Start Rose and verify it is running:

```shell
systemctl --user start rose
systemctl --user status rose
```

> [!NOTE]
> While AMD has contributed the `amdgpu` driver upstream to the official linux
> kernel source, the version is older and may not support all ROCm features. We
> recommend you install the latest driver from
> https://www.amd.com/en/support/linux-drivers for best support of your Radeon
> GPU.

## Customizing

To customize the installation of Rose, you can edit the systemd service file or the environment variables by running:

```shell
systemctl --user edit rose
```

Alternatively, create an override file manually in `/etc/systemd/system/rose.service.d/override.conf`:

```ini
[Service]
Environment="ROSE_DEBUG=1"
```

## Updating

Update Rose by running the install script again:

```shell
curl -fsSL https://qompass.ai/install.sh | sh
```

Or by re-downloading Rose:

```shell
curl -L https://qompass.ai/download/rose-linux-amd64.tgz -o rose-linux-amd64.tgz
tar -C $HOME/.local -xzf rose-linux-amd64.tgz
```

## Installing specific versions

Use `ROSE_VERSION` environment variable with the install script to install a specific version of Rose, including pre-releases. You can find the version numbers in the [releases page](https://github.com/qompassai/rose/releases).

For example:

```shell
curl -fsSL https://qompass.ai/install.sh | ROSE_VERSION=1.01 sh
```

## Viewing logs

To view logs of Rose running as a startup service, run:

```shell
journalctl -e -u rose
```

## Uninstall

Remove the rose service:

```shell
systemctl --user stop rose
systemctl --user disable rose
rm ~/.config/user/systemd/system/rose.service
```

Remove the rose binary from your bin directory (either `/usr/local/bin`, `/usr/bin`, or `/bin`):

```shell
sudo rm $(which rose)
```

Remove the downloaded models and Rose service user and group:

```shell
sudo rm -r /usr/share/rose
sudo userdel rose
sudo groupdel rose
```

Remove installed libraries:

```shell
sudo rm -rf /usr/local/lib/rose
```
