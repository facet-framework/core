# @@PACKAGE@@

Launcher for the @@PRODUCT@@ native binary. It is a library for the entry
packages and has no command of its own.

`@@PACKAGE@@` lists one platform package per supported platform as an exact
optional dependency — `@@PACKAGE@@-linux-x64`, `-linux-arm64`, `-darwin-x64`,
`-darwin-arm64`, `-win32-x64`, `-win32-arm64` — and npm installs the one that
matches the machine. The launcher runs that binary with every argument
unchanged, stdin, stdout and stderr inherited, exits with the binary's exit
code and, on Linux and macOS, passes SIGINT, SIGTERM and SIGHUP on to it. On
Windows it forwards nothing: the binary receives Ctrl-C from the console
itself. It prints nothing itself unless it cannot start the binary.

If the platform package is missing, either optional dependencies were omitted
(`--omit=optional`, `--no-optional`) or node_modules was installed on another
platform and copied here. Install again on this machine without omitting them.

Requires Node.js 18 or later.
