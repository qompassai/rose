# Common environment setup across build*.sh scripts

export VERSION=${VERSION:-$(git describe --tags --first-parent --abbrev=7 --long --dirty --always | sed -e "s/^v//g")}
export GOFLAGS="'-ldflags=-w -s \"-X=github.com/qompassai/rose/version.Version=$VERSION\" \"-X=github.com/qompassai/rose/server.mode=release\"'"
# TODO - consider `docker buildx ls --format=json` to autodiscover platform capability
PLATFORM=${PLATFORM:-"linux/arm64,linux/amd64"}
DOCKER_ORG=${DOCKER_ORG:-"qompassai"}
FINAL_IMAGE_REPO=${FINAL_IMAGE_REPO:-"${DOCKER_ORG}/rose"}
ROSE_COMMON_BUILD_ARGS="--build-arg=VERSION \
    --build-arg=GOFLAGS \
    --build-arg=ROSE_CUSTOM_CPU_DEFS \
    --build-arg=ROSE_SKIP_CUDA_GENERATE \
    --build-arg=ROSE_SKIP_CUDA_11_GENERATE \
    --build-arg=ROSE_SKIP_CUDA_12_GENERATE \
    --build-arg=CUDA_V11_ARCHITECTURES \
    --build-arg=CUDA_V12_ARCHITECTURES \
    --build-arg=ROSE_SKIP_ROCM_GENERATE \
    --build-arg=ROSE_FAST_BUILD \
    --build-arg=CUSTOM_CPU_FLAGS \
    --build-arg=GPU_RUNNER_CPU_FLAGS \
    --build-arg=AMDGPU_TARGETS"

echo "Building Rose"
echo "VERSION=$VERSION"
CUDA_12_GENERATE \
    --build-arg=CUDA_V11_ARCHITECTURES \
    --build-arg=CUDA_V12_ARCHITECTURES \
    --build-arg=ROSE_SKIP_ROCM_GENERATE \
    --build-arg=ROSE_FAST_BUILD \
    --build-arg=CUSTOM_CPU_FLAGS \
    --build-arg=GPU_RUNNER_CPU_FLAGS \
    --build-arg=AMDGPU_TARGETS"

echo "Building Rose"
echo "VERSION=$VERSION"
echo "PLATFORM=$PLATFORM"
