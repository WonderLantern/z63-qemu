/*
 * z63log -- the Z 6III firmwares' own logs, line by line, as they are written.
 *
 * SPDX-License-Identifier: GPL-2.0-or-later
 *
 * Neither processor prints a boot log. The RTOS's application layer logs to
 * binary rings in heap memory through one writer, sub_40C06924(object,
 * level, a, code, b) -- 84 modules' wrappers end there, among them the
 * camera controller's (sub_40F561E0) and the capture modules' trace -- and
 * the object's first u16 names the module. The kernel layer has TOPPERS
 * syslog, syslog_wri_log(prio, record) at 0x401842f8, which is fully
 * enabled and never called on a healthy boot. The body MCU keeps an error
 * ring through sub_80214C0(code, value). And a camera error -- the "Press
 * the shutter release button to reset" kind -- is posted through
 * sub_40EB1414(code, ...). This plugin hooks the entry of those four and
 * writes one line per call:
 *
 *   <seq> <us> cpu<n> log  mod=0x42 lvl=1 a=0x1 code=0x1ea7 b=0x0 from=0x40f3b2ec
 *   <seq> <us> cpu<n> syslog prio=3 fmt="..." args=0x.. 0x.. ...
 *   <seq> <us> cpu4  mcu  code=10002 value=0x0 from=0x8041f8d
 *   <seq> <us> cpu<n> error code=0x37 a=0x0 b=0x1 from=0x40f35c04
 *
 * <us> is host time in microseconds since the plugin loaded: good for order
 * and gaps, not guest time (QEMU's register list does not expose
 * CNTVCT_EL0, and plugins get no virtual clock). Codes are the firmware's
 * own numbers; what they mean is not decoded here. listregs=on names the
 * registers vCPU 0 offers, for extending the hooks.
 *
 *   build: z63-tools/plugins/build.sh   (needs a QEMU configured with
 *          -Dplugins=true; this tree's build has it)
 *   use:   -plugin z63-tools/plugins/libz63log.dylib,out=/tmp/z63log.txt
 *
 * call=0x40f36a08:0x... adds A53 function entries of your own, mcall= the
 * body MCU's (Thumb bit ignored); up to 32 in all. One "call" line each
 * time, the first four argument registers and the return address -- which
 * of a function's callers it was, without a debugger attached.
 */
#include <glib.h>
#include <inttypes.h>
#include <stdio.h>
#include <string.h>
#include <qemu-plugin.h>

QEMU_PLUGIN_EXPORT int qemu_plugin_version = QEMU_PLUGIN_VERSION;

enum { ARCH_UNKNOWN, ARCH_A53, ARCH_M4 };

typedef struct Hook {
    uint64_t pc;
    int arch;
    const char *what;
} Hook;

static const Hook hooks[] = {
    { 0x40c06924, ARCH_A53, "log" },     /* sub_40C06924 */
    { 0x401842f8, ARCH_A53, "syslog" },  /* syslog_wri_log */
    { 0x080214c0, ARCH_M4,  "mcu" },     /* sub_80214C0 */
    { 0x40eb1414, ARCH_A53, "error" },   /* sub_40EB1414: camera error */
};

#define MAX_VCPUS 16

typedef struct Vcpu {
    int arch;
    struct qemu_plugin_register *arg[5], *link;
} Vcpu;

static Vcpu vcpus[MAX_VCPUS];
static Hook calls[32];                  /* call=, mcall= entries */
static size_t n_calls;
static FILE *out;
static GMutex lock;
static uint64_t seq;
static int64_t t0;
static bool list_regs;                  /* listregs=on: name vCPU 0's */

static uint64_t reg_u64(struct qemu_plugin_register *r)
{
    g_autoptr(GByteArray) buf = g_byte_array_new();
    uint64_t v = 0;
    int n;

    if (!r) {
        return 0;
    }
    n = qemu_plugin_read_register(r, buf);
    if (n > 0) {
        memcpy(&v, buf->data, MIN((size_t)n, sizeof(v)));   /* LE host */
    }
    return v;
}

