{
  description = "Example Go development environment for Zero to Nix";

  # Flake inputs
  inputs = {
    nixpkgs.url =
      "github:NixOS/nixpkgs/nixpkgs-unstable"; # also valid: "nixpkgs"
    gitignore = {
      url = "github:hercules-ci/gitignore.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  # Flake outputs
  outputs = { self, nixpkgs, gitignore }:
    let
      # Systems supported
      allSystems = [
        "x86_64-linux" # 64-bit Intel/AMD Linux
        "aarch64-linux" # 64-bit ARM Linux
        "x86_64-darwin" # 64-bit Intel macOS
        "aarch64-darwin" # 64-bit ARM macOS
      ];

      # Helper to provide system-specific attributes
      forAllSystems = f:
        nixpkgs.lib.genAttrs allSystems
        (system: f { pkgs = import nixpkgs { inherit system; }; });
    in {
      packages = forAllSystems ({ pkgs }: {
        default = pkgs.buildGoModule {
          name = "unleasherator";
          src = gitignore.lib.gitignoreSource ./.;
          vendorSha256 = "sha256-lbC6kqCeJFqq5HKvT9VY+pGNqwkAPfJB9LtkHqIeFnA=";
        };
      });

      devShells = forAllSystems ({ pkgs }: {
        default = pkgs.mkShell {
          # The Nix packages provided in the environment
          packages = with pkgs; [ go_1_20 gotools gopls ];
        };
      });
    };
}
