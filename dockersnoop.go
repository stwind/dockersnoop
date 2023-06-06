package main

import (
	"bytes"
	"embed"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	strings "strings"
	"time"
	"unsafe"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	containersapi "github.com/containerd/containerd/api/services/containers/v1"
	contentapi "github.com/containerd/containerd/api/services/content/v1"
	diffapi "github.com/containerd/containerd/api/services/diff/v1"
	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	imagesapi "github.com/containerd/containerd/api/services/images/v1"
	introspectionapi "github.com/containerd/containerd/api/services/introspection/v1"
	leasesapi "github.com/containerd/containerd/api/services/leases/v1"
	namespacesapi "github.com/containerd/containerd/api/services/namespaces/v1"
	snapshotsapi "github.com/containerd/containerd/api/services/snapshots/v1"
	tasksapi "github.com/containerd/containerd/api/services/tasks/v1"
	versionservice "github.com/containerd/containerd/api/services/version/v1"
	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes/empty"

	bpf "github.com/iovisor/gobpf/bcc"
)

import "C"

const (
	maxSegSize = 1024 * 50
)

// containerd 1.2.13 messages
var serviceMsgs = map[string][]proto.Message{
	"/containerd.services.containers.v1.Containers/Get":           {new(containersapi.GetContainerRequest), new(containersapi.GetContainerResponse)},
	"/containerd.services.containers.v1.Containers/List":          {new(containersapi.ListContainersRequest), new(containersapi.ListContainersResponse)},
	"/containerd.services.containers.v1.Containers/ListStream":    {new(containersapi.ListContainersRequest), new(containersapi.ListContainersResponse)},
	"/containerd.services.containers.v1.Containers/Create":        {new(containersapi.CreateContainerRequest), new(containersapi.CreateContainerResponse)},
	"/containerd.services.containers.v1.Containers/Update":        {new(containersapi.UpdateContainerRequest), new(containersapi.UpdateContainerResponse)},
	"/containerd.services.containers.v1.Containers/Delete":        {new(containersapi.DeleteContainerRequest), new(empty.Empty)},
	"/containerd.services.content.v1.Content/Info":                {new(contentapi.InfoRequest), new(contentapi.InfoResponse)},
	"/containerd.services.content.v1.Content/Update":              {new(contentapi.UpdateRequest), new(contentapi.UpdateResponse)},
	"/containerd.services.content.v1.Content/List":                {new(contentapi.ListContentRequest), new(contentapi.ListContentResponse)},
	"/containerd.services.content.v1.Content/Delete":              {new(contentapi.DeleteContentRequest), new(empty.Empty)},
	"/containerd.services.content.v1.Content/Read":                {new(contentapi.ReadContentRequest), new(contentapi.ReadContentResponse)},
	"/containerd.services.content.v1.Content/Status":              {new(contentapi.StatusRequest), new(contentapi.StatusResponse)},
	"/containerd.services.content.v1.Content/ListStatuses":        {new(contentapi.ListStatusesRequest), new(contentapi.ListStatusesResponse)},
	"/containerd.services.content.v1.Content/Write":               {new(contentapi.WriteContentRequest), new(contentapi.WriteContentResponse)},
	"/containerd.services.content.v1.Content/Abort":               {new(contentapi.AbortRequest), new(empty.Empty)},
	"/containerd.services.diff.v1.Diff/Apply":                     {new(diffapi.ApplyRequest), new(diffapi.ApplyResponse)},
	"/containerd.services.diff.v1.Diff/Diff":                      {new(diffapi.DiffRequest), new(diffapi.DiffResponse)},
	"/containerd.services.images.v1.Images/Get":                   {new(imagesapi.GetImageRequest), new(imagesapi.GetImageResponse)},
	"/containerd.services.images.v1.Images/List":                  {new(imagesapi.ListImagesRequest), new(imagesapi.ListImagesResponse)},
	"/containerd.services.images.v1.Images/Create":                {new(imagesapi.CreateImageRequest), new(imagesapi.CreateImageResponse)},
	"/containerd.services.images.v1.Images/Update":                {new(imagesapi.UpdateImageRequest), new(imagesapi.UpdateImageResponse)},
	"/containerd.services.images.v1.Images/Delete":                {new(imagesapi.DeleteImageRequest), new(empty.Empty)},
	"/containerd.services.introspection.v1.Introspection/Plugins": {new(introspectionapi.PluginsRequest), new(introspectionapi.PluginsResponse)},
	"/containerd.services.leases.v1.Leases/Create":                {new(leasesapi.CreateRequest), new(leasesapi.CreateRequest)},
	"/containerd.services.leases.v1.Leases/Delete":                {new(leasesapi.DeleteRequest), new(empty.Empty)},
	"/containerd.services.leases.v1.Leases/List":                  {new(leasesapi.ListRequest), new(leasesapi.ListResponse)},
	"/containerd.services.snapshots.v1.Snapshots/Prepare":         {new(snapshotsapi.PrepareSnapshotRequest), new(snapshotsapi.PrepareSnapshotResponse)},
	"/containerd.services.snapshots.v1.Snapshots/View":            {new(snapshotsapi.ViewSnapshotRequest), new(snapshotsapi.ViewSnapshotResponse)},
	"/containerd.services.snapshots.v1.Snapshots/Mounts":          {new(snapshotsapi.MountsRequest), new(snapshotsapi.MountsResponse)},
	"/containerd.services.snapshots.v1.Snapshots/Commit":          {new(snapshotsapi.CommitSnapshotRequest), new(empty.Empty)},
	"/containerd.services.snapshots.v1.Snapshots/Remove":          {new(snapshotsapi.RemoveSnapshotRequest), new(empty.Empty)},
	"/containerd.services.snapshots.v1.Snapshots/Stat":            {new(snapshotsapi.StatSnapshotRequest), new(snapshotsapi.StatSnapshotResponse)},
	"/containerd.services.snapshots.v1.Snapshots/Update":          {new(snapshotsapi.UpdateSnapshotRequest), new(snapshotsapi.UpdateSnapshotResponse)},
	"/containerd.services.snapshots.v1.Snapshots/List":            {new(snapshotsapi.ListSnapshotsRequest), new(snapshotsapi.ListSnapshotsResponse)},
	"/containerd.services.snapshots.v1.Snapshots/Usage":           {new(snapshotsapi.UsageRequest), new(snapshotsapi.UsageResponse)},
	"/containerd.services.namespaces.v1.Namespaces/Get":           {new(namespacesapi.GetNamespaceRequest), new(namespacesapi.GetNamespaceResponse)},
	"/containerd.services.namespaces.v1.Namespaces/List":          {new(namespacesapi.ListNamespacesRequest), new(namespacesapi.ListNamespacesResponse)},
	"/containerd.services.namespaces.v1.Namespaces/Create":        {new(namespacesapi.CreateNamespaceRequest), new(namespacesapi.CreateNamespaceResponse)},
	"/containerd.services.namespaces.v1.Namespaces/Update":        {new(namespacesapi.UpdateNamespaceRequest), new(namespacesapi.UpdateNamespaceResponse)},
	"/containerd.services.namespaces.v1.Namespaces/Delete":        {new(namespacesapi.DeleteNamespaceRequest), new(namespacesapi.DeleteNamespaceRequest)},
	"/containerd.services.events.v1.Events/Publish":               {new(eventsapi.PublishRequest), new(empty.Empty)},
	"/containerd.services.events.v1.Events/Foward":                {new(eventsapi.ForwardRequest), new(empty.Empty)},
	"/containerd.services.events.v1.Events/Subscribe":             {new(eventsapi.SubscribeRequest), new(eventsapi.Envelope)},
	"/containerd.services.version.v1.Version/Version":             {new(empty.Empty), new(versionservice.VersionResponse)},
	"/containerd.services.tasks.v1.Tasks/Create":                  {new(tasksapi.CreateTaskRequest), new(tasksapi.CreateTaskResponse)},
	"/containerd.services.tasks.v1.Tasks/Start":                   {new(tasksapi.StartRequest), new(tasksapi.StartResponse)},
	"/containerd.services.tasks.v1.Tasks/Delete":                  {new(tasksapi.DeleteTaskRequest), new(tasksapi.DeleteResponse)},
	"/containerd.services.tasks.v1.Tasks/DeleteProcess":           {new(tasksapi.DeleteProcessRequest), new(tasksapi.DeleteResponse)},
	"/containerd.services.tasks.v1.Tasks/Get":                     {new(tasksapi.GetRequest), new(tasksapi.GetResponse)},
	"/containerd.services.tasks.v1.Tasks/List":                    {new(tasksapi.ListTasksRequest), new(tasksapi.ListTasksResponse)},
	"/containerd.services.tasks.v1.Tasks/Kill":                    {new(tasksapi.KillRequest), new(empty.Empty)},
	"/containerd.services.tasks.v1.Tasks/Exec":                    {new(tasksapi.ExecProcessRequest), new(empty.Empty)},
	"/containerd.services.tasks.v1.Tasks/ResizePty":               {new(tasksapi.ResizePtyRequest), new(empty.Empty)},
	"/containerd.services.tasks.v1.Tasks/CloseIO":                 {new(tasksapi.CloseIORequest), new(empty.Empty)},
	"/containerd.services.tasks.v1.Tasks/Pause":                   {new(tasksapi.PauseTaskRequest), new(empty.Empty)},
	"/containerd.services.tasks.v1.Tasks/Resume":                  {new(tasksapi.ResumeTaskRequest), new(empty.Empty)},
	"/containerd.services.tasks.v1.Tasks/ListPids":                {new(tasksapi.ListPidsRequest), new(tasksapi.ListPidsResponse)},
	"/containerd.services.tasks.v1.Tasks/Checkpoint":              {new(tasksapi.CheckpointTaskRequest), new(tasksapi.CheckpointTaskResponse)},
	"/containerd.services.tasks.v1.Tasks/Update":                  {new(tasksapi.UpdateTaskRequest), new(empty.Empty)},
	"/containerd.services.tasks.v1.Tasks/Metrics":                 {new(tasksapi.MetricsRequest), new(tasksapi.MetricsResponse)},
	"/containerd.services.tasks.v1.Tasks/Wait":                    {new(tasksapi.WaitRequest), new(tasksapi.WaitResponse)},
	"/runtime.v1.RuntimeService/Version":                          {new(runtimeapi.VersionRequest), new(runtimeapi.VersionResponse)},
	"/runtime.v1.RuntimeService/RunPodSandbox":                    {new(runtimeapi.RunPodSandboxRequest), new(runtimeapi.RunPodSandboxResponse)},
	"/runtime.v1.RuntimeService/StopPodSandbox":                   {new(runtimeapi.StopPodSandboxRequest), new(runtimeapi.StopPodSandboxResponse)},
	"/runtime.v1.RuntimeService/RemovePodSandbox":                 {new(runtimeapi.RemovePodSandboxRequest), new(runtimeapi.RemovePodSandboxResponse)},
	"/runtime.v1.RuntimeService/PodSandboxStatus":                 {new(runtimeapi.PodSandboxStatusRequest), new(runtimeapi.PodSandboxStatusResponse)},
	"/runtime.v1.RuntimeService/ListPodSandbox":                   {new(runtimeapi.ListPodSandboxRequest), new(runtimeapi.ListPodSandboxResponse)},
	"/runtime.v1.RuntimeService/CreateContainer":                  {new(runtimeapi.CreateContainerRequest), new(runtimeapi.CreateContainerResponse)},
	"/runtime.v1.RuntimeService/StartContainer":                   {new(runtimeapi.StartContainerRequest), new(runtimeapi.StartContainerResponse)},
	"/runtime.v1.RuntimeService/StopContainer":                    {new(runtimeapi.StopContainerRequest), new(runtimeapi.StopContainerResponse)},
	"/runtime.v1.RuntimeService/RemoveContainer":                  {new(runtimeapi.RemoveContainerRequest), new(runtimeapi.RemoveContainerResponse)},
	"/runtime.v1.RuntimeService/ListContainers":                   {new(runtimeapi.ListContainersRequest), new(runtimeapi.ListContainersResponse)},
	"/runtime.v1.RuntimeService/ContainerStatus":                  {new(runtimeapi.ContainerStatusRequest), new(runtimeapi.ContainerStatusResponse)},
	"/runtime.v1.RuntimeService/UpdateContainerResources":         {new(runtimeapi.UpdateContainerResourcesRequest), new(runtimeapi.UpdateContainerResourcesResponse)},
	"/runtime.v1.RuntimeService/ReopenContainerLog":               {new(runtimeapi.ReopenContainerLogRequest), new(runtimeapi.ReopenContainerLogResponse)},
	"/runtime.v1.RuntimeService/ExecSync":                         {new(runtimeapi.ExecSyncRequest), new(runtimeapi.ExecSyncResponse)},
	"/runtime.v1.RuntimeService/Exec":                             {new(runtimeapi.ExecRequest), new(runtimeapi.ExecResponse)},
	"/runtime.v1.RuntimeService/Attach":                           {new(runtimeapi.AttachRequest), new(runtimeapi.AttachResponse)},
	"/runtime.v1.RuntimeService/PortForward":                      {new(runtimeapi.PortForwardRequest), new(runtimeapi.PortForwardResponse)},
	"/runtime.v1.RuntimeService/ContainerStats":                   {new(runtimeapi.ContainerStatsRequest), new(runtimeapi.ContainerStatsResponse)},
	"/runtime.v1.RuntimeService/ListContainerStats":               {new(runtimeapi.ListContainerStatsRequest), new(runtimeapi.ListContainerStatsResponse)},
	"/runtime.v1.RuntimeService/PodSandboxStats":                  {new(runtimeapi.PodSandboxStatsRequest), new(runtimeapi.PodSandboxStatsResponse)},
	"/runtime.v1.RuntimeService/ListPodSandboxStats":              {new(runtimeapi.ListPodSandboxStatsRequest), new(runtimeapi.ListPodSandboxStatsResponse)},
	"/runtime.v1.RuntimeService/UpdateRuntimeConfig":              {new(runtimeapi.UpdateRuntimeConfigRequest), new(runtimeapi.UpdateRuntimeConfigResponse)},
	"/runtime.v1.RuntimeService/Status":                           {new(runtimeapi.StatusRequest), new(runtimeapi.StatusResponse)},
	"/runtime.v1.RuntimeService/CheckpointContainer":              {new(runtimeapi.CheckpointContainerRequest), new(runtimeapi.CheckpointContainerResponse)},
	//	"/runtime.v1.RuntimeService/GetContainerEvents": {new(runtimeapi.GetEventsRequest), new(runtimeapi.stream ContainerEventResponse)},
	"/runtime.v1.RuntimeService/ListMetricDescriptors": {new(runtimeapi.ListMetricDescriptorsRequest), new(runtimeapi.ListMetricDescriptorsResponse)},
	"/runtime.v1.RuntimeService/ListPodSandboxMetrics": {new(runtimeapi.ListPodSandboxMetricsRequest), new(runtimeapi.ListPodSandboxMetricsResponse)},
	"/runtime.v1.ImageService/ListImages":              {new(runtimeapi.ListImagesRequest), new(runtimeapi.ListImagesResponse)},
	"/runtime.v1.ImageService/ImageStatus":             {new(runtimeapi.ImageStatusRequest), new(runtimeapi.ImageStatusResponse)},
	"/runtime.v1.ImageService/PullImage":               {new(runtimeapi.PullImageRequest), new(runtimeapi.PullImageResponse)},
	"/runtime.v1.ImageService/RemoveImage":             {new(runtimeapi.RemoveImageRequest), new(runtimeapi.RemoveImageResponse)},
	"/runtime.v1.ImageService/ImageFsInfo":             {new(runtimeapi.ImageFsInfoRequest), new(runtimeapi.ImageFsInfoResponse)},
}

