#!/usr/bin/env perl
# rebrand-rose.pl — re-apply the Rose identity to an upstream Ollama tree.
#
# This is the mechanical half of the fork's delta vs upstream, kept as a
# rerunnable script so future upstream syncs are "import tag, run script,
# build, parity-check" instead of another 1,700-commit drift.
#
# What it does (text files only, tracked by git, minus vendored C trees):
#   1. module path  github.com/ollama/ollama -> github.com/qompassai/rose
#   2. env prefix   OLLAMA_ -> ROSE_
#   3. word tokens  Ollama -> Rose, ollama -> rose, OLLAMA -> ROSE
#      with real service domains protected: ollama.com and ollama.ai are
#      restored verbatim (registry.ollama.ai, cdn.ollama.com are live
#      upstream services, not brand strings).
#
# What it deliberately does NOT do (hand patches, separate commits):
#   - envconfig.Var OLLAMA_* alias fallback
#   - envconfig.Models store-path compat default
#   - types/model default registry host (harbor.qompass.ai)
#   - licenses / README / .ai memory (paper commit)
#
# Usage: perl scripts/rebrand-rose.pl [--dry-run] [path ...]
# With no paths, processes every git-tracked text file in the tree.

use strict;
use warnings;

my $dry = grep { $_ eq '--dry-run' } @ARGV;
my @paths = grep { $_ ne '--dry-run' } @ARGV;

if (!@paths) {
    @paths = `git ls-files`;
    chomp @paths;
}

# Vendored / generated trees: upstream content we do not brand.
my %skip_dir = map { $_ => 1 } qw(llama ml/backend/ggml);
my @files;
for my $p (@paths) {
    next if $p =~ m{^(?:llama|ml/backend/ggml)/};
    next if $p =~ /\.(?:sum|lock|png|jpg|jpeg|gif|ico|svg|gguf|bin|wasm|zip|gz|pdf)$/;
    next if -B $p;
    push @files, $p;
}

my $changed = 0;
for my $f (@files) {
    open my $in, '<', $f or die "read $f: $!";
    local $/;
    my $src = <$in>;
    close $in;
    next unless defined $src;
    my $orig = $src;

    # Protect real service domains before token replacement.
    $src =~ s/ollama\.com/\x01HOST\x01/g;
    $src =~ s/ollama\.ai/\x01REG\x01/g;

    # 1. module path
    $src =~ s{github\.com/ollama/ollama}{github.com/qompassai/rose}g;
    # 2. env prefix (also catches CMake OLLAMA_* vars and doc references)
    $src =~ s/OLLAMA_/ROSE_/g;
    # 3. word tokens (not inside identifiers: \w and _ are word chars here)
    $src =~ s/(?<![A-Za-z0-9_])Ollama(?![A-Za-z0-9_])/Rose/g;
    $src =~ s/(?<![A-Za-z0-9_])ollama(?![A-Za-z0-9_])/rose/g;
    $src =~ s/(?<![A-Za-z0-9_])OLLAMA(?![A-Za-z0-9_])/ROSE/g;

    # Restore protected domains.
    $src =~ s/\x01HOST\x01/ollama.com/g;
    $src =~ s/\x01REG\x01/ollama.ai/g;

    next if $src eq $orig;
    $changed++;
    if ($dry) {
        print "would rebrand: $f\n";
    } else {
        open my $out, '>', $f or die "write $f: $!";
        print {$out} $src;
        close $out;
    }
}
print(($dry ? "dry-run: " : "") . "$changed files rebranded\n");
