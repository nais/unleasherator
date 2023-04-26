{
  description = "My haskell application";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs";
    flake-utils.url = "github:numtide/flake-utils";
    gitignore = {
      url = "github:hercules-ci/gitignore.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };

  };

  outputs = { self, nixpkgs, flake-utils, gitignore }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        kubetools = let
          nixToKubebuilderTools = let
            platformMapping = {
              x86_64-linux = "linux-amd64";
              aarch64-linux = "linux-arm64";
              x86_64-darwin = "darwin-amd64";
              aarch64-darwin = "darwin-arm64";
            };
          in builtins.getAttr system platformMapping;
        in pkgs.fetchzip {
          url =
            #this needs a per-system mapping to google names
            "https://storage.googleapis.com/kubebuilder-tools/kubebuilder-tools-1.25.0-${nixToKubebuilderTools}.tar.gz";
          sha256 = "sha256-TLTeAE8rzf5iz+mraaoOIGCDTmmZiDygLhIbVNmMPbE=";
        };
        unleash = pkgs.buildGoModule {
          name = "unleasherator";
          nativeBuildInputs = [ pkgs.kubebuilder kubetools ];
          KUBEBUILDER_ASSETS = "${kubetools}/bin";
          src = gitignore.lib.gitignoreSource ./.;
          vendorSha256 = "sha256-XrwMUAjsGCIJQt2wu53VD7F3XUR8Gq7GEtxH4EJnXcM=";
        };
        helmify = pkgs.buildGoModule {
          name = "helmify";
          src = pkgs.fetchFromGitHub {
            owner = "arttor";
            repo = "helmify";
            rev = "9e709ee1587ab637bf811837213670c1f1125ba4";
            sha256 = "sha256-7BYarYamPAPx9kmaGyJt9Kd6kwIw99loSLY0vIyexy8=";
          };
          vendorSha256 = "sha256-6Tue+5KaLvjc+6ql/gXVhnQzc7gWQA5YtwXNKhCI6Os=";

        };
        dockerImage = pkgs.dockerTools.buildLayeredImage {
          name = "unleasherator";
          contents = [ unleash ];
          config = { Entrypoint = [ "${unleash}/bin/unleasherator" ]; };
        };
      in {
        defaultPackage = unleash;
        docker = dockerImage;
        devShell = pkgs.mkShell {
          packages = with pkgs; [
            go_1_20
            gotools
            gopls
            kustomize
            helmify
            kubernetes-controller-tools
          ];
        };
        kubetools = kubetools;
      });
}
