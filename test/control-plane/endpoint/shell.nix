with import <nixpkgs> {};
stdenv.mkDerivation {
  name = "env";
  nativeBuildInputs = [
    bashInteractive
  ];
  buildInputs = [
    delve
    glibc
    glibc.static
  ];
  hardeningDisable = ["all"];
}
