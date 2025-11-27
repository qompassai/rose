#!/bin/bash

PACKAGE_NAME="rose"
PACKAGE_VERSION=$(git describe --tags 2>/dev/null || echo "dev")

export CUDA_PATH=${CUDA_PATH:-/opt/cuda}
export VULKAN_SDK=${VULKAN_SDK:-/usr}
export PATH="/opt/osxcross/bin:$PATH"
export OSXCROSS_SDK="/opt/osxcross/SDK/MacOSX15.4.sdk"

export CGO_ENABLED=1

OUTPUT_DIR="releases"
mkdir -p "$OUTPUT_DIR"

TARGETS=(
  "linux/amd64:tar.gz"
  "linux/arm64:tar.gz"
  "darwin/amd64:tar.gz"
  "darwin/arm64:tar.gz"
  "windows/amd64:zip"
)

build_with_cuda_vulkan() {
  local output_file=$1
  export OSXCROSS_NO_INCLUDE_PATH_WARNINGS=1
  export CGO_ENABLED=1
  
  export CC=clang
  export CXX=clang++
  
  export CGO_CFLAGS="-I/usr/include/vulkan -I$CUDA_PATH/include" 
  export CGO_CXXFLAGS="-I/usr/include/vulkan -I$CUDA_PATH/include --cuda-gpu-arch=sm_89"
  export CGO_CXXFLAGS="$CGO_CFLAGS -std=c++20"
  export CGO_LDFLAGS="-L$CUDA_PATH/lib64 -lcudart_static -ldl -lrt -pthread -L/usr/lib -lvulkan"
  
  go build -tags "cuda vulkan intel" \
    -ldflags "-X main.EnabledBackends=cuda,vulkan,intel -X main.Version=$PACKAGE_VERSION" \
    -o "$output_file" ./main.go
    
  return $?
}

build_standard() {
  local output_file=$1
  local goos=$2
  local goarch=$3
  
  export CGO_ENABLED=1
  
  if [ "$goos" = "linux" ] && [ "$goarch" = "arm64" ]; then
    export CC=aarch64-linux-gnu-gcc
  elif [ "$goos" = "windows" ]; then
    export CC=x86_64-w64-mingw32-gcc
  elif [ "$goos" = "darwin" ]; then
    if [ "$goarch" = "arm64" ]; then
      export CC=oa64-clang
    else
      export CC=o64-clang
    fi
  fi
  
  export PKG_CONFIG_PATH=$PKG_CONFIG_PATH:/opt/liboqs/lib/pkgconfig
  
  GOOS=$goos GOARCH=$goarch go build \
    -ldflags "-X main.EnabledBackends=standard -X main.Version=$PACKAGE_VERSION" \
    -o "$output_file" ./main.go
    
  return $?
}

echo "Building Rose version $PACKAGE_VERSION..."

if [[ "$(uname)" == "Linux" ]]; then
  echo "Building native Linux version with CUDA/Vulkan support..."
  native_output="$OUTPUT_DIR/$PACKAGE_NAME-$PACKAGE_VERSION-native"
  
  if build_with_cuda_vulkan "$native_output"; then
    tar -czvf "${native_output}.tar.gz" "$native_output"
    rm "$native_output"
    echo "✅ Native build with CUDA/Vulkan completed successfully!"
  else
    echo "❌ Native build with CUDA/Vulkan failed!"
  fi
fi

for target in "${TARGETS[@]}"; do
  goos_arch="${target%:*}"
  file_extension="${target#*:}"
  IFS='/' read -r goos goarch <<< "$goos_arch"
  
  file_name="${PACKAGE_NAME}-${PACKAGE_VERSION}-${goos}-${goarch}"
  output_file="$OUTPUT_DIR/$file_name"
  
  echo "Building for $goos/$goarch..."
  
  if [ "$goos" = "windows" ]; then
    output_file="${output_file}.exe"
  fi
  
  build_standard "$output_file" "$goos" "$goarch"
  
  if [ $? -ne 0 ]; then
    echo "❌ Build failed for $goos/$goarch"
    continue
  fi
  
  if [ "$goos" = "windows" ]; then
    (cd "$OUTPUT_DIR" && zip "${file_name}.zip" "$(basename "$output_file")" && rm "$(basename "$output_file")")
  else
    tar -czvf "${output_file}.tar.gz" "$output_file"
    rm "$output_file"
  fi
  
  echo "✅ Finished building for $goos/$goarch"
done

echo "All builds completed successfully! Binaries available in $OUTPUT_DIR/"

echo "Generating checksums..."
(cd "$OUTPUT_DIR" && sha256sum * > checksums.txt)

echo "Build process complete!"
