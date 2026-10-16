{
  description = "go-duktape: Go bindings for the duktape JavaScript engine";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.05";

  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      # musl.cc's prebuilt armv7 toolchain: static, so it runs as it comes.
      # benchstat, to compare the benchmarks' runs (HEAP_CACHE.md). nixpkgs
      # does not package it, so it is built from golang.org/x/perf at a pinned
      # commit: the last one whose go.mod still builds with this nixpkgs' Go
      # (1.24); later ones need 1.25.
      benchstat = pkgs.buildGoModule {
        pname = "benchstat";
        version = "0-unstable-2026-02-09";
        src = pkgs.fetchFromGitHub {
          owner = "golang";
          repo = "perf";
          rev = "b57e4e371b65454dd44026af9d45c69605bc0c3c";
          hash = "sha256-Sz7Fq0H+ikccAfaJirKHLIA+P4ZqMPS3S/Vj7WrGFy4=";
        };
        vendorHash = "sha256-kGF184E+rOWncQsvjk1iCpF26/3Ll/IY9CPEh6vhRBQ=";
        subPackages = [ "cmd/benchstat" ];
      };
      armv7CC = builtins.fetchTarball {
        url = "http://more.musl.cc/11.2.1/x86_64-linux-musl/armv7l-linux-musleabihf-cross.tgz";
        sha256 = "05hmrsp6lcq8d3hbzv761ah6waqz7g735x53bx86p7w2zrj6sqfm";
      };
    in
    {
      devShells.${system} = {
        default = pkgs.mkShell {
          packages = [ pkgs.go benchstat ];
        };

        # A shell whose C compiler links statically against musl, as embedded
        # programs often are. HEAP_CACHE.md's benchmark needs it: the effect
        # the heap's block cache avoids is musl's allocator's, and does not
        # show against glibc. (pkgsStatic's own stdenv is what sets
        # CC to the musl compiler; a plain mkShell would set it back to gcc.)
        musl = pkgs.pkgsStatic.mkShell {
          packages = [ pkgs.go pkgs.file benchstat ];
          CGO_ENABLED = "1";
        };

        # A shell that builds for armv7 against musl, with qemu to run what it
        # builds (go test -exec qemu-arm).
        # The heap cache's header and duktape's value layout differ on 32-bit
        # ARM, so the cache's tests are worth running there too.
        armv7 = pkgs.mkShellNoCC {
          packages = [ pkgs.go pkgs.qemu-user benchstat ];
          GOARCH = "arm";
          GOARM = "7";
          CGO_ENABLED = "1";
          CC = "${armv7CC}/bin/armv7l-linux-musleabihf-gcc";
        };
      };
    };
}
