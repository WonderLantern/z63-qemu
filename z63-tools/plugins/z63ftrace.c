/*
 * z63ftrace -- which functions run, for the first time, after a moment.
 *
 * SPDX-License-Identifier: GPL-2.0-or-later
 *
 * A capture that stops part-way leaves every task idle in its message loop:
 * nothing is blocked, the next step was simply never asked for. To see how
 * far a sequence got, this plugin takes a list of A53 function entries
 * (funcs=<file>, one address per line, as IDA lists them) and a trigger
 * (after=<pc>, optionally afterx0=<value> for the first argument): every
 * listed function's first call after the trigger is written as one line,
 *
 *   <us> cpu<n> first pc=0x40f62720 x0=0x5 x1=0x0 x2=0x0 from=0x40f7d078
 *
 * in order, with its first three arguments and return address; cap=<n>
 * (default 1) allows more calls per function. At exit each listed function
 * that ran after the trigger gets a summary line with its count and whether
 * it had run before the trigger ("new" when not). <us> is host time since
 * load, as in z63log.
 *
 *   use: -plugin z63-tools/plugins/libz63ftrace.dylib,funcs=f.txt,
 *        after=0x40fc4140,afterx0=1,out=trace.txt
 */
#include <glib.h>
#include <inttypes.h>
#include <stdio.h>
#include <string.h>
#include <qemu-plugin.h>

QEMU_PLUGIN_EXPORT int qemu_plugin_version = QEMU_PLUGIN_VERSION;

#define MAX_VCPUS 16

typedef struct Func {
    uint64_t pc;
    uint64_t before, after;
} Func;

static struct qemu_plugin_register *arg[MAX_VCPUS][3], *lr_reg[MAX_VCPUS];
static GHashTable *funcs;               /* pc -> Func */
static FILE *out;
static GMutex lock;
static int64_t t0;
static uint64_t trigger_pc, trigger_x0, cap = 1;
static bool trigger_x0_set, triggered;

static uint64_t reg_u64(struct qemu_plugin_register *r)
{
    g_autoptr(GByteArray) buf = g_byte_array_new();
    uint64_t v = 0;

    if (!r) {
        return 0;
    }
    qemu_plugin_read_register(r, buf);
    memcpy(&v, buf->data, MIN(buf->len, sizeof(v)));
    return v;
}

static void vcpu_init(qemu_plugin_id_t id, unsigned int idx)
{
    g_autoptr(GArray) regs = qemu_plugin_get_registers();

    if (idx >= MAX_VCPUS) {
        return;
    }
    for (guint i = 0; i < regs->len; i++) {
        qemu_plugin_reg_descriptor *d =
            &g_array_index(regs, qemu_plugin_reg_descriptor, i);

        for (int a = 0; a < 3; a++) {
            char x[4];

            snprintf(x, sizeof(x), "x%d", a);
            if (!strcmp(d->name, x)) {
                arg[idx][a] = d->handle;
            }
        }
        if (!strcmp(d->name, "x30")) {
            lr_reg[idx] = d->handle;
        }
    }
}

static void trigger_hit(unsigned int idx, void *udata)
{
    if (triggered || idx >= MAX_VCPUS || !arg[idx][0]) {
        return;
    }
    if (trigger_x0_set && reg_u64(arg[idx][0]) != trigger_x0) {
        return;
    }
    g_mutex_lock(&lock);
    if (!triggered) {
        triggered = true;
        fprintf(out, "%" PRId64 " cpu%u trigger pc=0x%" PRIx64 "\n",
                g_get_monotonic_time() - t0, idx, trigger_pc);
    }
    g_mutex_unlock(&lock);
}

