# Native Extensions — GNE

**GNE** (GaLang Native Extension) lets you write extensions in **C** — a
Redis client, a database driver, crypto, SMTP, anything — without touching
the GaLang compiler or runtime. The runtime stays Go; extensions are
loaded when `use` reaches them via `dlopen` (Linux/macOS) or `LoadLibrary`
(Windows).

> Working examples: the
> [galang-gne-examples](https://github.com/lnx645/galang-gne-examples)
> repo (`hello` and `kv`). Requirements: `gar` **0.5.0+** and gcc.

## Official extensions — `redis` and `smtp`

Two official extensions are compiled from the sources in this repo
(`ext/redis/`, `ext/smtp/`) and released as multi-platform zip assets.
Install them from the official channel (requires `gar` **0.6.0+**):

```bash
gar gne install redis          # latest release
gar gne install smtp@0.6.0     # pin a version
gar gne list                   # name, version, ABI, platforms
gar gne remove redis           # remove binary + sidecar
```

Other specs: `gar gne install ./redis.zip` (local file) or
`gar gne install https://.../redis.zip` (HTTPS required). Packages install
flat into `~/.galang/gne`, so `use "redis"` / `use "smtp"` finds them
right away — command details in
[CLI — Extension Manager](cli.md#extension-manager--gar-gne), full API in
[Standard Modules](modules.md).

Both require a CGO-enabled `gar` binary (see the [Platforms](#platforms)
table):

- **`redis`** — a RESP2 client with `connect`, `ping`, `get`, `set`, `del`,
  `exists`, `incr`, `expire`, `hset`, `hget`, `lpush`, `lrange`, `keys`,
  `cmd`, `close` plus connection timeouts.
- **`smtp`** — send mail over AUTH LOGIN: `connect`, `auth`, `from`, `to`,
  `subject`, `send`, `send_html`, `close`. STARTTLS/SSL are **not**
  included — read the limitations in
  [Standard Modules — smtp](modules.md#smtp--official-extension).

### Build them yourself

The full sources are in the repo — compile as in the
[Building](#building) table below and drop the result into `./gne/` or
`~/.galang/gne` (even without `pack`, `use` finds it). To package it as
a proper zip:

```bash
make ext                    # linux/amd64, linux/arm64, windows/amd64
make pack-ext               # dist/redis.zip and dist/smtp.zip
```

macOS cannot be cross-compiled from Linux (CGO), so darwin `.dylib`
binaries are built by GitHub Actions on every release tag. Two options:
compile directly on your Mac (`clang -dynamiclib ...`), or download
`ext-darwin-builds.zip` from the release page and run `make pack-ext` to
package the full set.

## Quick tour

```c
/* hello.c */
#include "gne.h"

static const gne_host_api *api;

static int add(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret) {
    int64_t a, b;
    (void)argc;
    if (api->get_int(ctx, argv[0], &a) != 0 ||
        api->get_int(ctx, argv[1], &b) != 0) {
        api->throw(ctx, "type_error", 500, "add() needs two integers");
        return 1;
    }
    *ret = api->int_new(ctx, a + b);
    return 0;
}

int gne_module_init(const gne_host_api *a, gne_ctx *ctx, gne_handle *out) {
    api = a;                              // keep api: valid for the process
    if (api->abi != GNE_ABI) {            // REQUIRED: reject a different ABI
        api->throw(ctx, "gne_abi", 500, "ABI mismatch");
        return 1;
    }
    api->define_fn(ctx, "add", 2, 2, add);
    *out = api->module(ctx);              // module namespace
    return 0;
}
```

```sh
gcc -shared -fPIC -I <dir of gne.h> -o hello.so hello.c   # macOS: -dynamiclib
mkdir -p gne && mv hello.so gne/
```

```galang
use "hello"
print(hello.add(2, 3))    // 5
```

## How `use` resolves

The order is unchanged: **builtin → `.ga` file → GNE** (neither parser nor
compiler changes at all). When the file step finds nothing, the interpreter
searches for a native extension in order:

1. `./gne/` — relative to the directory of the **file issuing `use`**
   (wins during project development);
2. each `$GNE_PATH` entry (`:` on Linux/macOS, `;` on Windows; relative
   entries resolve against the issuing file's directory);
3. `~/.galang/gne` — per-user global. The old `~/.garurda/gne` location
   (pre-v0.7.0) is still read so existing extensions keep working; new
   installs always write to `~/.galang/gne`.

For `use "redis"` it looks for `redis.so` (Linux), `redis.dylib` or
`redis.so` (macOS), `redis.dll` (Windows). Explicit paths work too:
`use "lib/redis.so"` loads that file directly (relative to the issuing
file). The "not found" error lists **every** candidate that was tried.

Loaded files are cached: a second `use` reuses the same namespace object;
two names pointing at the same file share one instance.

## Entry point

An extension **must** export exactly one symbol:

```c
GNE_EXPORT int gne_module_init(const gne_host_api *api, gne_ctx *ctx,
                               gne_handle *out);
```

- Build the namespace (usually `api->object()` plus `api->define_fn()` /
  `api->define_method()`), write its handle to `*out`
  (`api->module(ctx)`), or write `0` to use the default object.
- Return `0` on success. On failure: `api->throw(...)` then return
  non-zero.
- Always check `api->abi != GNE_ABI` first — that is how an extension
  rejects a runtime with a different ABI version.

## API reference

All functions live in the `const gne_host_api *api` table (saved in a
static variable during init).

| Group | Functions |
|---|---|
| Constructors | `null`, `bool_new`, `int_new`, `float_new`, `string`, `array`, `object` |
| Accessors | `type_of`, `get_bool`, `get_int`, `get_float`, `str_len`, `str_copy` |
| Containers | `len`, `arr_push`, `arr_get`, `arr_set`, `obj_set`, `obj_get`, `obj_has` |
| Functions | `define_fn`, `define_method`, `module`, `call` |
| Errors | `throw`, `failed` |
| State | `set_data`, `get_data` |
| Lifetime | `retain`, `release` |

Accessor convention: **0 = success, -1 = failure** (wrong type or handle) —
accessors never throw; the extension decides whether to reject or not.
`len` works for arrays and strings (strings: **byte** length).

## Values and handle rules

GaLang values are referenced through `gne_handle` (`uint64_t`). **0 is
never valid**; GaLang's `null` has its own handle from `api->null()`.
Handles are never reused — `release()` down to rc=0 deletes the entry, so
a stale handle yields a clear `get_*` failure instead of a silent leak.

Ownership rules (memorize these):

- **Every handle created during a call** — arguments, constructor
  results, `get_*` results, `call` results — is **host-owned
  temporary**. The host releases them all once the `cfunc` returns.
  **Do not `release()` them yourself**; just `retain()` when a handle
  must outlive the call (e.g. stored in state).
- **`*ret` transfers ownership to the host.** Never `release()` after
  returning it; if C also keeps it, `retain()` **before** returning.
- `define_method()` pins its own `self` (the host already retained it),
  so methods stay valid for the module's lifetime.
- Strings always **cross the C↔Go boundary by copy** (`str_copy` into
  your own buffer, or the `string` constructor). No host pointer may be
  stored — that is a cgo discipline requirement.

## Errors

```c
api->throw(ctx, "redis_error", 502, "connection refused");
return 1;
```

- `throw` followed by a **non-zero return** → a catchable error in
  GaLang carrying `e.code`, `e.status`, `e.message`:

```galang
try {
    redis.connect($dsn)
} catch e {
    print(e.code + "/" + str(e.status))   // redis_error/502
}
```

- Non-zero return **without** `throw` → a `gne_error` (status 500).
- Returning 0 with `*ret` left unset → engine error (program stops) —
  that is an extension bug, not a runtime condition.
- A `throw` inside a callback (`call`) is forwarded intact: the original
  code/status is preserved.
- A panic inside the host is recovered as a catchable `gne_panic` error —
  the Go runtime never dies.

## Methods and native objects

`define_method(ctx, obj, name, min, max, fn, self)` binds a function to an
object; when called, **`argv[0]` = `self`** (min/max exclude self):

```c
static int next(gne_ctx *ctx, int argc, const gne_handle *argv, gne_handle *ret) {
    /* argv[0] = the counter object */
    ...
}
/* during init: */
counter = api->object(ctx);
api->define_method(ctx, counter, "next", 0, 0, next, counter);
api->obj_set(ctx, api->module(ctx), "counter", counter);
```

```galang
$c = hello.counter
print($c.next())
```

## Calling back into GaLang

```c
gne_handle out;
if (api->call(ctx, argv[0], 1, &argv[1], &out) != 0)
    return 1;            // the callback error is already recorded in ctx
*ret = out;
```

`call` accepts anything callable (closure, builtin). If the result is a
promise (async function), pass it straight through — do not wait; the
GaLang runtime handles it.

## Per-module state

`set_data`/`get_data` hang one `void *` on **this module's instance** —
safe when one `.so` is loaded by several interpreters (state never leaks
between instances):

```c
typedef struct { ... } kv_state;

int gne_module_init(const gne_host_api *a, gne_ctx *ctx, gne_handle *out) {
    kv_state *s = calloc(1, sizeof *s);
    a->set_data(ctx, s);
    ...
}
/* inside functions: kv_state *s = a->get_data(ctx); */
```

Static globals inside the `.so` live for one process — do not use them for
per-instance state. See `kv/kv.c` in the examples repo for the full pattern
(key-value + save/load to file).

## Building

| Platform | Command |
|---|---|
| Linux | `gcc -shared -fPIC -Wall -Wextra -I <gne.h> -o x.so x.c` |
| macOS | `gcc -dynamiclib -Wall -Wextra -I <gne.h> -o x.dylib x.c` |
| Windows | `x86_64-w64-mingw32-gcc -shared -Wall -Wextra -I <gne.h> -o x.dll x.c` (MSYS2: `pacman -S mingw-w64-x86_64-gcc`) |

`gne.h` ships in **every release zip** and in the GaLang repo
(`include/gne.h`). After building, drop the result into `./gne/` or
`~/.galang/gne`.

## Limitations to know

- **Performance**: every C↔Go call crosses the cgo boundary (hundreds of
  nanoseconds per crossing). GNE is for I/O, library bindings, and heavy
  work — not the heart of a hot loop (count operations first; benchmark).
- **One goroutine**: host functions are only called from the same
  interpreter goroutine; `ctx` is valid for a single call — never store
  it or send it to another thread.
- **No unload**: modules live for the whole process (`dlclose` is never
  called) — reset state in `init` when needed.
- **No CGO, no GNE**: certain cross-builds ship a stub — `use` of an
  extension is rejected with a "CGO required" message (see the platform
  table below).

## Security

Loading a native extension means **running C code in your process** — as
risky as installing an npm package with native addons or running a
binary from a repository. GNE does not change that arithmetic, but it
narrows the path:

- only three explicit locations (`./gne`, `$GNE_PATH`, `~/.galang/gne`)
  — no `PATH`/`LD_LIBRARY_PATH` scanning that could be hijacked;
- `dlopen` with `RTLD_NOW | RTLD_LOCAL` (symbols do not leak globally);
- the ABI is checked at init; handles are never reused;
- extension authors do not need — and cannot — touch the GaLang
  compiler or runtime.

Only install extensions from sources you trust.

## Platforms

| Release zip | CGO | GNE | SQLite |
|---|---|---|---|
| `gar-linux-amd64` | ✔ | ✔ | ✔ |
| `gar-windows-amd64` | ✔ (mingw) | ✔ | ✔ |
| `gar-darwin-amd64`, `gar-darwin-arm64` | ✔ | ✔ | ✔ |
| `gar-linux/arm64`, `gar-windows/arm64` | ✘ | stub | ✘ |

`CGO=0` binaries run normally; only native extension `use` (and SQLite)
is rejected with a clear message. The darwin binaries are built on
GitHub Actions with CGO=1; for `linux/arm64` and `windows/arm64`, a
native build on that platform (e.g. `make build` on an ARM machine) gets
full GNE.

## Examples

The [galang-gne-examples](https://github.com/lnx645/galang-gne-examples)
repo:

- **`hello/`** — functions, object methods, catchable `throw`, callbacks
  into GaLang closures, state across calls;
- **`kv/`** — key-value with per-module state plus file persistence (the
  same pattern as Database/Redis extensions);
- `build.sh` / `build.ps1` — Linux/macOS/Windows builds.
