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