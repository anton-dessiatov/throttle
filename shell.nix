let pkgs = import <nixpkgs> {};
in
pkgs.stdenv.mkDerivation {
  name = "throttle-dev";

  buildInputs = [
    pkgs.go_1_12
    ];

  shellHook = ''
  export GO111MODULE=on
  unset GOPATH
  '';
}
