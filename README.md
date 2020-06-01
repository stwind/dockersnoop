# dockersnoop

Intercept gRPC traffic between docker and containerd using eBPF. In the spirit of [bcc](https://github.com/iovisor/bcc/) `xxxsnoop` tools and inspired by [sockdump](https://github.com/mechpen/sockdump) and [grpc-snoop](https://github.com/nrc/grpc-snoop).

## Requirements

Tested with

* Ubuntu 18.04
* Docker 19.03
* containerd 1.2.13

## Setup

Use Vagrant and you are all set

```sh
$ vagrant up
$ vagrant ssh
```

## Running

Due to the stateful nature of HTTP/2 [HPACK](https://http2.github.io/http2-spec/compression.html) header compression mechanism, one cannot capture the headers without snooping the entire connection. So we have to stop `dockerd` and `containerd` first.

```sh
$ systemctl stop containerd
```

And restart `containerd`

```sh
$ containerd
```

Now we can start snooping

```sh
$ go run main.go
COMM           PID    TID    PEER   TYPE   STREAM METHOD                                                  MESSAGE
```

And start `dockerd` in another terminal

```sh
$ dockerd
```

We can see initial interaction between `dockerd` and `containerd`

```
COMM           PID    TID    PEER   TYPE   STREAM METHOD                                                  MESSAGE
dockerd        14768  14770  14455  REQ    1      /containerd.services.namespaces.v1.Namespaces/Get       "&GetNamespaceRequest{Name:moby,}"
containerd     14455  14462  14768  RESP   1      /containerd.services.namespaces.v1.Namespaces/Get       "&GetNamespaceResponse{Namespace:Namespace{Name:moby,Labels:map[string]string{},},}"
dockerd        14768  14776  14455  REQ    1      /containerd.services.namespaces.v1.Namespaces/Get       "&GetNamespaceRequest{Name:plugins.moby,}"
containerd     14455  14462  14768  RESP   1      /containerd.services.namespaces.v1.Namespaces/Get       "&GetNamespaceResponse{Namespace:Namespace{Name:plugins.moby,Labels:map[string]string{},},}"
dockerd        14768  14777  14455  REQ    3      /containerd.services.events.v1.Events/Subscribe         "&SubscribeRequest{Filters:[namespace==plugins.moby,topic~=|^/tasks/|],}"
dockerd        14768  14776  14455  REQ    3      /containerd.services.events.v1.Events/Subscribe         "&SubscribeRequest{Filters:[namespace==moby,topic~=|^/tasks/|],}"
dockerd        14768  14776  14455  REQ    5      /containerd.services.version.v1.Version/Version         ""
containerd     14455  14462  14768  RESP   5      /containerd.services.version.v1.Version/Version         "&VersionResponse{Version:1.2.13,Revision:7ad184331fa3e55e52b890ea95e65ba581ae3429,}"
dockerd        14768  14775  14455  REQ    7      /containerd.services.version.v1.Version/Version         ""
containerd     14455  14460  14768  RESP   7      /containerd.services.version.v1.Version/Version         "&VersionResponse{Version:1.2.13,Revision:7ad184331fa3e55e52b890ea95e65ba581ae3429,}"
```

Let's start a container and see what is going on

```sh
$ docker run -ti --rm alpine echo hello
Unable to find image 'alpine:latest' locally
latest: Pulling from library/alpine
df20fa9351a1: Pull complete
Digest: sha256:185518070891758909c9f839cf4ca393ee977ac378609f700f60a771a2dfe321
Status: Downloaded newer image for alpine:latest
hello
```

And we can see how `docker` and `containerd `talked to each other

```
dockerd        14768  14770  14455  REQ    9      /containerd.services.leases.v1.Leases/Create            "&CreateRequest{ID:206405101-C1nU,Labels:map[string]string{containerd.io/gc.expire: 2020-06-02T16:16
containerd     14455  14460  14768  RESP   9      /containerd.services.leases.v1.Leases/Create            "&CreateRequest{ID:\n\x0e206405101-C1nU\x12\v\b\xed\xd3\xd4\xf6\x05\x10\xfb\xe9\x8fc\x1a/\n\x17conta
dockerd        14768  14773  14455  REQ    11     /containerd.services.containers.v1.Containers/Create    "&CreateContainerRequest{Container:Container{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f
containerd     14455  14460  14768  RESP   11     /containerd.services.containers.v1.Containers/Create    "&CreateContainerResponse{Container:Container{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9
dockerd        14768  14912  14455  REQ    13     /containerd.services.leases.v1.Leases/Delete            "&DeleteRequest{ID:206405101-C1nU,Sync:false,}"
containerd     14455  14462  14768  RESP   13     /containerd.services.leases.v1.Leases/Delete            ""
dockerd        14768  14770  14455  REQ    15     /containerd.services.containers.v1.Containers/Get       "&GetContainerRequest{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,}"
containerd     14455  14460  14768  RESP   15     /containerd.services.containers.v1.Containers/Get       "&GetContainerResponse{Container:Container{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f47
dockerd        14768  14770  14455  REQ    17     /containerd.services.containers.v1.Containers/Get       "&GetContainerRequest{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,}"
containerd     14455  14462  14768  RESP   17     /containerd.services.containers.v1.Containers/Get       "&GetContainerResponse{Container:Container{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f47
dockerd        14768  14776  14455  REQ    19     /containerd.services.containers.v1.Containers/Get       "&GetContainerRequest{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,}"
containerd     14455  14462  14768  RESP   19     /containerd.services.containers.v1.Containers/Get       "&GetContainerResponse{Container:Container{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f47
dockerd        14768  14773  14455  REQ    21     /containerd.services.containers.v1.Containers/Get       "&GetContainerRequest{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,}"
containerd     14455  14462  14768  RESP   21     /containerd.services.containers.v1.Containers/Get       "&GetContainerResponse{Container:Container{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f47
dockerd        14768  14773  14455  REQ    23     /containerd.services.tasks.v1.Tasks/Create              "&CreateTaskRequest{ContainerID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,Roo
containerd     14455  14460  14768  RESP   3      /containerd.services.events.v1.Events/Subscribe         "&Envelope{Timestamp:2020-06-01 16:16:45.694094644 +0000 UTC,Namespace:moby,Topic:/tasks/create,Even
containerd     14455  14565  14768  RESP   23     /containerd.services.tasks.v1.Tasks/Create              "&CreateTaskResponse{ContainerID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,Pi
dockerd        14768  14770  14455  REQ    25     /containerd.services.tasks.v1.Tasks/Start               "&StartRequest{ContainerID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,ExecID:,
containerd     14455  14565  14768  RESP   3      /containerd.services.events.v1.Events/Subscribe         "&Envelope{Timestamp:2020-06-01 16:16:45.706865362 +0000 UTC,Namespace:moby,Topic:/tasks/start,Event
containerd     14455  14565  14768  RESP   25     /containerd.services.tasks.v1.Tasks/Start               "&StartResponse{Pid:14962,}"
dockerd        14768  14773  14455  REQ    27     /containerd.services.containers.v1.Containers/Get       "&GetContainerRequest{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,}"
containerd     14455  14565  14768  RESP   27     /containerd.services.containers.v1.Containers/Get       "&GetContainerResponse{Container:Container{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f47
dockerd        14768  14773  14455  REQ    29     /containerd.services.tasks.v1.Tasks/Get                 "&GetRequest{ContainerID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,ExecID:,}"
containerd     14455  14565  14768  RESP   29     /containerd.services.tasks.v1.Tasks/Get                 "&GetResponse{Process:&containerd_v1_types.Process{ContainerID:,ID:0d4b83534389f9594bb6f2b895c68c50d
dockerd        14768  14912  14455  REQ    31     /containerd.services.tasks.v1.Tasks/ResizePty           "&ResizePtyRequest{ContainerID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,Exec
containerd     14455  14565  14768  RESP   31     /containerd.services.tasks.v1.Tasks/ResizePty           ""
containerd     15006  15012  14455  REQ    1      /containerd.services.events.v1.Events/Publish           "&PublishRequest{Topic:/tasks/exit,Event:&google_protobuf1.Any{TypeUrl:containerd.events.TaskExit,Va
containerd     14455  14565  14768  RESP   3      /containerd.services.events.v1.Events/Subscribe         "&Envelope{Timestamp:2020-06-01 16:16:45.760203741 +0000 UTC,Namespace:moby,Topic:/tasks/exit,Event:
containerd     14455  14565  15006  RESP   1      /containerd.services.events.v1.Events/Publish           ""
dockerd        14768  14770  14455  REQ    33     /containerd.services.containers.v1.Containers/Get       "&GetContainerRequest{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,}"
containerd     14455  14565  14768  RESP   33     /containerd.services.containers.v1.Containers/Get       "&GetContainerResponse{Container:Container{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f47
dockerd        14768  14773  14455  REQ    35     /containerd.services.tasks.v1.Tasks/Get                 "&GetRequest{ContainerID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,ExecID:,}"
containerd     14455  14565  14768  RESP   35     /containerd.services.tasks.v1.Tasks/Get                 "&GetResponse{Process:&containerd_v1_types.Process{ContainerID:,ID:0d4b83534389f9594bb6f2b895c68c50d
dockerd        14768  14773  14455  REQ    37     /containerd.services.tasks.v1.Tasks/Get                 "&GetRequest{ContainerID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,ExecID:,}"
containerd     14455  14565  14768  RESP   37     /containerd.services.tasks.v1.Tasks/Get                 "&GetResponse{Process:&containerd_v1_types.Process{ContainerID:,ID:0d4b83534389f9594bb6f2b895c68c50d
dockerd        14768  14773  14455  REQ    39     /containerd.services.tasks.v1.Tasks/Delete              "&DeleteTaskRequest{ContainerID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,}"
containerd     14455  14929  14768  RESP   39     /containerd.services.tasks.v1.Tasks/Delete              "&DeleteResponse{ID:,Pid:14962,ExitStatus:0,ExitedAt:2020-06-01 16:16:45.7326066 +0000 UTC,}"
containerd     14455  14929  14768  RESP   3      /containerd.services.events.v1.Events/Subscribe         "&Envelope{Timestamp:2020-06-01 16:16:45.787312911 +0000 UTC,Namespace:moby,Topic:/tasks/delete,Even
dockerd        14768  14776  14455  REQ    41     /containerd.services.containers.v1.Containers/Get       "&GetContainerRequest{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,}"
containerd     14455  14929  14768  RESP   41     /containerd.services.containers.v1.Containers/Get       "&GetContainerResponse{Container:Container{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f47
dockerd        14768  14776  14455  REQ    43     /containerd.services.tasks.v1.Tasks/Get                 "&GetRequest{ContainerID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,ExecID:,}"
dockerd        14768  14912  14455  REQ    45     /containerd.services.containers.v1.Containers/Get       "&GetContainerRequest{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,}"
containerd     14455  14565  14768  RESP   45     /containerd.services.containers.v1.Containers/Get       "&GetContainerResponse{Container:Container{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f47
dockerd        14768  14776  14455  REQ    47     /containerd.services.containers.v1.Containers/Get       "&GetContainerRequest{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,}"
containerd     14455  14462  14768  RESP   47     /containerd.services.containers.v1.Containers/Get       "&GetContainerResponse{Container:Container{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f47
dockerd        14768  14776  14455  REQ    49     /containerd.services.tasks.v1.Tasks/Get                 "&GetRequest{ContainerID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,ExecID:,}"
dockerd        14768  14776  14455  REQ    51     /containerd.services.containers.v1.Containers/Get       "&GetContainerRequest{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,}"
dockerd        14768  14776  14455  REQ    53     /containerd.services.containers.v1.Containers/Delete    "&DeleteContainerRequest{ID:0d4b83534389f9594bb6f2b895c68c50dd7d089d9952bba97c9f478c77190405,}"
containerd     14455  14464  14768  RESP   53     /containerd.services.containers.v1.Containers/Delete    ""
```

