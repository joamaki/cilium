let
  pkgs =
    let
      hostPkgs = import <nixpkgs> {};
      pinnedVersion = hostPkgs.lib.importJSON ./nixpkgs-version.json;
      pinnedPkgs = hostPkgs.fetchFromGitHub {
        owner = "NixOS";
  	repo = "nixpkgs";
  	inherit (pinnedVersion) rev sha256;
      };
    in import pinnedPkgs {};

in pkgs.runCommand "dummy" { buildInputs = with pkgs; [ go_1_18 gopls gcc ]; } ""
