# containerdsnoop

Intercept gRPC traffic to [containerd](https://github.com/containerd/containerd) by passively captureing `AF_UNIX` packets with eBPF.

This program was originally written by [stwind](https://github.com/stwind) as [dockersnoop](https://github.com/stwind/dockersnoop). It was inspired [bcc](https://github.com/iovisor/bcc/)-based tools such as `xxxsnoop`, [sockdump](https://github.com/mechpen/sockdump), [grpc-snoop](https://github.com/nrc/grpc-snoop).

## Requirements

Tested with

* `Ubuntu==22.04`
* `containerd==1.7.1`
* `bcc==0.24.0`

Kubernetes `v1.27.1` was used to deploy the pod, although as long as the `containerd` version is the same, it should work with other versions.

## Setup

We assume you already have a Kubernetes cluster up and running. Then, on each node, install `go` and `bcc`:

```bash
curl -sLO https://dl.google.com/go/go1.20.4.linux-amd64.tar.gz
tar -C /usr/local -xzf go1.20.4.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/golang.sh

apt-get -yqq update && apt-get install -yqq python3-pip linux-headers-$(uname -r) bison build-essential \
  cmake flex g++ git libelf-dev zlib1g-dev libfl-dev systemtap-sdt-dev binutils-dev llvm-8-dev llvm-8-runtime \
  libclang-8-dev clang-8 arping netperf iperf3 python3-distutils
wget https://github.com/iovisor/bcc/releases/download/v0.24.0/bcc-src-with-submodule.tar.gz
pip install setuptools
tar xvf bcc-src-with-submodule.tar.gz && rm bcc-src-with-submodule.tar.gz
mkdir bcc/build; cd bcc/build
cmake -DPYTHON_CMD=python3 ..
make -j8 && make install && ldconfig
```

## Running

Due to the stateful nature of HTTP/2 [HPACK](https://http2.github.io/http2-spec/compression.html) header compression mechanism, one cannot capture the headers without snooping the entire connection. So we have to stop `dockerd` and `containerd` first.

```bash
systemctl stop containerd
```

Now we can start snooping. In a separate terminal, run:

```bash
go run main.go
COMM           PID    TID     PEER     TYPE   STREAM METHOD                                                  MESSAGE
```

Now we can restart `containerd`:

```bash
systemctl start containerd
```

We can see the interaction between `crictl`, `containerd` and `kubelet`:

```plaintext
COMM           PID     TID     PEER    TYPE   STREAM METHOD                                                  MESSAGE
kubelet        1732336 1732336 2991846 REQ    95     /runtime.v1.RuntimeService/ListPodSandbox               "&ListPodSandboxRequest{Filter:nil,}"
containerd     2991846 2991855 1732336 RESP   95     /runtime.v1.RuntimeService/ListPodSandbox               "&ListPodSandboxResponse{Items:[]*PodSandbox{&PodSandbox{Id:82328368a32b7ed47cb425332f053506ed3bae5c
kubelet        1732336 1732357 2991846 REQ    97     /runtime.v1.RuntimeService/ListContainers               "&ListContainersRequest{Filter:&ContainerFilter{Id:,State:nil,PodSandboxId:,LabelSelector:map[string
containerd     2991846 2991855 1732336 RESP   97     /runtime.v1.RuntimeService/ListContainers               "&ListContainersResponse{Containers:[]*Container{&Container{Id:5983f1bd0899c5518872bfdd03cf2a65634c7
crictl         2992048 2992051 2991846 REQ    1      /runtime.v1.RuntimeService/Version                      "&VersionRequest{Version:,}"
```

By deploying a pod on the node, we can see the API calls to `containerd`.
