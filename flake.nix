{
  description = "go-duktape: Go bindings for the duktape JavaScript engine";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.05";

  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
    in
    {
      devShells.${system} = {
        default = pkgs.mkShell {
          packages = [ pkgs.go ];
        };

        # A shell whose C compiler links statically against musl, the libc
        # the idefix firmware uses. HEAP_CACHE.md's benchmark needs it: the
        # effect the heap's block cache avoids is musl's allocator's, and
        # does not show against glibc. (pkgsStatic's own stdenv is what sets
        # CC to the musl compiler; a plain mkShell would set it back to gcc.)
        musl = pkgs.pkgsStatic.mkShell {
          packages = [ pkgs.go pkgs.file ];
          CGO_ENABLED = "1";
        };
      };
    };
}
