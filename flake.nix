{
  description = "Go dev environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = import nixpkgs { inherit system; };
    in
    {
      devShells.${system}.default = pkgs.mkShell {
        packages = with pkgs; [
          go_1_25
          gopls
          delve
          air
          git
          wrk
        ];

        shellHook = ''
          export GOPATH=$HOME/go
          export PATH=$GOPATH/bin:$PATH

          if ! command -v swag >/dev/null 2>&1; then
            echo "Installing swag..."
            go install github.com/swaggo/swag/cmd/swag@latest
          fi

          echo "Environment ready"
          go version
          swag --version
        '';
      };
    };
}
