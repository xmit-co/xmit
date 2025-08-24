{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, flake-utils, nixpkgs }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
        {
          devShell = pkgs.mkShell {
            buildInputs = with pkgs; [ go ];
          };
          packages.default = pkgs.buildGoModule {
            pname = "xmit";
            version = "0.3.5";            src = ./.;
            vendorHash = "sha256-+X5e8GFobcbsJnm7h6fYo1A9Ukqkfd3wmQ6yETvDe+k=";
          };
        }
    );
}
