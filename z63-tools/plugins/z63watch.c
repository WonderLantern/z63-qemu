/*
 * z63watch -- who writes a range of guest memory, and what.
 *
 * SPDX-License-Identifier: GPL-2.0-or-later
 *
 * For code in [pclo, pchi), every store whose virtual address falls in
 * [lo, hi) is written out as one line: host microseconds, vCPU, PC,
 * address, size and value. Each (pc, address) pair is reported at most
 * max= times (default 4), so a store in a loop does not flood the file.
 * Restricting the code range keeps the cost to the instructions that can
 * matter -- the body MCU's flash, say, with the A53s untouched.
 *
 *   -plugin libz63watch.dylib,lo=0x20014150,hi=0x20014190,
 *           pclo=0x08000000,pchi=0x08080000,out=/tmp/w.txt
 */
#include <glib.h>
#include <inttypes.h>
#include <stdio.h>
#include <string.h>
#include <qemu-plugin.h>

QEMU_PLUGIN_EXPORT int qemu_plugin_version = QEMU_PLUGIN_VERSION;

static uint64_t lo, hi, pclo, pchi = UINT64_MAX, maxper = 4;
static FILE *out;
static GMutex lock;
static GHashTable *seen;               /* (pc << 32 | addr low) -> count */
static int64_t t0;
static struct qemu_plugin_register *lr_reg[16];   /* per vCPU: lr / x30 */

static uint64_t read_lr(unsigned int vcpu)
{
    g_autoptr(GByteArray) buf = g_byte_array_new();
    uint64_t v = 0;
    int n;

    if (vcpu >= 16 || !lr_reg[vcpu]) {
        return 0;
    }
    n = qemu_plugin_read_register(lr_reg[vcpu], buf);
    if (n > 0) {
        memcpy(&v, buf->data, MIN((size_t)n, sizeof(v)));
    }
    return v;
}

static void vcpu_init(qemu_plugin_id_t id, unsigned int idx)
{
    g_autoptr(GArray) regs = qemu_plugin_get_registers();

    for (guint i = 0; idx < 16 && i < regs->len; i++) {
        qemu_plugin_reg_descriptor *d =
            &g_array_index(regs, qemu_plugin_reg_descriptor, i);

        if (!strcmp(d->name, "lr") || !strcmp(d->name, "x30")) {
            lr_reg[idx] = d->handle;
        }
    }
}

static void store(unsigned int vcpu, qemu_plugin_meminfo_t info,
                  uint64_t vaddr, void *udata)
{
    uint64_t pc = (uintptr_t)udata;
    qemu_plugin_mem_value v;
    uint64_t val = 0, key;
    gpointer n;

    if (!qemu_plugin_mem_is_store(info) || vaddr < lo || vaddr >= hi) {
        return;
    }
    v = qemu_plugin_mem_get_value(info);
    switch (v.type) {
    case QEMU_PLUGIN_MEM_VALUE_U8:  val = v.data.u8;  break;
    case QEMU_PLUGIN_MEM_VALUE_U16: val = v.data.u16; break;
    case QEMU_PLUGIN_MEM_VALUE_U32: val = v.data.u32; break;
    case QEMU_PLUGIN_MEM_VALUE_U64: val = v.data.u64; break;
    default: break;
    }
    key = (pc << 32) ^ vaddr;
    g_mutex_lock(&lock);
    n = g_hash_table_lookup(seen, &key);
    if (GPOINTER_TO_UINT(n) < maxper) {
        uint64_t *k = g_new(uint64_t, 1);

        *k = key;
        g_hash_table_replace(seen, k, GUINT_TO_POINTER(GPOINTER_TO_UINT(n) + 1));
        fprintf(out, "%" PRId64 " cpu%u pc=0x%" PRIx64 " lr=0x%" PRIx64
                " addr=0x%" PRIx64 " size=%u val=0x%" PRIx64 "\n",
                g_get_monotonic_time() - t0, vcpu, pc, read_lr(vcpu), vaddr,
                1u << qemu_plugin_mem_size_shift(info), val);
        fflush(out);
    }
    g_mutex_unlock(&lock);
}

static void tb_trans(qemu_plugin_id_t id, struct qemu_plugin_tb *tb)
{
    size_t n = qemu_plugin_tb_n_insns(tb);

    for (size_t i = 0; i < n; i++) {
        struct qemu_plugin_insn *insn = qemu_plugin_tb_get_insn(tb, i);
        uint64_t pc = qemu_plugin_insn_vaddr(insn);

        if (pc >= pclo && pc < pchi) {
            qemu_plugin_register_vcpu_mem_cb(insn, store,
                                             QEMU_PLUGIN_CB_R_REGS,
                                             QEMU_PLUGIN_MEM_W,
                                             (void *)(uintptr_t)pc);
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
        g_auto(GStrv) kv = g_strsplit(argv[i], "=", 2);

        if (!kv[0] || !kv[1]) {
            continue;
        }
        if (!strcmp(kv[0], "out")) {
            path = argv[i] + 4;
        } else if (!strcmp(kv[0], "lo")) {
            lo = g_ascii_strtoull(kv[1], NULL, 0);
        } else if (!strcmp(kv[0], "hi")) {
            hi = g_ascii_strtoull(kv[1], NULL, 0);
        } else if (!strcmp(kv[0], "pclo")) {
            pclo = g_ascii_strtoull(kv[1], NULL, 0);
        } else if (!strcmp(kv[0], "pchi")) {
            pchi = g_ascii_strtoull(kv[1], NULL, 0);
        } else if (!strcmp(kv[0], "max")) {
            maxper = g_ascii_strtoull(kv[1], NULL, 0);
        }
    }
    out = path ? fopen(path, "w") : stderr;
    if (!out) {
        return -1;
    }
    seen = g_hash_table_new_full(g_int64_hash, g_int64_equal, g_free, NULL);
    t0 = g_get_monotonic_time();
    fprintf(out, "# stores to [0x%" PRIx64 ", 0x%" PRIx64 ") from code in "
            "[0x%" PRIx64 ", 0x%" PRIx64 ")\n", lo, hi, pclo, pchi);
    qemu_plugin_register_vcpu_init_cb(id, vcpu_init);
    qemu_plugin_register_vcpu_tb_trans_cb(id, tb_trans);
    qemu_plugin_register_atexit_cb(id, at_exit, NULL);
    return 0;
}
