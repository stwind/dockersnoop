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
```

Now we can restart `containerd`:

```bash
systemctl start containerd
```

We can see the interaction between `crictl`, `containerd` and `kubelet`:

```plaintext
TIMESTAMP       COMM           PID     TID     PEER    TYPE   STREAM METHOD                                                  MESSAGE
12:46:10.032477 kubelet        1732336 1732413 3135695 REQ    77     /runtime.v1.RuntimeService/ListContainers               "&ListContainersRequest{Filter:&ContainerFilter{Id:,State:nil,PodSandboxId:,LabelSelector:map[string
12:46:10.032691 containerd     3135695 3135697 1732336 RESP   77     /runtime.v1.RuntimeService/ListContainers               "&ListContainersResponse{Containers:[]*Container{&Container{Id:5983f1bd0899c5518872bfdd03cf2a65634c7
12:46:10.504193 crictl         3135858 3135860 3135695 REQ    1      /runtime.v1.RuntimeService/Version                      "&VersionRequest{Version:,}"
12:46:10.504573 containerd     3135695 3135697 3135858 RESP   1      /runtime.v1.RuntimeService/Version                      "&VersionResponse{Version:0.1.0,RuntimeName:containerd,RuntimeVersion:1.6.21,RuntimeApiVersion:v1,}"
12:46:10.506027 crictl         3135858 3135860 3135695 REQ    1      /runtime.v1.ImageService/ImageFsInfo                    "&ImageFsInfoRequest{}"
12:46:10.506390 containerd     3135695 3135697 3135858 RESP   1      /runtime.v1.ImageService/ImageFsInfo                    "&ImageFsInfoResponse{ImageFilesystems:[]*FilesystemUsage{&FilesystemUsage{Timestamp:168562356966191
12:46:10.507056 crictl         3135858 3135858 3135695 REQ    3      /runtime.v1.RuntimeService/ListContainers               "&ListContainersRequest{Filter:&ContainerFilter{Id:,State:&ContainerStateValue{State:CONTAINER_RUNNI
12:46:10.507344 containerd     3135695 3135697 3135858 RESP   3      /runtime.v1.RuntimeService/ListContainers               "&ListContainersResponse{Containers:[]*Container{&Container{Id:5983f1bd0899c5518872bfdd03cf2a65634c7
12:46:11.034421 kubelet        1732336 1736005 3135695 REQ    79     /runtime.v1.RuntimeService/ListPodSandbox               "&ListPodSandboxRequest{Filter:nil,}"
12:46:11.037227 containerd     3135695 3135697 1732336 RESP   79     /runtime.v1.RuntimeService/ListPodSandbox               "&ListPodSandboxResponse{Items:[]*PodSandbox{&PodSandbox{Id:82328368a32b7ed47cb425332f053506ed3bae5c
12:46:11.037337 kubelet        1732336 1736005 3135695 REQ    81     /runtime.v1.RuntimeService/ListContainers               "&ListContainersRequest{Filter:&ContainerFilter{Id:,State:nil,PodSandboxId:,LabelSelector:map[string
12:46:11.037820 containerd     3135695 3135704 1732336 RESP   81     /runtime.v1.RuntimeService/ListContainers               "&ListContainersResponse{Containers:[]*Container{&Container{Id:5983f1bd0899c5518872bfdd03cf2a65634c7
12:46:11.345306 kubelet        1732336 1736005 3135695 REQ    83     /runtime.v1.RuntimeService/ListPodSandbox               "&ListPodSandboxRequest{Filter:&PodSandboxFilter{Id:,State:&PodSandboxStateValue{State:SANDBOX_READY
12:46:11.345752 containerd     3135695 3135704 1732336 RESP   83     /runtime.v1.RuntimeService/ListPodSandbox               "&ListPodSandboxResponse{Items:[]*PodSandbox{&PodSandbox{Id:f1e510c1c80883e802e232147c70c96e476787f7
12:46:11.346954 kubelet        1732336 1736005 3135695 REQ    85     /runtime.v1.RuntimeService/ListContainers               "&ListContainersRequest{Filter:&ContainerFilter{Id:,State:&ContainerStateValue{State:CONTAINER_RUNNI
12:46:11.348123 containerd     3135695 3135697 1732336 RESP   85     /runtime.v1.RuntimeService/ListContainers               "&ListContainersResponse{Containers:[]*Container{&Container{Id:fda306477c7e62ff4191e59428b05399ba47b
```

By deploying a pod on the node, we can see the API calls to `containerd`:

```plaintext
12:46:13.897107 kubelet        1732336 1732357 3135695 REQ    1      /containerd.services.containers.v1.Containers/Get       "id:\"6d57be379426c29d07311258e3ecd0aa98996bd4918c9dc9ad65fd99125ef436\""
12:46:13.898589 containerd     3135695 3135701 1732336 RESP   1      /containerd.services.containers.v1.Containers/Get       "container:{id:\"6d57be379426c29d07311258e3ecd0aa98996bd4918c9dc9ad65fd99125ef436\"  labels:{key:\"i
12:46:13.899796 kubelet        1732336 1732357 3135695 REQ    3      /containerd.services.containers.v1.Containers/Get       "id:\"6d57be379426c29d07311258e3ecd0aa98996bd4918c9dc9ad65fd99125ef436\""
12:46:13.900594 containerd     3135695 3135916 1732336 RESP   3      /containerd.services.containers.v1.Containers/Get       "container:{id:\"6d57be379426c29d07311258e3ecd0aa98996bd4918c9dc9ad65fd99125ef436\"  labels:{key:\"i
12:46:13.902061 kubelet        1732336 1732357 3135695 REQ    5      /containerd.services.tasks.v1.Tasks/Get                 "container_id:\"6d57be379426c29d07311258e3ecd0aa98996bd4918c9dc9ad65fd99125ef436\""
12:46:13.941179 containerd     3135695 3135704 1732336 RESP   5      /containerd.services.tasks.v1.Tasks/Get                 "process:{id:\"6d57be379426c29d07311258e3ecd0aa98996bd4918c9dc9ad65fd99125ef436\"  pid:3135986  stat
12:46:13.955575 containerd     3135695 3135704 1732336 RESP   107    /runtime.v1.RuntimeService/RunPodSandbox                "&RunPodSandboxResponse{PodSandboxId:6d57be379426c29d07311258e3ecd0aa98996bd4918c9dc9ad65fd99125ef43
12:46:13.956311 kubelet        1732336 1732336 3135695 REQ    109    /runtime.v1.RuntimeService/PodSandboxStatus             "&PodSandboxStatusRequest{PodSandboxId:6d57be379426c29d07311258e3ecd0aa98996bd4918c9dc9ad65fd99125ef
12:46:13.956920 containerd     3135695 3135701 1732336 RESP   109    /runtime.v1.RuntimeService/PodSandboxStatus             "&PodSandboxStatusResponse{Status:&PodSandboxStatus{Id:6d57be379426c29d07311258e3ecd0aa98996bd4918c9
12:46:13.958038 kubelet        1732336 1732336 3135695 REQ    5      /runtime.v1.ImageService/ImageStatus                    "&ImageStatusRequest{Image:&ImageSpec{Image:registry-10-231-0-208.nip.io/mfranzil/5gb:1,Annotations:
12:46:13.958560 containerd     3135695 3135704 1732336 RESP   5      /runtime.v1.ImageService/ImageStatus                    "&ImageStatusResponse{Image:nil,Info:map[string]string{},}"
12:46:13.959852 kubelet        1732336 1736005 3135695 REQ    7      /runtime.v1.ImageService/PullImage                      "&PullImageRequest{Image:&ImageSpec{Image:registry-10-231-0-208.nip.io/mfranzil/5gb:1,Annotations:ma
```