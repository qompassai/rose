const std = @import("std");
const builtin = @import("builtin");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});
    const build_options = b.addOptions();

    const cpu_features = b.option(bool, "cpu-specific", "Enable CPU-specific optimizations") orelse false;
    const enable_cuda = b.option(bool, "cuda", "Enable NVIDIA CUDA support") orelse false;
    const enable_intel_gpu = b.option(bool, "intel-gpu", "Enable Intel GPU support") orelse false;
    const enable_quantum_crypto = b.option(bool, "quantum-crypto", "Enable post-quantum cryptography") orelse false;

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

    const gen_headers = b.addWriteFiles();
    _ = gen_headers.add("build_config.h",
        \\#pragma once
        \\#cmakedefine ENABLE_CPU_OPTIMIZATIONS
        \\#cmakedefine ENABLE_CUDA
        \\#cmakedefine ENABLE_INTEL_GPU
        \\#cmakedefine ENABLE_QUANTUM_CRYPTO
    );

    var exe_extension: []const u8 = "";
    const target_os_tag: std.Target.Os.Tag = target.result.os.tag;
    if (target_os_tag == .windows) exe_extension = ".exe";

    const is_native = target.result.cpu.arch == builtin.target.cpu.arch and
                      target.result.os.tag == builtin.target.os.tag;

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

        if (target_os_tag == .windows) {
            args.append("-tags") catch unreachable;
            args.append("windows") catch unreachable;
        } else if (target_os_tag == .macos) {
            args.append("-tags") catch unreachable;
            args.append("darwin") catch unreachable;
        }

        if (go_tags.len > 0) {
            args.append("-tags") catch unreachable;
            args.append(std.mem.join(b.allocator, ",", go_tags) catch unreachable) catch unreachable;
        }

        args.append("-o") catch unreachable;
        const output_name = std.fmt.allocPrint(b.allocator, "rose{s}", .{exe_extension}) catch unreachable;
        args.append(output_name) catch unreachable;
        args.append("./main.go") catch unreachable;
        break :blk args.toOwnedSlice() catch unreachable;
    };

    const go_build = b.addSystemCommand(go_args);
    go_build.step.name = "go_build";

    if (!is_native) {
        if (target_os_tag == .windows) {
            go_build.setEnvironmentVariable("GOARCH", @tagName(target.result.cpu.arch));

            go_build.setEnvironmentVariable("GOARCH", target.getCpuArch().genericName());
        } else if (target_os_tag == .macos) {
            go_build.setEnvironmentVariable("GOOS", "darwin");
            go_build.setEnvironmentVariable("GOARCH", target.getCpuArch().genericName());
        } else if (target_os_tag == .linux) {
            go_build.setEnvironmentVariable("GOOS", "linux");
            go_build.setEnvironmentVariable("GOARCH", target.getCpuArch().genericName());
        }
    }

    if (enable_quantum_crypto) {
        go_build.setEnvironmentVariable("PKG_CONFIG_PATH", "$PKG_CONFIG_PATH:/home/phaedrus/liboqs-go/.config");

        const go_clean = b.addSystemCommand(&[_][]const u8{"go", "clean", "-cache"});
        go_build.step.dependOn(&go_clean.step);

        const check_liboqs = b.addSystemCommand(&[_][]const u8{"pkg-config", "--exists", "liboqs-go"});
        check_liboqs.step.name = "check_liboqs";

        const verify_step = b.step("verify-liboqs", "Verify liboqs-go installation");
        verify_step.dependOn(&check_liboqs.step);
        go_build.step.dependOn(&check_liboqs.step);
    }

    const cmake_lists_path = ".";
    const build_type_arg = std.fmt.allocPrint(b.allocator, "-DCMAKE_BUILD_TYPE={s}", .{@tagName(optimize)}) catch unreachable;

    const cmake_args = blk: {
        var args = std.ArrayList([]const u8).init(b.allocator);
        args.append("cmake") catch unreachable;
        args.append("-Bbuild") catch unreachable;
        args.append("-H" ++ cmake_lists_path) catch unreachable;
        args.append(build_type_arg) catch unreachable;
        args.append("-DCMAKE_EXPORT_COMPILE_COMMANDS=ON") catch unreachable;

        if (!is_native) {
            const toolchain_arg = std.fmt.allocPrint(
                b.allocator,
                "-DCMAKE_TOOLCHAIN_FILE=cmake/toolchains/{s}.cmake",
                .{target.getOsTag().zigName()}
            ) catch unreachable;
            args.append(toolchain_arg) catch unreachable;
        }

        args.append(if (cpu_features) "-DENABLE_CPU_OPTIMIZATIONS=ON" else "-DENABLE_CPU_OPTIMIZATIONS=OFF") catch unreachable;
        args.append(if (enable_cuda) "-DENABLE_CUDA=ON" else "-DENABLE_CUDA=OFF") catch unreachable;
        args.append(if (enable_intel_gpu) "-DENABLE_INTEL_GPU=ON" else "-DENABLE_INTEL_GPU=OFF") catch unreachable;
        args.append(if (enable_quantum_crypto) "-DENABLE_QUANTUM_CRYPTO=ON" else "-DENABLE_QUANTUM_CRYPTO=OFF") catch unreachable;
        break :blk args.toOwnedSlice() catch unreachable;
    };

    const cpp_build = b.addSystemCommand(cmake_args);
    cpp_build.step.name = "cpp_configure";

    var make_cmd = [_][]const u8{"make", "-C", "build", "-j8"};
    if (target_os_tag == .windows) {
        make_cmd = [_][]const u8{"cmake", "--build", "build", "--config", @tagName(optimize)};
    }

    const cpp_make = b.addSystemCommand(&make_cmd);
    cpp_make.step.name = "cpp_compile";
    cpp_make.step.dependOn(&cpp_build.step);

    const build_step = b.step("rose", "Build the Rose backend");
    build_step.dependOn(&gen_headers.step);
    build_step.dependOn(&cpp_make.step);
    build_step.dependOn(&go_build.step);
    b.default_step.dependOn(build_step);

    var run_cmd_args = [_][]const u8{"./rose"};
    if (target_os_tag == .windows) run_cmd_args = [_][]const u8{"./rose.exe"};

    const run_cmd = b.addSystemCommand(&run_cmd_args);
    run_cmd.step.dependOn(build_step);

    const run_step = b.step("run", "Run the Rose binary");
    run_step.dependOn(&run_cmd.step);

    const info_cmd = b.addSystemCommand(&[_][]const u8{
        "echo",
        std.fmt.allocPrint(b.allocator, "Building for target: {s}-{s}", .{
            target.getOsTag().zigName(),
            @tagName(target.result.cpu.arch)
        }) catch unreachable,
    });
    info_cmd.step.name = "target_info";

    const info_step = b.step("info", "Show build target information");
    info_step.dependOn(&info_cmd.step);
}

