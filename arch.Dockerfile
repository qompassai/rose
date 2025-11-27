# vim: filetype=dockerfile

ARG FLAVOR=${TARGETARCH}

ARG ROCMVERSION=6.3.3
ARG JETPACK5VERSION=r35.4.1
ARG JETPACK6VERSION=r36.4.3
ARG CMAKEVERSION=4.0.0

# AMD64 base using Arch Linux
FROM --platform=linux/amd64 archlinux:base-devel AS base-amd64
RUN pacman -Syu --noconfirm && \
    pacman -S --noconfirm yay git gcc gcc-libs make cmake ccache clang python sudo && \
    pacman -S --noconfirm cuda
ENV PATH=/opt/cuda/bin:$PATH

# ARM64 base using Arch Linux ARM
FROM --platform=linux/arm64 menci/archlinuxarm:base AS base-arm64
RUN pacman -Syu --noconfirm && \
    pacman -S --noconfirm base-devel git clang ccache python && \
    pacman -S --noconfirm cuda
ENV CC=clang CXX=clang++

FROM base-${TARGETARCH} AS base
ARG CMAKEVERSION
RUN curl -fsSL https://github.com/Kitware/CMake/releases/download/v${CMAKEVERSION}/cmake-${CMAKEVERSION}-linux-$(uname -m).tar.gz | tar xz -C /usr/local --strip-components 1
COPY CMakeLists.txt CMakePresets.json ./
COPY ml/backend/ggml/ggml ml/backend/ggml/ggml
ENV LDFLAGS=-s

FROM base AS cpu
RUN pacman -S --noconfirm gcc gcc-libs
RUN --mount=type=cache,target=/root/.ccache \
    cmake --preset 'CPU' \
        && cmake --build --parallel --preset 'CPU' \
        && cmake --install build --component CPU --strip --parallel 8

FROM base AS cuda-11
RUN pacman -S --noconfirm cuda
ENV PATH=/opt/cuda/bin:$PATH
RUN --mount=type=cache,target=/root/.ccache \
    cmake --preset 'CUDA 11' \
        && cmake --build --parallel --preset 'CUDA 11' \
        && cmake --install build --component CUDA --strip --parallel 8

FROM base AS cuda-12
RUN pacman -S --noconfirm cuda
ENV PATH=/opt/cuda/bin:$PATH
RUN --mount=type=cache,target=/root/.ccache \
    cmake --preset 'CUDA 12' \
        && cmake --build --parallel --preset 'CUDA 12' \
        && cmake --install build --component CUDA --strip --parallel 8

FROM base AS rocm-6
RUN pacman -S --noconfirm opencl-amd rocm-hip-sdk
ENV PATH=/opt/rocm/bin:/opt/rocm/hip/bin:$PATH
RUN --mount=type=cache,target=/root/.ccache \
    cmake --preset 'ROCm 6' \
        && cmake --build --parallel --preset 'ROCm 6' \
        && cmake --install build --component HIP --strip --parallel 8

# Jetpack stages remain Debian-based since these are NVIDIA containers
FROM --platform=linux/arm64 nvcr.io/nvidia/l4t-jetpack:${JETPACK5VERSION} AS jetpack-5
ARG CMAKEVERSION
RUN apt-get update && apt-get install -y curl ccache \
    && curl -fsSL https://github.com/Kitware/CMake/releases/download/v${CMAKEVERSION}/cmake-${CMAKEVERSION}-linux-$(uname -m).tar.gz | tar xz -C /usr/local --strip-components 1
COPY CMakeLists.txt CMakePresets.json ./
COPY ml/backend/ggml/ggml ml/backend/ggml/ggml
RUN --mount=type=cache,target=/root/.ccache \
    cmake --preset 'JetPack 5' \
        && cmake --build --parallel --preset 'JetPack 5' \
        && cmake --install build --component CUDA --strip --parallel 8

FROM --platform=linux/arm64 nvcr.io/nvidia/l4t-jetpack:${JETPACK6VERSION} AS jetpack-6
ARG CMAKEVERSION
RUN apt-get update && apt-get install -y curl ccache \
    && curl -fsSL https://github.com/Kitware/CMake/releases/download/v${CMAKEVERSION}/cmake-${CMAKEVERSION}-linux-$(uname -m).tar.gz | tar xz -C /usr/local --strip-components 1
COPY CMakeLists.txt CMakePresets.json ./
COPY ml/backend/ggml/ggml ml/backend/ggml/ggml
RUN --mount=type=cache,target=/root/.ccache \
    cmake --preset 'JetPack 6' \
        && cmake --build --parallel --preset 'JetPack 6' \
        && cmake --install build --component CUDA --strip --parallel 8

FROM base AS build
WORKDIR /go/src/github.com/qompassai/rose
COPY go.mod go.sum ./
RUN pacman -S --noconfirm go && \
    go mod download
COPY . .
ARG GOFLAGS="'-ldflags=-w -s'"
ENV CGO_ENABLED=1
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -buildmode=pie -o /bin/rose .

FROM --platform=linux/amd64 scratch AS amd64
COPY --from=cuda-11 dist/lib/rose/cuda_v11 /lib/rose/cuda_v11
COPY --from=cuda-12 dist/lib/rose/cuda_v12 /lib/rose/cuda_v12

FROM --platform=linux/arm64 scratch AS arm64
COPY --from=cuda-11 dist/lib/rose/cuda_v11 /lib/rose/cuda_v11
COPY --from=cuda-12 dist/lib/rose/cuda_v12 /lib/rose/cuda_v12
COPY --from=jetpack-5 dist/lib/rose/cuda_v11 lib/rose/cuda_jetpack5
COPY --from=jetpack-6 dist/lib/rose/cuda_v12 lib/rose/cuda_jetpack6

FROM scratch AS rocm
COPY --from=rocm-6 dist/lib/rose/rocm /lib/rose/rocm

FROM ${FLAVOR} AS archive
COPY --from=cpu dist/lib/rose /lib/rose
COPY --from=build /bin/rose /bin/rose

FROM archlinux:base
RUN pacman -Syu --noconfirm && \
    pacman -S --noconfirm ca-certificates base-devel curl && \
    pacman -Scc --noconfirm
COPY --from=archive /bin /usr/bin
ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
COPY --from=archive /lib/rose /usr/lib/rose
ENV LD_LIBRARY_PATH=/usr/local/nvidia/lib:/usr/local/nvidia/lib64
ENV NVIDIA_DRIVER_CAPABILITIES=compute,utility
ENV NVIDIA_VISIBLE_DEVICES=all
ENV ROSE_HOST=0.0.0.0:11434
EXPOSE 11434
ENTRYPOINT ["/bin/rose"]
CMD ["serve"]

