{
  description = "go-duktape: Go bindings for the duktape JavaScript engine";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.05";

  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      # musl.cc's prebuilt armv7 toolchain: static, so it runs as it comes.
      armv7CC = builtins.fetchTarball {
        url = "http://more.musl.cc/11.2.1/x86_64-linux-musl/armv7l-linux-musleabihf-cross.tgz";
        sha256 = "05hmrsp6lcq8d3hbzv761ah6waqz7g735x53bx86p7w2zrj6sqfm";
      };
    in
    {
      devShells.${system} = {
        default = pkgs.mkShell {
          packages = [ pkgs.go ];
        };

        # A shell whose C compiler links statically against musl, as embedded
        # programs often are. HEAP_CACHE.md's benchmark needs it: the effect
        # the heap's block cache avoids is musl's allocator's, and does not
        # show against glibc. (pkgsStatic's own stdenv is what sets
        # CC to the musl compiler; a plain mkShell would set it back to gcc.)
        musl = pkgs.pkgsStatic.mkShell {
          packages = [ pkgs.go pkgs.file ];
          CGO_ENABLED = "1";
        };

        # A shell that builds for armv7 against musl, with qemu to run what it
        # builds (go test -exec qemu-arm).
        # The heap cache's header and duktape's value layout differ on 32-bit
        # ARM, so the cache's tests are worth running there too.
        armv7 = pkgs.mkShellNoCC {
          packages = [ pkgs.go pkgs.qemu-user ];
          GOARCH = "arm";
          GOARM = "7";
          CGO_ENABLED = "1";
          CC = "${armv7CC}/bin/armv7l-linux-musleabihf-gcc";
        };
      };
    };
}