static bool mem_read(uint64_t addr, void *dst, size_t len)
{
    g_autoptr(GByteArray) buf = g_byte_array_new();

    if (!qemu_plugin_read_memory_vaddr(addr, buf, len) || buf->len < len) {
        return false;
    }
    memcpy(dst, buf->data, len);
    return true;
}

static void vcpu_init(qemu_plugin_id_t id, unsigned int idx)
{
    g_autoptr(GArray) regs = qemu_plugin_get_registers();
    Vcpu *v;

    if (idx >= MAX_VCPUS) {
        return;
    }
    v = &vcpus[idx];
    for (guint i = 0; i < regs->len; i++) {
        qemu_plugin_reg_descriptor *d =
            &g_array_index(regs, qemu_plugin_reg_descriptor, i);

        if (list_regs && idx == 0) {
            fprintf(out, "# reg %s (%s)\n", d->name,
                    d->feature ? d->feature : "-");
        }

        for (int a = 0; a < 5; a++) {
            char x[4], r[4];

            snprintf(x, sizeof(x), "x%d", a);
            snprintf(r, sizeof(r), "r%d", a);
            if (!strcmp(d->name, x)) {
                v->arg[a] = d->handle;
                v->arch = ARCH_A53;
            } else if (!strcmp(d->name, r)) {
                v->arg[a] = d->handle;
                if (v->arch == ARCH_UNKNOWN) {
                    v->arch = ARCH_M4;
                }
            }
        }
        if (!strcmp(d->name, "x30") || !strcmp(d->name, "lr")) {
            v->link = d->handle;
        }
    }
}

static void hit(unsigned int idx, void *udata)
{
    const Hook *h = udata;
    Vcpu *v = idx < MAX_VCPUS ? &vcpus[idx] : NULL;
    uint64_t a[5], from;
    char ts[24];

    if (!v || v->arch != h->arch) {
        return;
    }
    for (int i = 0; i < 5; i++) {
        a[i] = reg_u64(v->arg[i]);
    }
    from = reg_u64(v->link);
    snprintf(ts, sizeof(ts), "%" PRId64, g_get_monotonic_time() - t0);

    g_mutex_lock(&lock);
    if (!strcmp(h->what, "log")) {
        uint16_t mod = 0;

        mem_read(a[0], &mod, sizeof(mod));
        fprintf(out, "%" PRIu64 " %s cpu%u log mod=0x%x lvl=%" PRIu64
                " a=0x%" PRIx64 " code=0x%" PRIx64 " b=0x%" PRIx64
                " from=0x%" PRIx64 "\n", seq++, ts, idx, mod, a[1],
                a[2] & 0xffffffff, a[3] & 0xffffffff, a[4] & 0xffffffff,
                from);
    } else if (!strcmp(h->what, "syslog")) {
        /* TOPPERS SYSLOG: logtype, logtim, prcid, then loginfo[], whose
         * first word is the format string for the printf-style types. */
        uint64_t rec[9] = { 0 };
        char fmt[128] = "";

        mem_read(a[1], rec, sizeof(rec));
        if (rec[3] >= 0x40000000 && rec[3] < 0x50000000) {
            mem_read(rec[3], fmt, sizeof(fmt) - 1);
            fmt[sizeof(fmt) - 1] = 0;
            for (char *c = fmt; *c; c++) {
                if (*c == '\n' || *c == '\r') {
                    *c = ' ';
                }
            }
        }
        fprintf(out, "%" PRIu64 " %s cpu%u syslog prio=%" PRIu64
                " type=0x%" PRIx64 " fmt=\"%s\" args=0x%" PRIx64 " 0x%" PRIx64
                " 0x%" PRIx64 " 0x%" PRIx64 " from=0x%" PRIx64 "\n",
                seq++, ts, idx, a[0], rec[0], fmt, rec[4], rec[5], rec[6],
                rec[7], from);
    } else if (!strcmp(h->what, "error")) {
        /* sub_40EB1414(code, flag, flag): the camera error a dialog shows
         * (sub_40F35AD4 is the usual caller). */
        fprintf(out, "%" PRIu64 " %s cpu%u error code=0x%" PRIx64
                " a=0x%" PRIx64 " b=0x%" PRIx64 " from=0x%" PRIx64 "\n",
                seq++, ts, idx, a[0] & 0xffffffff, a[1] & 0xff, a[2] & 0xff,
                from);
    } else if (!strcmp(h->what, "call")) {
        char r = h->arch == ARCH_A53 ? 'x' : 'r';

        fprintf(out, "%" PRIu64 " %s cpu%u call pc=0x%" PRIx64 " %c0=0x%"
                PRIx64 " %c1=0x%" PRIx64 " %c2=0x%" PRIx64 " %c3=0x%" PRIx64
                " from=0x%" PRIx64 "\n", seq++, ts, idx, h->pc, r, a[0], r,
                a[1], r, a[2], r, a[3], from);
    } else {
        fprintf(out, "%" PRIu64 " %s cpu%u mcu code=%" PRIu64
                " value=0x%" PRIx64 " from=0x%" PRIx64 "\n", seq++, ts, idx,
                a[0] & 0xffffffff, a[1] & 0xffffffff, from & 0xffffffff);
    }
    g_mutex_unlock(&lock);
}

