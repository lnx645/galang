# Garurda CLI

The Garurda interpreter is the `gar` binary.

## Commands

```bash
gar run <file.ga> [-- argumen...]   # run the program
gar repl [file.ga]                  # interactive session (aliases: shell, sh)
gar version                         # show the version (aliases: -v, --version)
gar help                            # help (aliases: -h, --help)
```

## Running a Program

```bash
gar run hello.ga
```

The `.ga` extension is required; missing file → an error message and exit
code 2.

### Program arguments — `--`

Everything **after `--`** is passed on to the program and available in the
global `args` variable (an array of strings):

```bash
gar run tugas.ga -- alice 30 --port 9000
```

```garurda
// tugas.ga
print(args)            // [alice, 30, --port, 9000]
print(args[0])         // alice
```

Without `--`, `args` is empty (`[]`).

### Exit code

| Code | Meaning |
|---|---|
| `0` | success |
| `1` | runtime error (message + `file:line:column` position on stderr) |
| `2` | incorrect CLI usage (missing file, unknown command) |

## REPL

```bash
gar repl               # start empty
gar repl setup.ga      # preload a script first, then the REPL is ready
```

Example session:

```text
> 2 + 2
4
> "abc".upper()
ABC
> .help
> .exit
```

### REPL Commands

| Command | Purpose |
|---|---|
| `.help` / `help` | show help |
| `.exit` / `.quit` / `exit` / `quit` | exit |

Multi-line blocks (the body of `fn`, `if`, `for`, `try`, etc.) are continued
automatically until the closing `}` brace — you don't have to write the whole
expression on one line.

Preloading `file.ga` is useful for setting up functions/modules before you
start experimenting.

## Environment

`gar run` does not read special flags; application configuration (port etc.)
is set inside the script, e.g. `http.listen(8868)`.

## Systemd Service (deployment)

Example unit for production:

```ini
[Unit]
Description=Garurda App
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/gar run /srv/app/main.ga
WorkingDirectory=/srv/app
Restart=always
RestartSec=3
User=www-data

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable gapp.service
sudo systemctl start gapp.service
sudo systemctl status gapp.service
sudo journalctl -u gapp.service -f    # tail the log (including stderr)
```

Note: internal errors in HTTP handlers are logged to **stderr** — `journalctl`
picks them up.

## Building from Source

```bash
export HOME=/root          # if HOME is empty in your environment
go build -o bin/gar ./cmd/gar
./bin/gar version
```

Running the test suite:

```bash
go test ./...
```
