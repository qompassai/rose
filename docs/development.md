# Development

Install prerequisites:

- [Go 1.27.1](https://go.dev/doc/devel/release) or a later patched Go 1.27 toolchain.
  The minimum is recorded in `go.mod`; cryptography uses the Go standard library,
  not a separately installed liboqs shared library.
- C/C++ Compiler e.g. Clang on macOS, [TDM-GCC](https://github.com/jmeubank/tdm-gcc/releases/latest) (Windows amd64) or [llvm-mingw](https://github.com/mstorsjo/llvm-mingw) (Windows arm64), GCC/Clang on Linux.

Then build and run Rose from the root directory of the repository:

```shell
go run . serve
```

## macOS (Apple Silicon)

macOS Apple Silicon supports Metal which is built-in to the Rose binary. No additional steps are required.

## macOS (Intel)

Install prerequisites:

- [CMake](https://cmake.org/download/) or `brew install cmake`

Then, configure and build the project:

```shell
cmake -B build
cmake --build build
```

Lastly, run Rose:

```shell
go run . serve
```

## Windows

Install prerequisites:

- [CMake](https://cmake.org/download/)
- [Visual Studio 2022](https://visualstudio.microsoft.com/downloads/) including the Native Desktop Workload
- (Optional) AMD GPU support
    - [ROCm](https://rocm.docs.amd.com/en/latest/)
    - [Ninja](https://github.com/ninja-build/ninja/releases)
- (Optional) NVIDIA GPU support
    - [CUDA SDK](https://developer.nvidia.com/cuda-downloads?target_os=Windows&target_arch=x86_64&target_version=11&target_type=exe_network)

Then, configure and build the project:

```shell
cmake -B build
cmake --build build --config Release
```

> [!IMPORTANT]
> Building for ROCm requires additional flags:
> ```
> cmake -B build -G Ninja -DCMAKE_C_COMPILER=clang -DCMAKE_CXX_COMPILER=clang++
> cmake --build build --config Release
> ```


Lastly, run Rose:

```shell
go run . serve
```

## Windows (ARM)

Windows ARM does not support additional acceleration libraries at this time.  Do not use cmake, simply `go run` or `go build`.

## Linux

Install prerequisites:

- [CMake](https://cmake.org/download/) or `sudo apt install cmake` or `sudo dnf install cmake`
- (Optional) AMD GPU support
    - [ROCm](https://rocm.docs.amd.com/projects/install-on-linux/en/latest/install/quick-start.html)
- (Optional) NVIDIA GPU support
    - [CUDA SDK](https://developer.nvidia.com/cuda-downloads)

> [!IMPORTANT]
> Ensure prerequisites are in `PATH` before running CMake.


Then, configure and build the project:

```shell
cmake -B build
cmake --build build
```

Lastly, run Rose:

```shell
go run . serve
```

## Docker

```shell
docker build .
```

### ROCm

```shell
docker build --build-arg FLAVOR=rocm .
```

## Running tests

To run tests, use `go test`:

```shell
go test ./...
```

Concurrency tests use the stable `testing/synctest.Test` API and run without
experimental build tags. Remove any old `GOEXPERIMENT=synctest` setting from your
test command or shell configuration; do not exclude these tests to obtain a pass.

For integration and security changes, follow [the engineering playbook](../SKILLS.md).
Full inference builds still need the platform's C/C++ and acceleration dependencies;
passing the pure-Go transport and authentication tests is not a full inference test.

## Library detection

Rose looks for acceleration libraries in the following paths relative to the `rose` executable:

* `./lib/rose` (Windows)
* `../lib/rose` (Linux)
* `.` (macOS)
* `build/lib/rose` (for development)

If the libraries are not found, Rose will not run with any acceleration libraries.