static void func_hit(unsigned int idx, void *udata)
{
    Func *f = udata;

    if (idx >= MAX_VCPUS || !arg[idx][0]) {
        return;
    }
    if (!triggered) {
        f->before++;
        return;
    }
    if (f->after++ >= cap) {
        return;
    }
    g_mutex_lock(&lock);
    fprintf(out, "%" PRId64 " cpu%u first pc=0x%" PRIx64 " x0=0x%" PRIx64
            " x1=0x%" PRIx64 " x2=0x%" PRIx64 " from=0x%" PRIx64 "%s\n",
            g_get_monotonic_time() - t0, idx, f->pc, reg_u64(arg[idx][0]),
            reg_u64(arg[idx][1]), reg_u64(arg[idx][2]),
            reg_u64(lr_reg[idx]), f->before ? "" : " new");
    g_mutex_unlock(&lock);
}

static void tb_trans(qemu_plugin_id_t id, struct qemu_plugin_tb *tb)
{
    size_t n = qemu_plugin_tb_n_insns(tb);

    for (size_t i = 0; i < n; i++) {
        struct qemu_plugin_insn *insn = qemu_plugin_tb_get_insn(tb, i);
        uint64_t pc = qemu_plugin_insn_vaddr(insn);
        Func *f = g_hash_table_lookup(funcs, &pc);

        if (pc == trigger_pc) {
            qemu_plugin_register_vcpu_insn_exec_cb(
                insn, trigger_hit, QEMU_PLUGIN_CB_R_REGS, NULL);
        }
        if (f) {
            qemu_plugin_register_vcpu_insn_exec_cb(
                insn, func_hit, QEMU_PLUGIN_CB_R_REGS, f);
        }
    }
}

static void summary(gpointer key, gpointer value, gpointer data)
{
    Func *f = value;

    if (f->after) {
        fprintf(out, "summary pc=0x%" PRIx64 " after=%" PRIu64
                " before=%" PRIu64 "%s\n", f->pc, f->after, f->before,
                f->before ? "" : " new");
    }
}

static void at_exit(qemu_plugin_id_t id, void *p)
{
    g_hash_table_foreach(funcs, summary, NULL);
    if (out && out != stderr) {
        fclose(out);
    }
}

QEMU_PLUGIN_EXPORT int qemu_plugin_install(qemu_plugin_id_t id,
                                           const qemu_info_t *info,
                                           int argc, char **argv)
{
    const char *path = NULL, *list = NULL;
    g_autofree char *text = NULL;

    for (int i = 0; i < argc; i++) {
        if (g_str_has_prefix(argv[i], "out=")) {
            path = argv[i] + 4;
        } else if (g_str_has_prefix(argv[i], "funcs=")) {
            list = argv[i] + 6;
        } else if (g_str_has_prefix(argv[i], "after=")) {
            trigger_pc = g_ascii_strtoull(argv[i] + 6, NULL, 0);
        } else if (g_str_has_prefix(argv[i], "afterx0=")) {
            trigger_x0 = g_ascii_strtoull(argv[i] + 8, NULL, 0);
            trigger_x0_set = true;
        } else if (g_str_has_prefix(argv[i], "cap=")) {
            cap = g_ascii_strtoull(argv[i] + 4, NULL, 0);
        }
    }
    if (!list || !g_file_get_contents(list, &text, NULL, NULL)) {
        fprintf(stderr, "z63ftrace: funcs=<file> is required\n");
        return -1;
    }
    funcs = g_hash_table_new(g_int64_hash, g_int64_equal);
    for (char *line = strtok(text, "\n"); line; line = strtok(NULL, "\n")) {
        Func *f = g_new0(Func, 1);

        f->pc = g_ascii_strtoull(line, NULL, 0);
        if (f->pc) {
            g_hash_table_insert(funcs, &f->pc, f);
        } else {
            g_free(f);
        }
    }
    triggered = !trigger_pc;
    out = path ? fopen(path, "w") : stderr;
    if (!out) {
        return -1;
    }
    setvbuf(out, NULL, _IOLBF, 0);
    t0 = g_get_monotonic_time();
    fprintf(out, "# %u functions, trigger 0x%" PRIx64 "\n",
            g_hash_table_size(funcs), trigger_pc);
    qemu_plugin_register_vcpu_init_cb(id, vcpu_init);
    qemu_plugin_register_vcpu_tb_trans_cb(id, tb_trans);
    qemu_plugin_register_atexit_cb(id, at_exit, NULL);
    return 0;
}
