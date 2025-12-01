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
            version = "0.5.0";  
            src = ./.;
            vendorHash = "sha256-beSUbtBgJHlXmgv+cLSOaGP4evXdxN0sOpasw+v8xgs=";
          };
        }
    );
}
