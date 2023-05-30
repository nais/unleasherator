{
  description = "Unleasherator";

  inputs = {
    nixpkgs.url = # Pick a some commit
      "nixpkgs/583402b28da67550092dd40c55d8cec002443428";
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
        kubetools-1_27_1 = let
          nixToKubebuilderTools = let
            platformMapping = {
              x86_64-linux = "linux-amd64";
              aarch64-linux = "linux-arm64";
              x86_64-darwin = "darwin-amd64";
              aarch64-darwin = "darwin-arm64";
            };
            shaMapping = {
              aarch64-linux =
                "sha256-M9CgiHugLh7t7ulWGp4psRCtihBDxmBxqmSw5UHxKj4=";
              aarch64-darwin =
                "sha256-TLTeAE8rzf5iz+mraaoOIGCDTmmZiDygLhIbVNmMPbE=";
              x86_64-linux =
                "sha256-gJ/BvTbzKa8Wx2Hleyy2GEe+EOnlKvqT/6xuPu1nvB0=";
            };
          in {
            sha = builtins.getAttr system shaMapping;
            path = builtins.getAttr system platformMapping;
          };
        in pkgs.fetchzip {
          url =
            "https://storage.googleapis.com/kubebuilder-tools/kubebuilder-tools-1.27.1-${nixToKubebuilderTools.path}.tar.gz";
          sha256 = nixToKubebuilderTools.sha;
        };
        unleasherator = pkgs.buildGoModule {
          name = "unleasherator";
          nativeBuildInputs = [
            pkgs.kubebuilder
            kubetools-1_27_1
            pkgs.kubernetes-controller-tools
            helmify
            pkgs.kustomize
            pkgs.coreutils
          ];
          ##      preBuild = "make manifests && make generate && make helm";
          KUBEBUILDER_ASSETS = "${kubetools-1_27_1}/bin";
          src = gitignore.lib.gitignoreSource ./.;
          vendorSha256 = "sha256-Isnm/DHn54ql51V2EGSMGHeyVYdIeq/q/bmkEH8Ia0A=";
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
          contents = [ unleasherator ];
          config = {
            Entrypoint = [ "${unleasherator}/bin/unleasherator" ];
            User = "65532:65532";
          };

        };
        scripts = with pkgs; [
          (writeScriptBin "unleasherator-restart" ''
            kubectl rollout restart deployment/unleasherator-controller-manager -n unleasherator-system
            kubectl rollout status deployment/unleasherator-controller-manager -n unleasherator-system --timeout=60s
          '')

          (writeScriptBin "unleasherator-logs" ''
            kubectl logs deployment/unleasherator-controller-manager -n unleasherator-system -f
          '')

          (writeScriptBin "unleasherator-undeploy" ''
            kustomize build config/default | kubectl delete --ignore-not-found=$(ignore-not-found) -f -
          '')

          (writeScriptBin "unleasherator-uninstall" ''
            kustomize build config/crd | kubectl delete --ignore-not-found=$(ignore-not-found) -f -

          '')
          (writeScriptBin "unleasherator-install" ''
            kubectl logs deployment/unleasherator-controller-manager -n unleasherator-system -f
          '')

        ];
      in {
        defaultPackage = unleasherator;
        unleasherator = unleasherator;
        docker = dockerImage;
        devShell = pkgs.mkShell {
          packages = with pkgs; [
            go_1_20
            gotools
            gopls
            kustomize
            helmify
            kubernetes-controller-tools
            kubetools-1_27_1
            scripts
          ];
          shellHook = ''
            export KUBEBUILDER_ASSETS=${kubetools-1_27_1}/bin
          '';
        };
        kubetools = kubetools-1_27_1;
      });
}
