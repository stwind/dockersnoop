package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
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

const source = `
#include <linux/sched.h>
#include <linux/net.h>
#include <uapi/linux/un.h>
#include <net/af_unix.h>

#define SS_MAX_SEG_SIZE     __SS_MAX_SEG_SIZE__
#define SS_MAX_SEGS_PER_MSG __SS_MAX_SEGS_PER_MSG__

struct packet {
	u32 pid;
	u32 tid;
	u32 peer_pid;
	u32 len;
	u64 conn;
	char comm[TASK_COMM_LEN];
	char data[SS_MAX_SEG_SIZE];
};

BPF_ARRAY(packet_array, struct packet, __NUM_CPUS__);
BPF_PERF_OUTPUT(events);

int probe_unix_stream_sendmsg(struct pt_regs *ctx,
                              struct socket *sock,
                              struct msghdr *msg,
                              size_t len)
{
	struct packet *packet;
	struct unix_address *addr;
	char *path, *buf;
	unsigned int n, match = 0;
	struct iov_iter *iter;
	const struct kvec *iov;
	struct pid *peer_pid;

	addr = ((struct unix_sock *)sock->sk)->addr;
	path = addr->name[0].sun_path;
	__FILTER__

	addr = ((struct unix_sock *)((struct unix_sock *)sock->sk)->peer)->addr;
	path = addr->name[0].sun_path;
	__FILTER__

	if (match == 0)
		return 0;

	n = bpf_get_smp_processor_id();
	packet = packet_array.lookup(&n);
	if (packet == NULL)
			return 0;

	packet->pid = bpf_get_current_pid_tgid() >> 32;
	packet->tid = bpf_get_current_pid_tgid();
	bpf_get_current_comm(&packet->comm, sizeof(packet->comm));
	packet->peer_pid = sock->sk->sk_peer_pid->numbers[0].nr;
	packet->conn = (u64)sock;

	iter = &msg->msg_iter;
	iov = iter->kvec;

	#pragma unroll
	for (int i = 0; i < SS_MAX_SEGS_PER_MSG; i++) {
			if (i >= iter->nr_segs)
					break;

			packet->len = iov->iov_len;

			buf = iov->iov_base;
			n = iov->iov_len;

			bpf_probe_read(
					&packet->data,
					n > sizeof(packet->data) ? sizeof(packet->data) : n,
					buf);

			n += offsetof(struct packet, data);
			events.perf_submit(
					ctx,
					packet,
					n > sizeof(*packet) ? sizeof(*packet) : n);
			iov++;
	}

	return 0;
}
`

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
	comps := []string{}
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
	program := source
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
	framer := http2.NewFramer(ioutil.Discard, buffer)
	framer.MaxHeaderListSize = uint32(16 << 20)
	framer.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	return &actor{
		fr:  framer,
		buf: buffer,
	}
}

func run(channel chan []byte) {
	framers := make(map[uint64]*actor)
	paths := make(map[uint32]string)
	side := make(map[uint32]int)

	buffersReq := make(map[uint32]*bytes.Buffer)
	buffersResp := make(map[uint32]*bytes.Buffer)

	var pkt packet

	fmt.Printf("%-14s %-6s %-6s %-6s %-6s %-6s %-55s %s\n", "COMM", "PID", "TID", "PEER", "TYPE", "STREAM", "METHOD", "MESSAGE")
	header := "%-14s %-6d %-6d %-6d %-6s %-6d %-55s %s\n"

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
				if l > 100 {
					l = 100
				}
				fmt.Printf(header, comm, pkt.Pid, pkt.Tid, pkt.PeerPid, dType, id, path, msgStr[:l])
			default:
			}
		}

	}
}

func main() {
	address := flag.String("address", "/run/containerd/containerd.sock", "containerd sock file")
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

	go run(channel)

	perfMap.Start()
	<-sig
	perfMap.Stop()
}
