package main

import (
	"bytes"
	"encoding/binary"
	"errors"
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
					// check size in args to make compiler/validator happy
					n > sizeof(packet->data) ? sizeof(packet->data) : n,
					buf);

			n += offsetof(struct packet, data);
			events.perf_submit(
					ctx,
					packet,
					// check size in args to make compiler/validator happy
					n > sizeof(*packet) ? sizeof(*packet) : n);
			iov++;
	}

	return 0;
}
`

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

func newProgram() string {
	replaces := map[string]string{
		"__NUM_CPUS__": strconv.Itoa(runtime.NumCPU()),
		// "__FILTER__":   makeFilter("/var/run/docker.sock"),
		"__FILTER__":              makeFilter("/run/containerd/containerd.sock"),
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
		fmt.Fprintf(os.Stderr, "Failed to load kprobe: %s\n", err)
		os.Exit(1)
	}

	if err := m.AttachKprobe("unix_stream_sendmsg", kprobe, -1); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to attach kprobe: %s\n", err)
		os.Exit(1)
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

func main() {
	program := newProgram()

	m := bpf.NewModule(program, []string{})
	defer m.Close()

	attachKprobe(m)

	channel := make(chan []byte, 1000)
	perfMap, err := bpf.InitPerfMap(bpf.NewTable(m.TableId("events"), m), channel, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init perf map: %s\n", err)
		os.Exit(1)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)

	go func() {
		framers := make(map[uint64]*actor)
		paths := make(map[uint32]string)

		var pkt packet

		for {
			data := <-channel

			// packet struct
			if err := binary.Read(bytes.NewBuffer(data[:packetSize]), bpf.GetHostByteOrder(), &pkt); err != nil {
				fmt.Printf("failed to decode packet: %s\n", err)
				continue
			}
			comm := C.GoString((*C.char)(unsafe.Pointer(&pkt.Comm)))
			fmt.Printf("pid=%d,tid=%d,peer=%d,comm=%s,conn=%d\n", pkt.Pid, pkt.Tid, pkt.PeerPid, comm, pkt.Conn)

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
							paths[id] = hf.Value
						}
					}

				case *http2.DataFrame:
					path := paths[id]
					log.Printf("stream: %d %s path=%s data %q", id, path, frame.Data())

				default:
				}
			}

		}
	}()

	fmt.Println("started")

	perfMap.Start()
	<-sig
	perfMap.Stop()
}
