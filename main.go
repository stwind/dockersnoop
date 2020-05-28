package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"unsafe"

	bpf "github.com/iovisor/gobpf/bcc"
)

import "C"

const source = `
#include <linux/sched.h>
#include <linux/net.h>
#include <uapi/linux/un.h>
#include <net/af_unix.h>

struct packet {
	u32 pid;
	u32 peer_pid;
	u32 len;
	char comm[TASK_COMM_LEN];
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
	char *path;
	unsigned int n, match = 0;
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
	bpf_get_current_comm(&packet->comm, sizeof(packet->comm));
	packet->peer_pid = sock->sk->sk_peer_pid->numbers[0].nr;
	packet->len = len;

	events.perf_submit(ctx, packet, sizeof(struct packet));

	return 0;
}
`

type packet struct {
	Pid     uint32
	PeerPid uint32
	Len     uint32
	Comm    [16]byte
}

func makeFilter(path string) string {
	chars := []byte(path + "\000")
	comps := []string{}
	for i, b := range chars {
		comps = append(comps, fmt.Sprintf("*(path+%d) == %d", i, b))
	}
	return "if (" + strings.Join(comps, " && ") + ") match = 1;"
}

func main() {
	replaces := map[string]string{
		"__NUM_CPUS__": strconv.Itoa(runtime.NumCPU()),
		"__FILTER__":   makeFilter("/var/run/docker.sock"),
	}
	program := source
	for key, val := range replaces {
		program = strings.Replace(program, key, val, -1)
	}
	// fmt.Println(program)

	m := bpf.NewModule(program, []string{})
	defer m.Close()

	kprobe, err := m.LoadKprobe("probe_unix_stream_sendmsg")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load kprobe: %s\n", err)
		os.Exit(1)
	}

	if err := m.AttachKprobe("unix_stream_sendmsg", kprobe, -1); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to attach kprobe: %s\n", err)
		os.Exit(1)
	}

	table := bpf.NewTable(m.TableId("events"), m)

	channel := make(chan []byte, 1000)

	perfMap, err := bpf.InitPerfMap(table, channel, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init perf map: %s\n", err)
		os.Exit(1)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, os.Kill)

	go func() {
		var pkt packet

		for {
			data := <-channel

			if err := binary.Read(bytes.NewBuffer(data), bpf.GetHostByteOrder(), &pkt); err != nil {
				fmt.Printf("failed to decode received data: %s\n", err)
				continue
			}

			comm := C.GoString((*C.char)(unsafe.Pointer(&pkt.Comm)))
			fmt.Printf("pid %d peer %d len %d comm %-16s\n", pkt.Pid, pkt.PeerPid, pkt.Len, comm)
		}
	}()
	fmt.Println("started")

	perfMap.Start()
	<-sig
	perfMap.Stop()
}
