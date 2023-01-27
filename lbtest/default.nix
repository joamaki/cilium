{ buildGoModule, bpftool }:

buildGoModule {
    pname = "lbtest";
    version = "0.0.0";
    src = ./..;
    subPackages = ["lbtest"];
    vendorHash = null;
    postInstall = ''
      mkdir $out/bpf
      cp $src/bpf/bpf_xdp.o $out/bpf
    '';

    propagatedBuildInputs = [ bpftool ];
}