type packet struct {
	Pid     uint32
	Tid     uint32
	PeerPid uint32
	Len     uint32
	Conn    uint64
	Comm    [16]byte
}

const (
	packetSize = int(unsafe.Sizeof(packet{}))
)

func makeFilter(path string) string {
	chars := []byte(path + "\000")
	comps := make([]string, 0)
	for i, b := range chars {
		comps = append(comps, fmt.Sprintf("*(path+%d) == %d", i, b))
	}
	return "if (" + strings.Join(comps, " && ") + ") match = 1;"
}

func newProgram(address string) string {
	replaces := map[string]string{
		"__NUM_CPUS__":            strconv.Itoa(runtime.NumCPU()),
		"__FILTER__":              makeFilter(address),
		"__SS_MAX_SEG_SIZE__":     strconv.Itoa(maxSegSize),
		"__SS_MAX_SEGS_PER_MSG__": "10",
	}
	// read program from file
	var f embed.FS
	program_bytes, err := f.ReadFile("bpf/filter.c")
	if err != nil {
		log.Fatalf("Failed to read program: %s\n", err)
	}
	program := string(program_bytes)

	for key, val := range replaces {
		program = strings.Replace(program, key, val, -1)
	}
	return program
}

func attachKprobe(m *bpf.Module) {
	kprobe, err := m.LoadKprobe("probe_unix_stream_sendmsg")
	if err != nil {
		log.Fatalf("Failed to load kprobe: %s\n", err)
	}

	if err := m.AttachKprobe("unix_stream_sendmsg", kprobe, -1); err != nil {
		log.Fatalf("Failed to attach kprobe: %s\n", err)
	}
}

