{
  description = "Check goontunes library cache against Spotify";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        python = pkgs.python3.withPackages (ps: [ ps.requests ps.spotipy ps.pygame ps.websockets ]);
      in
      {
        # `nix run` runs the library checker
        packages.default = pkgs.writeShellScriptBin "check-library" ''
          exec ${python}/bin/python3 ${./check_library.py} "$@"
        '';

        packages.unavailable-liked = pkgs.writeShellScriptBin "unavailable-liked" ''
          exec ${python}/bin/python3 ${./unavailable_liked.py} "$@"
        '';

        apps.default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/check-library";
        };

        apps.unavailable-liked = {
          type = "app";
          program = "${self.packages.${system}.unavailable-liked}/bin/unavailable-liked";
        };

        # `nix develop` drops you into a shell with python + requests available
        devShells.default = pkgs.mkShell {
          packages = [ python ];
        };
      });
}
