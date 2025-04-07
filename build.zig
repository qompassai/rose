const std = @import("std");

pub fn build(b: *std.Build) void {
    const optimize = b.standardOptimizeOption(.{});
    const build_options = b.addOptions();

    // Optional features from CLI
    const cpu_features = b.option(bool, "cpu-specific", "Enable CPU-specific optimizations") orelse false;
    const enable_cuda = b.option(bool, "cuda", "Enable NVIDIA CUDA support") orelse false;
    const enable_intel_gpu = b.option(bool, "intel-gpu", "Enable Intel GPU support") orelse false;
    const enable_quantum_crypto = b.option(bool, "quantum-crypto", "Enable post-quantum cryptography") orelse false;

    // Set flags for conditional C++ or Zig code use
    if (cpu_features) {
        build_options.addOption(bool, "cpu_specific_optimizations", true);
        build_options.addOption(bool, "avx2", true);
        build_options.addOption(bool, "avx512f", true);
        build_options.addOption(bool, "sse4_2", true);
        build_options.addOption(bool, "fma", true);
        build_options.addOption(bool, "bmi2", true);
    }
    if (enable_cuda) build_options.addOption(bool, "enable_cuda", true);
    if (enable_intel_gpu) build_options.addOption(bool, "enable_intel_gpu", true);
    if (enable_quantum_crypto) build_options.addOption(bool, "enable_quantum_crypto", true);

    // Generate C config header (optional use in native components)
    const gen_headers = b.addWriteFiles();
    _ = gen_headers.add("build_config.h",
        \\#pragma once
        \\#cmakedefine ENABLE_CPU_OPTIMIZATIONS
        \\#cmakedefine ENABLE_CUDA
        \\#cmakedefine ENABLE_INTEL_GPU
        \\#cmakedefine ENABLE_QUANTUM_CRYPTO
    );

    // Go build (main entry point is main.go in root)
    const go_tags = blk: {
        var tags = std.ArrayList([]const u8).init(b.allocator);
        if (enable_cuda) tags.append("cuda") catch unreachable;
        if (enable_quantum_crypto) tags.append("quantum") catch unreachable;
        break :blk tags.toOwnedSlice() catch unreachable;
    };

    const go_args = blk: {
        var args = std.ArrayList([]const u8).init(b.allocator);
        args.append("go") catch unreachable;
        args.append("build") catch unreachable;
        if (go_tags.len > 0) {
            args.append("-tags") catch unreachable;
            args.append(std.mem.join(b.allocator, ",", go_tags) catch unreachable) catch unreachable;
        }
        args.append("-o") catch unreachable;
        args.append("rose") catch unreachable;
        args.append("./main.go") catch unreachable;
        break :blk args.toOwnedSlice() catch unreachable;
    };

    const go_build = b.addSystemCommand(go_args);
    go_build.step.name = "go_build";

    // C++ native extensions (optional - will no-op if no CMakeLists.txt is used)
    const cmake_lists_path = "llama"; // where CMakeLists.txt exists
    const build_type_arg = std.fmt.allocPrint(b.allocator, "-DCMAKE_BUILD_TYPE={s}", .{@tagName(optimize)}) catch unreachable;

    const cmake_args = blk: {
        var args = std.ArrayList([]const u8).init(b.allocator);
        args.append("cmake") catch unreachable;
        args.append("-Bbuild") catch unreachable;
        args.append("-H" ++ cmake_lists_path) catch unreachable;
        args.append(build_type_arg) catch unreachable;
        args.append("-DCMAKE_EXPORT_COMPILE_COMMANDS=ON") catch unreachable;
        args.append(if (cpu_features) "-DENABLE_CPU_OPTIMIZATIONS=ON" else "-DENABLE_CPU_OPTIMIZATIONS=OFF") catch unreachable;
        args.append(if (enable_cuda) "-DENABLE_CUDA=ON" else "-DENABLE_CUDA=OFF") catch unreachable;
        args.append(if (enable_intel_gpu) "-DENABLE_INTEL_GPU=ON" else "-DENABLE_INTEL_GPU=OFF") catch unreachable;
        args.append(if (enable_quantum_crypto) "-DENABLE_QUANTUM_CRYPTO=ON" else "-DENABLE_QUANTUM_CRYPTO=OFF") catch unreachable;
        break :blk args.toOwnedSlice() catch unreachable;
    };

    const cpp_build = b.addSystemCommand(cmake_args);
    cpp_build.step.name = "cpp_configure";

    const cpp_make = b.addSystemCommand(&[_][]const u8{ "make", "-C", "build", "-j8" });
    cpp_make.step.name = "cpp_compile";
    cpp_make.step.dependOn(&cpp_build.step);

    // Main build step
    const build_step = b.step("rose", "Build the Rose backend");
    build_step.dependOn(&gen_headers.step);
    build_step.dependOn(&cpp_make.step);
    build_step.dependOn(&go_build.step);
    b.default_step.dependOn(build_step);

    // Run command step
    const run_cmd = b.addSystemCommand(&[_][]const u8{ "./rose" });
    run_cmd.step.dependOn(build_step);

    const run_step = b.step("run", "Run the Rose binary");
    run_step.dependOn(&run_cmd.step);
}