static void tb_trans(qemu_plugin_id_t id, struct qemu_plugin_tb *tb)
{
    size_t n = qemu_plugin_tb_n_insns(tb);

    for (size_t i = 0; i < n; i++) {
        struct qemu_plugin_insn *insn = qemu_plugin_tb_get_insn(tb, i);
        uint64_t pc = qemu_plugin_insn_vaddr(insn);

        for (size_t k = 0; k < G_N_ELEMENTS(hooks); k++) {
            if (hooks[k].pc == pc) {
                qemu_plugin_register_vcpu_insn_exec_cb(
                    insn, hit, QEMU_PLUGIN_CB_R_REGS, (void *)&hooks[k]);
            }
        }
        for (size_t k = 0; k < n_calls; k++) {
            if (calls[k].pc == pc) {
                qemu_plugin_register_vcpu_insn_exec_cb(
                    insn, hit, QEMU_PLUGIN_CB_R_REGS, (void *)&calls[k]);
            }
        }
    }
}

static void at_exit(qemu_plugin_id_t id, void *p)
{
    if (out && out != stderr) {
        fclose(out);
    }
}

QEMU_PLUGIN_EXPORT int qemu_plugin_install(qemu_plugin_id_t id,
                                           const qemu_info_t *info,
                                           int argc, char **argv)
{
    const char *path = NULL;

    for (int i = 0; i < argc; i++) {
        if (g_str_has_prefix(argv[i], "out=")) {
            path = argv[i] + 4;
        } else if (!strcmp(argv[i], "listregs=on")) {
            list_regs = true;
        } else if (g_str_has_prefix(argv[i], "call=") ||
                   g_str_has_prefix(argv[i], "mcall=")) {
            bool m4 = argv[i][0] == 'm';
            g_auto(GStrv) pcs = g_strsplit(strchr(argv[i], '=') + 1, ":", -1);

            for (char **p = pcs; *p && n_calls < G_N_ELEMENTS(calls); p++) {
                calls[n_calls++] = (Hook) {
                    g_ascii_strtoull(*p, NULL, 0) & ~(uint64_t)(m4 ? 1 : 0),
                    m4 ? ARCH_M4 : ARCH_A53, "call"
                };
            }
        }
    }
    out = path ? fopen(path, "w") : stderr;
    if (!out) {
        return -1;
    }
    setvbuf(out, NULL, _IOLBF, 0);
    t0 = g_get_monotonic_time();
    for (size_t k = 0; k < G_N_ELEMENTS(hooks); k++) {
        fprintf(out, "# hook %s at 0x%" PRIx64 " (%s)\n", hooks[k].what,
                hooks[k].pc, hooks[k].arch == ARCH_A53 ? "A53" : "M4");
    }
    for (size_t k = 0; k < n_calls; k++) {
        fprintf(out, "# hook call at 0x%" PRIx64 " (%s)\n", calls[k].pc,
                calls[k].arch == ARCH_A53 ? "A53" : "M4");
    }
    qemu_plugin_register_vcpu_init_cb(id, vcpu_init);
    qemu_plugin_register_vcpu_tb_trans_cb(id, tb_trans);
    qemu_plugin_register_atexit_cb(id, at_exit, NULL);
    return 0;
}