func drainClientPreface(p []byte) []byte {
	for i, c := range http2.ClientPreface {
		if i >= len(p) || byte(c) != p[i] {
			return p
		}
	}
	return p[len(http2.ClientPreface):]
}

type actor struct {
	fr  *http2.Framer
	buf *bytes.Buffer
}

func newFramer() *actor {
	buffer := bytes.NewBuffer([]byte{})
	framer := http2.NewFramer(io.Discard, buffer)
	framer.MaxHeaderListSize = uint32(16 << 20)
	framer.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	return &actor{
		fr:  framer,
		buf: buffer,
	}
}

func run(channel chan []byte, completeContent bool) {
	framers := make(map[uint64]*actor)
	paths := make(map[uint32]string)
	side := make(map[uint32]int)

	buffersReq := make(map[uint32]*bytes.Buffer)
	buffersResp := make(map[uint32]*bytes.Buffer)

	var pkt packet

	header := "%-17s %-14s %-6d %-6d %-6d %-6s %-6d %-55s %s\n"
	fmt.Printf(strings.Replace(header, "d", "s", -1),
		"TIMESTAMP", "COMM", "PID", "TID", "PEER", "TYPE", "STREAM", "METHOD", "MESSAGE")

	for {
		data := <-channel

		// packet struct
		if err := binary.Read(bytes.NewBuffer(data[:packetSize]), bpf.GetHostByteOrder(), &pkt); err != nil {
			fmt.Printf("failed to decode packet: %s\n", err)
			continue
		}
		comm := C.GoString((*C.char)(unsafe.Pointer(&pkt.Comm)))

		rest := data[packetSize : packetSize+int(pkt.Len)]
		rest = drainClientPreface(rest)

		if _, ok := framers[pkt.Conn]; !ok {
			framers[pkt.Conn] = newFramer()
		}
		framer := framers[pkt.Conn]
		framer.buf.Write(rest)

		for {
			frame, err := framer.fr.ReadFrame()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				} else {
					log.Printf("failed to decode http2 frame: %s\n", err)
					continue
				}
			}

			id := frame.Header().StreamID

			switch frame := frame.(type) {
			case *http2.MetaHeadersFrame:
				for _, hf := range frame.Fields {
					// log.Printf("stream: %d, header: %s, fields: %s\n", id, hf.Name, hf.Value)
					if hf.Name == ":path" {
						side[id] = 0
						paths[id] = hf.Value
					} else if hf.Name == ":status" {
						side[id] = 1
					}
				}

			case *http2.DataFrame:
				path, ok := paths[id]
				if !ok {
					log.Printf("missing path for stream: %d\n", id)
					break
				}

				var buf []byte
				b := frame.Data()

				if side[id] == 0 {
					if buffer, ok := buffersReq[id]; ok {
						buffer.Write(b)
						buf = buffer.Bytes()
						delete(buffersReq, id)
					} else {
						buf = b[5:]
					}
				} else {
					if buffer, ok := buffersResp[id]; ok {
						buffer.Write(b)
						buf = buffer.Bytes()
						delete(buffersReq, id)
					} else {
						buf = b[5:]
					}
				}

				if _, ok := serviceMsgs[path]; !ok {
					log.Printf("missing service for path: %s\n", path)
					break
				}

				msg := proto.Clone(serviceMsgs[path][side[id]])

				if err := proto.Unmarshal(buf, msg); err != nil {
					if err == io.ErrUnexpectedEOF {
						// frame splited in two packets, current in a buffer for later
						tmp := make([]byte, len(buf))
						copy(tmp, buf)
						if side[id] == 0 {
							buffersReq[id] = bytes.NewBuffer(tmp)
						} else {
							buffersResp[id] = bytes.NewBuffer(tmp)
						}
					}

					break
				}

				dType := "REQ"
				if side[id] == 1 {
					dType = "RESP"
				}

				msgStr := fmt.Sprintf("%q", msg)
				l := len(msgStr)
				if !completeContent && l > 100 {
					l = 100
				}
				commz := strings.Replace(comm, "\x00", "", -1)
				// format idx as int32

				now := time.Now().Format("15:04:05.000000")
				fmt.Printf(header, now, commz,
					pkt.Pid, pkt.Tid, pkt.PeerPid, dType, id, path, msgStr[:l])
			default:
			}
		}

	}
}

func main() {
	address := flag.String("address", "/run/containerd/containerd.sock", "containerd sock file")
	completeContent := flag.Bool("complete_content", false, "complete content")
	flag.Parse()

	program := newProgram(*address)

	m := bpf.NewModule(program, []string{})
	defer m.Close()

	attachKprobe(m)

	channel := make(chan []byte, 1000)
	perfMap, err := bpf.InitPerfMap(bpf.NewTable(m.TableId("events"), m), channel, nil)
	if err != nil {
		log.Fatalf("Failed to init perf map: %s\n", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)

	go run(channel, *completeContent)

	perfMap.Start()
	<-sig
	perfMap.Stop()
}
