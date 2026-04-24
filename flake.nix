{
  outputs = inputs:
    inputs.flake-parts.lib.mkFlake {inherit inputs;} ({lib, ...}: {
      systems = inputs.nixpkgs.lib.platforms.all;

      perSystem = {
        config,
        inputs',
        pkgs,
        ...
      }: {
        packages = {
          lumen = inputs'.gomod2nix.legacyPackages.buildGoApplication {
            pname = "lumen";
            version = (builtins.fromJSON (builtins.readFile ./package.json)).version;

            inherit (pkgs) go;
            modules = ./gomod2nix.toml;

            src = lib.cleanSource ./.;
            subPackages = ["."];

            nativeBuildInputs = with pkgs; [
              pkg-config
            ];
            buildInputs = with pkgs; [
              sqlite.dev
            ];

            doCheck = false;
          };

          default = config.packages.lumen;
        };

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            inputs'.gomod2nix.legacyPackages.gomod2nix
            pkg-config
            sqlite
          ];
          buildInputs = with pkgs; [
            sqlite.dev
          ];
        };
      };
    });

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-parts = {
      url = "github:hercules-ci/flake-parts";
      inputs.nixpkgs-lib.follows = "nixpkgs";
    };

    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };
}
